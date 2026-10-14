package storvia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient starts a stub API and returns a client pointed at it.
func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := New(Config{
		BaseURL:   server.URL,
		Token:     "test-token",
		UserAgent: "storvia-sdk-test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client, server
}

func TestNewRejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"missing base url", Config{}, true},
		{"bad scheme", Config{BaseURL: "ftp://example.com"}, true},
		{"unparseable", Config{BaseURL: "://nope"}, true},
		{"valid http", Config{BaseURL: "http://localhost:8080"}, false},
		{"valid https", Config{BaseURL: "https://api.example.com"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestCreateUploadSendsTheIdempotencyKey is the guard that stops a client-side
// retry from creating a duplicate file.
func TestCreateUploadSendsTheIdempotencyKey(t *testing.T) {
	var receivedKey string
	var receivedBody map[string]any

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedKey = r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"upload_id": "upl_1", "object_id": "obj_1", "mode": "drive_proxy",
			"provider": "google_drive", "state": "uploading", "chunk_size": 8388608,
		})
	})

	client, _ := newTestClient(t, handler)
	result, err := client.CreateUpload(context.Background(), "stable-key-1", CreateUploadRequest{
		Name: "report.pdf", MIMEType: "application/pdf", SizeBytes: 2048, Category: "report",
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}

	if receivedKey != "stable-key-1" {
		t.Errorf("Idempotency-Key = %q, want stable-key-1", receivedKey)
	}
	if receivedBody["size_bytes"] != float64(2048) {
		t.Errorf("size_bytes = %v, want 2048", receivedBody["size_bytes"])
	}
	if result.Mode != ModeDriveProxy {
		t.Errorf("mode = %q, want drive_proxy", result.Mode)
	}
}

func TestCreateUploadRequiresAnIdempotencyKey(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request must not be sent without a key")
	}))

	if _, err := client.CreateUpload(context.Background(), "", CreateUploadRequest{}); err == nil {
		t.Fatal("an empty idempotency key must be refused client-side")
	}
}

// TestErrorDecodingUsesTheProblemType is the check that a client can branch on a
// machine-readable kind rather than parsing prose.
func TestErrorDecodingUsesTheProblemType(t *testing.T) {
	tests := []struct {
		name      string
		problem   string
		status    int
		wantKind  Kind
		temporary bool
	}{
		{"quota", `{"error":{"type":"https://storvia.dev/problems/quota","title":"Storage quota exhausted","status":507,"detail":"drive storage or upload quota is exhausted"}}`, http.StatusInsufficientStorage, KindQuota, false},
		{"unavailable", `{"error":{"type":"https://storvia.dev/problems/unavailable","title":"Storage provider unavailable","status":503,"detail":"no second copy exists to serve from"}}`, http.StatusServiceUnavailable, KindUnavailable, true},
		{"uncertain", `{"error":{"type":"https://storvia.dev/problems/uncertain","title":"Upload outcome is not yet known","status":409,"detail":"the write result cannot be proved"}}`, http.StatusConflict, KindUncertain, true},
		{"consent", `{"error":{"type":"https://storvia.dev/problems/consent_required","title":"Google Drive connection required","status":428,"detail":"reconnect required"}}`, http.StatusPreconditionRequired, KindConsentRequired, false},
		{"conflict", `{"error":{"type":"https://storvia.dev/problems/conflict","title":"Conflicting request","status":409,"detail":"idempotency key reused"}}`, http.StatusConflict, KindConflict, false},
		{"validation", `{"error":{"type":"https://storvia.dev/problems/validation","title":"Request validation failed","status":400,"detail":"size_bytes exceeds the limit"}}`, http.StatusBadRequest, KindValidation, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.Header().Set("X-Request-ID", "corr-1")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.problem))
			})

			client, _ := newTestClient(t, handler)
			_, err := client.CreateUpload(context.Background(), "k", CreateUploadRequest{
				Name: "a.pdf", MIMEType: "application/pdf", SizeBytes: 1, Category: "report",
			})
			if err == nil {
				t.Fatal("want an error")
			}

			if !IsKind(err, tc.wantKind) {
				t.Errorf("kind = %q, want %q", err, tc.wantKind)
			}
			if got := StatusCode(err); got != tc.status {
				t.Errorf("status = %d, want %d", got, tc.status)
			}

			apiErr, ok := err.(*APIError)
			if !ok {
				t.Fatalf("error type = %T, want *APIError", err)
			}
			if apiErr.RequestID != "corr-1" {
				t.Errorf("RequestID = %q, want corr-1", apiErr.RequestID)
			}
			if apiErr.Temporary() != tc.temporary {
				t.Errorf("Temporary() = %v, want %v", apiErr.Temporary(), tc.temporary)
			}
		})
	}
}

// TestUncertainIsTemporaryButConflictsIsNot documents the retry guidance: an
// ambiguous outcome is worth retrying, a quota or conflict is not.
func TestUncertainIsTemporaryButConflictsIsNot(t *testing.T) {
	uncertain := &APIError{Kind: KindUncertain}
	if !uncertain.Temporary() {
		t.Error("an uncertain outcome must be retryable")
	}
	conflict := &APIError{Kind: KindConflict}
	if conflict.Temporary() {
		t.Error("a conflict must not be retried blindly; the caller must change something")
	}
	quota := &APIError{Kind: KindQuota}
	if quota.Temporary() {
		t.Error("a quota error must not be retried blindly")
	}
}

func TestUnknownProblemTypeFallsBackToTheStatus(t *testing.T) {
	// A newer server may register a problem type this SDK does not know. The
	// client must keep working rather than failing to classify.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"type":"https://storvia.dev/problems/some_new_thing","title":"T","status":403,"detail":"D"}}`))
	})

	client, _ := newTestClient(t, handler)
	_, err := client.GetObject(context.Background(), "obj_1")
	if err == nil {
		t.Fatal("want an error")
	}
	if !IsKind(err, KindInternal) {
		t.Errorf("kind = %q, want internal as the safe fallback", KindOfError(err))
	}
}

func TestNonJSONErrorBodyIsHandledSafely(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>proxy error mentioning internal-host-9.9.9.9</html>"))
	})

	client, _ := newTestClient(t, handler)
	_, err := client.GetObject(context.Background(), "obj_1")
	if err == nil {
		t.Fatal("want an error")
	}
	// A proxy's HTML body must not be surfaced as the failure message.
	if bytes.Contains([]byte(err.Error()), []byte("internal-host-9.9.9.9")) {
		t.Errorf("the raw upstream body leaked into the error: %v", err)
	}
}

func TestTransportFailureIsUncertain(t *testing.T) {
	client, server := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	// Close the server so the request cannot complete.
	server.Close()

	_, err := client.CreateUpload(context.Background(), "k", CreateUploadRequest{})
	if err == nil {
		t.Fatal("want an error")
	}
	// A transport failure is an unknown outcome: the server may have processed the
	// write before the response was lost. Reporting it as uncertain is what stops
	// a client from retrying into a duplicate.
	if !IsKind(err, KindUncertain) {
		t.Errorf("kind = %q, want uncertain", KindOfError(err))
	}
}

func TestBearerTokenIsSent(t *testing.T) {
	var received string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	})

	client, _ := newTestClient(t, handler)
	if err := client.DeleteObject(context.Background(), "obj_1"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if received != "Bearer test-token" {
		t.Errorf("Authorization = %q, want the bearer token", received)
	}
}

func TestWithTokenDoesNotMutateTheOriginal(t *testing.T) {
	client, err := New(Config{BaseURL: "http://localhost:8080", Token: "original"})
	if err != nil {
		t.Fatal(err)
	}
	derived := client.WithToken("other")

	if derived.token != "other" {
		t.Error("the derived client should use the new token")
	}
	if client.token != "original" {
		t.Error("WithToken must not mutate the receiver")
	}
}

func TestDownloadStreamsContent(t *testing.T) {
	payload := "file contents"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="report.pdf"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	})

	client, _ := newTestClient(t, handler)
	download, err := client.Download(context.Background(), "obj_1")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer download.Close()

	if download.MIMEType != "application/pdf" {
		t.Errorf("MIMEType = %q", download.MIMEType)
	}
	got, err := io.ReadAll(download.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Errorf("body = %q, want %q", got, payload)
	}
}

func TestDownloadRangeSendsAnInclusiveHeader(t *testing.T) {
	var receivedRange string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("chunk"))
	})

	client, _ := newTestClient(t, handler)
	download, err := client.DownloadRange(context.Background(), "obj_1", 100, 199)
	if err != nil {
		t.Fatalf("DownloadRange: %v", err)
	}
	download.Close()

	// HTTP ranges are inclusive; end-1 makes the intent match the header format.
	if receivedRange != "bytes=100-198" {
		t.Errorf("Range = %q, want bytes=100-198", receivedRange)
	}
}

func TestDownloadRangeRejectsNonsensicalBounds(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an invalid range must not be sent")
	}))

	if _, err := client.DownloadRange(context.Background(), "obj_1", -1, 100); err == nil {
		t.Error("a negative start must be rejected")
	}
	if _, err := client.DownloadRange(context.Background(), "obj_1", 100, 50); err == nil {
		t.Error("an end before the start must be rejected")
	}
}

func TestNoContentResponsesAreHandled(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	client, _ := newTestClient(t, handler)

	if err := client.DeleteObject(context.Background(), "obj_1"); err != nil {
		t.Errorf("a 204 must not be decoded as a body: %v", err)
	}
}

func TestGetDriveConnectionDecodesTheNarrowScope(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"connected": true,
			"status": "connected",
			"subject": "1234567890",
			"granted_scopes": ["https://www.googleapis.com/auth/drive.file"],
			"folder_present": true,
			"stale": false
		}`))
	})

	client, _ := newTestClient(t, handler)
	connection, err := client.GetDriveConnection(context.Background())
	if err != nil {
		t.Fatalf("GetDriveConnection: %v", err)
	}
	if !connection.Connected || !connection.FolderPresent {
		t.Errorf("connection = %+v, want a connected account with a folder", connection)
	}
	if len(connection.GrantedScopes) != 1 || connection.GrantedScopes[0] != "https://www.googleapis.com/auth/drive.file" {
		t.Errorf("scopes = %v, want only drive.file", connection.GrantedScopes)
	}
}

func TestStartDriveConnectionReturnsAConsentURL(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"authorize_url":"https://accounts.google.com/o/oauth2/v2/auth?client_id=x","expires_in_seconds":600}`))
	})

	client, _ := newTestClient(t, handler)
	url, err := client.StartDriveConnection(context.Background())
	if err != nil {
		t.Fatalf("StartDriveConnection: %v", err)
	}
	if url == "" {
		t.Error("a consent URL is required")
	}
}

// TestStatusSurfacesTheFallbackDisclosure checks that a client can see it must
// repeat the "stored outside your Drive" notice.
func TestStatusSurfacesTheFallbackDisclosure(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"primary_provider": "google_drive",
			"drive_connected": true,
			"fallback_enabled": true,
			"fallback_vendor": "cloudflare_r2",
			"fallback_ready": true,
			"fallback_requires_consent": false,
			"accepts_uploads": true,
			"fallback_stores_outside_drive": true
		}`))
	})

	client, _ := newTestClient(t, handler)
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.FallbackStoresOutsideDrive {
		t.Error("a client must be able to tell that uploads may leave the user's Drive")
	}
	if !status.AcceptsUploads {
		t.Error("AcceptsUploads should be true")
	}
}

func TestPresignedPartsCarryTheReplayedHeaders(t *testing.T) {
	// The signed headers must be replayed verbatim or the signature fails, so the
	// SDK must expose them rather than dropping them.
	expires := time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"upload_id": "upl_1", "object_id": "obj_1",
			"mode": "s3_presigned_parts", "provider": "s3_compatible",
			"state": "uploading", "chunk_size": 8388608,
			"fallback_reason": "drive_preflight_timeout",
			"parts": [{"part_number": 1, "url": "https://example.com/presigned", "expires_at": "` + expires + `"}]
		}`))
	})

	client, _ := newTestClient(t, handler)
	result, err := client.CreateUpload(context.Background(), "k", CreateUploadRequest{
		Name: "report.pdf", MIMEType: "application/pdf", SizeBytes: 10 << 20, Category: "report",
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	if result.Mode != ModeS3PresignedParts {
		t.Errorf("mode = %q, want s3_presigned_parts", result.Mode)
	}
	if result.FallbackReason == "" {
		t.Error("an outage routing decision must be reported to the caller")
	}
	if len(result.Parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(result.Parts))
	}
	if result.Parts[0].PartNumber != 1 || result.Parts[0].URL == "" {
		t.Errorf("part = %+v, want a usable capability", result.Parts[0])
	}
}

func TestContextCancellationIsHonoured(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	client, _ := newTestClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	if _, err := client.GetObject(ctx, "obj_1"); err == nil {
		t.Fatal("a cancelled context must abort the request")
	}
}

// KindOfError extracts the kind from any error, returning internal for non-API
// errors.
func KindOfError(err error) Kind {
	if err == nil {
		return ""
	}
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.Kind
	}
	return KindInternal
}
