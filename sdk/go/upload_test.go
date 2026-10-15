package storvia

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sessionRecorder stands in for Google's resumable upload endpoint and records the
// byte ranges it was sent, which is the only way to prove the chunking is correct.
type sessionRecorder struct {
	ranges  []string
	chunks  int
	lastLen int
	total   int64
	failAt  int
}

func (s *sessionRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.chunks++
	s.lastLen = len(body)
	s.total += int64(len(body))
	s.ranges = append(s.ranges, r.Header.Get("Content-Range"))

	if s.failAt > 0 && s.chunks == s.failAt {
		http.Error(w, "simulated provider failure", http.StatusServiceUnavailable)
		return
	}
	// The final chunk answers 200 with the created file resource; intermediate ones
	// answer 308 to ask for more.
	if strings.Contains(r.Header.Get("Content-Range"), "/"+fmt.Sprint(totalOf(r))) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-123","name":"report.pdf","size":"1048576"}`))
		return
	}
	w.WriteHeader(http.StatusPermanentRedirect)
}

func totalOf(r *http.Request) int {
	total := 0
	for i := len(r.Header.Get("Content-Range")) - 1; i >= 0; i-- {
		if r.Header.Get("Content-Range")[i] == '/' {
			fmt.Sscanf(r.Header.Get("Content-Range")[i+1:], "%d", &total)
			break
		}
	}
	return total
}

// startDriveUpload wires a client whose create-upload returns a Drive session.
func startDriveUpload(t *testing.T, recorder *sessionRecorder, size int64) (*Upload, *httptest.Server) {
	t.Helper()

	session := httptest.NewServer(recorder)
	t.Cleanup(session.Close)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/storage/uploads":
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"upload_id":"upl_1","object_id":"obj_1",
				"mode":"drive_resumable_session","provider":"google_drive","state":"uploading",
				"chunk_size":262144,"session":{"url":%q,"chunk_size":262144}}`, session.URL+"/upload")
		case "/v1/storage/uploads/upl_1/complete":
			_, _ = w.Write([]byte(`{"object":{"object_id":"obj_1","status":"ready","name":"report.pdf"}}`))
		default:
			http.Error(w, "no route", http.StatusNotFound)
		}
	}))
	t.Cleanup(api.Close)

	client, err := New(Config{BaseURL: api.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	upload, err := client.NewUpload(context.Background(), "key-1", CreateUploadRequest{
		Name: "report.pdf", MIMEType: "application/pdf", SizeBytes: size, Category: "report",
	})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if !upload.DriveMode() {
		t.Fatal("the upload is not in Drive mode")
	}
	return upload, api
}

// TestUploadAllSendsEveryChunkWithItsRange is the contract that makes a resumable
// upload safe to retry: each chunk must declare where it belongs, or a resend
// silently corrupts the file.
func TestUploadAllSendsEveryChunkWithItsRange(t *testing.T) {
	const size = 700 * 1024 // 2 full 256 KiB chunks plus a remainder
	recorder := &sessionRecorder{}
	upload, _ := startDriveUpload(t, recorder, size)

	payload := bytes.Repeat([]byte("x"), size)
	if err := upload.UploadAll(context.Background(), bytes.NewReader(payload)); err != nil {
		t.Fatalf("UploadAll: %v", err)
	}

	if recorder.total != size {
		t.Errorf("sent %d bytes, want %d", recorder.total, size)
	}
	if recorder.chunks != 3 {
		t.Fatalf("sent %d chunks, want 3 (two full plus a remainder)", recorder.chunks)
	}
	wantFirst := "bytes 0-262143/" + fmt.Sprint(size)
	if recorder.ranges[0] != wantFirst {
		t.Errorf("first range = %q, want %q", recorder.ranges[0], wantFirst)
	}
	wantLast := fmt.Sprintf("bytes %d-%d/%d", size-recorder.lastLen, size-1, size)
	if recorder.ranges[len(recorder.ranges)-1] != wantLast {
		t.Errorf("last range = %q, want %q", recorder.ranges[len(recorder.ranges)-1], wantLast)
	}

	// Only the final chunk may be a non-multiple of Drive's granularity.
	for i, r := range recorder.ranges[:len(recorder.ranges)-1] {
		if !strings.HasPrefix(r, fmt.Sprintf("bytes %d-", i*262144)) {
			t.Errorf("chunk %d range = %q, want it to start at %d", i, r, i*262144)
		}
	}
	if upload.UploadedBytes() != size {
		t.Errorf("UploadedBytes = %d, want %d", upload.UploadedBytes(), size)
	}
}

// TestUploadAllHandlesAFileSmallerThanOneChunk: a single sub-granularity chunk is
// legal and must not be padded.
func TestUploadAllHandlesAFileSmallerThanOneChunk(t *testing.T) {
	const size = 100
	recorder := &sessionRecorder{}
	upload, _ := startDriveUpload(t, recorder, size)

	if err := upload.UploadAll(context.Background(), bytes.NewReader(bytes.Repeat([]byte("y"), size))); err != nil {
		t.Fatalf("UploadAll: %v", err)
	}
	if recorder.chunks != 1 {
		t.Errorf("sent %d chunks, want 1", recorder.chunks)
	}
	if recorder.total != size {
		t.Errorf("sent %d bytes, want %d", recorder.total, size)
	}
}

// TestUploadAllReportsAFailedChunkWithoutLosingTheOffset. A mid-transfer failure must
// leave a usable offset rather than resetting to zero, or a retry would resend bytes
// the provider already accepted.
func TestUploadAllReportsAFailedChunkWithoutLosingTheOffset(t *testing.T) {
	const size = 1024 * 1024
	recorder := &sessionRecorder{failAt: 3}
	upload, _ := startDriveUpload(t, recorder, size)

	err := upload.UploadAll(context.Background(), bytes.NewReader(bytes.Repeat([]byte("z"), size)))
	if err == nil {
		t.Fatal("a rejected chunk must be reported")
	}
	if upload.UploadedBytes() == 0 {
		t.Error("the offset must survive a failure so the transfer can be resumed")
	}
	if upload.UploadedBytes() >= size {
		t.Error("the offset must not claim bytes that were rejected")
	}
}

// TestCompleteDriveSendsTheFileID guards the completion contract: the client must
// report the id Google gave it, because Storvia does not hold the session.
func TestCompleteDriveSendsTheFileID(t *testing.T) {
	recorder := &sessionRecorder{}
	upload, _ := startDriveUpload(t, recorder, 100)

	if err := upload.UploadAll(context.Background(), bytes.NewReader(bytes.Repeat([]byte("w"), 100))); err != nil {
		t.Fatalf("UploadAll: %v", err)
	}

	object, err := upload.CompleteDrive(context.Background(), "file-123")
	if err != nil {
		t.Fatalf("CompleteDrive: %v", err)
	}
	if object.ObjectID != "obj_1" || object.Status != "ready" {
		t.Errorf("object = %+v, want obj_1/ready", object)
	}
}

// TestCompleteDriveRequiresAFileID keeps the failure client-side rather than spending
// a round trip on a request the server will refuse.
func TestCompleteDriveRequiresAFileID(t *testing.T) {
	recorder := &sessionRecorder{}
	upload, _ := startDriveUpload(t, recorder, 100)

	if _, err := upload.CompleteDrive(context.Background(), ""); err == nil {
		t.Fatal("an empty file id must be refused")
	}
}

// TestCompleteRefusesADriveUpload: the plain Complete has no file id to send, so
// pointing a caller at it would be a confusing dead end.
func TestCompleteRefusesADriveUpload(t *testing.T) {
	recorder := &sessionRecorder{}
	upload, _ := startDriveUpload(t, recorder, 100)

	if _, err := upload.Complete(context.Background()); err == nil {
		t.Fatal("Complete must point a Drive caller at CompleteDrive")
	}
}
