package storvia

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// UploadMode is the transfer mechanism the server selected.
//
// The client never chooses a provider. It reports which mode to use, because the
// two modes are genuinely different protocols and pretending otherwise is how a
// client ends up uploading bytes to the wrong place.
type UploadMode string

const (
	// ModeDriveResumableSession uploads directly to a Drive resumable session URI.
	// Google mints the capability and Storvia relays it, so no file bytes pass
	// through the API.
	ModeDriveResumableSession UploadMode = "drive_resumable_session"
	// ModeS3PresignedParts uploads parts directly with short-lived, scoped URLs.
	ModeS3PresignedParts UploadMode = "s3_presigned_parts"
)

// PresignedPart is one short-lived upload capability.
//
// URL is a bearer token. Treat it like a credential: do not log it, do not store
// it, and do not share it beyond the upload it belongs to.
type PresignedPart struct {
	PartNumber int                 `json:"part_number"`
	URL        string              `json:"url"`
	Headers    map[string][]string `json:"headers,omitempty"`
	ExpiresAt  time.Time           `json:"expires_at"`
}

// CreateUploadRequest describes a new upload.
type CreateUploadRequest struct {
	// Name is the display name. It is not used as a storage key.
	Name string `json:"name"`
	// MIMEType is untrusted metadata used for routing and download headers.
	MIMEType string `json:"mime_type"`
	// SizeBytes must be positive and within the server's limit for the category.
	SizeBytes int64 `json:"size_bytes"`
	// Category selects a configured size limit.
	Category string `json:"category"`
}

// CreateUploadResult is the server's routing decision plus transfer instructions.
type CreateUploadResult struct {
	UploadID string     `json:"upload_id"`
	ObjectID string     `json:"object_id"`
	Mode     UploadMode `json:"mode"`
	// Provider is where the server will store this upload.
	Provider string `json:"provider"`
	State    string `json:"state"`
	// Replayed is true when this is the answer to an idempotent repeat rather than
	// a new provider write.
	Replayed bool `json:"replayed"`
	// FallbackReason is non-empty only when Drive was unavailable and the upload
	// was routed to the company-managed fallback.
	FallbackReason string `json:"fallback_reason,omitempty"`
	// ChunkSize is the byte size to use per chunk or part.
	ChunkSize int `json:"chunk_size"`
	// UploadedBytes is the confirmed offset on a replay or resume.
	UploadedBytes int64           `json:"uploaded_bytes"`
	ExpiresAt     *time.Time      `json:"expires_at,omitempty"`
	Parts         []PresignedPart `json:"parts,omitempty"`
	// Session is set in Drive mode.
	//
	// Its URL is a write capability for this one file. Storvia returns it once and
	// never stores it, so a replayed create-upload will not return it again.
	Session *DriveSession `json:"session,omitempty"`
}

// CreateUpload starts a new upload.
//
// idempotencyKey must be stable across retries of the same logical request. Passing
// a fresh key on every attempt defeats the protection and can create duplicate
// files.
func (c *Client) CreateUpload(ctx context.Context, idempotencyKey string, req CreateUploadRequest) (*CreateUploadResult, error) {
	if idempotencyKey == "" {
		return nil, errors.New("storvia: an idempotency key is required; a fresh key per attempt can create duplicate files")
	}

	var result CreateUploadResult
	if err := c.do(ctx, http.MethodPost, "/v1/storage/uploads", req, &result,
		withIdempotencyKey(idempotencyKey)); err != nil {
		return nil, err
	}
	return &result, nil
}

// UploadView is the state of an upload.
type UploadView struct {
	UploadID string `json:"upload_id"`
	ObjectID string `json:"object_id"`
	State    string `json:"state"`
	Provider string `json:"provider"`
	// FallbackReason explains an S3 routing decision.
	FallbackReason string `json:"fallback_reason,omitempty"`
	// UploadedBytes is the provider-confirmed offset.
	UploadedBytes int64  `json:"uploaded_bytes"`
	ExpectedBytes int64  `json:"expected_bytes"`
	AttemptState  string `json:"attempt_state"`
	// MutationMayHaveStarted tells a caller whether resending is safe. When it is
	// true the upload is pinned to its provider and must be reconciled, never
	// restarted somewhere else.
	MutationMayHaveStarted bool   `json:"mutation_may_have_started"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

// GetUpload reads an upload's state.
func (c *Client) GetUpload(ctx context.Context, uploadID string) (*UploadView, error) {
	if uploadID == "" {
		return nil, errors.New("storvia: an upload ID is required")
	}
	var view UploadView
	if err := c.do(ctx, http.MethodGet, "/v1/storage/uploads/"+uploadID, nil, &view); err != nil {
		return nil, err
	}
	return &view, nil
}

// ReconcileResult is what the server found when it asked the provider.
type ReconcileResult struct {
	UploadID string `json:"upload_id"`
	ObjectID string `json:"object_id"`
	Provider string `json:"provider"`
	State    string `json:"state"`
	// Outcome is one of:
	//
	//	already_ready     the upload finished long ago
	//	completed         the provider has the bytes; Storvia has recorded that
	//	nothing_stored    the provider is certain nothing landed; safe to retry
	//	size_mismatch     something is there but it is not what was declared
	//	unknown           nobody can tell, which is NOT the same as nothing_stored
	//
	// The difference between nothing_stored and unknown matters. Only the first one
	// tells you it is safe to start the upload again.
	Outcome  string `json:"outcome"`
	Reason   string `json:"reason,omitempty"`
	Location string `json:"location,omitempty"`
}

// ReconcileUpload asks the server what actually happened to an unfinished upload.
//
// It inspects only the provider the upload is already pinned to, is safe to call
// repeatedly, and never creates a second storage location. It reports; it does not
// retry on your behalf.
func (c *Client) ReconcileUpload(ctx context.Context, uploadID string) (*ReconcileResult, error) {
	var result ReconcileResult
	if err := c.do(ctx, http.MethodPost,
		"/v1/storage/uploads/"+uploadID+"/reconcile", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelUpload abandons an upload that has not begun writing bytes.
//
// The server refuses this once the upload may already have written data; that
// refusal is correct and must not be worked around by starting a new upload.
func (c *Client) CancelUpload(ctx context.Context, uploadID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/storage/uploads/"+uploadID, nil, nil)
}

// Object describes a stored file.
//
// ProviderFileID and object key are deliberately absent: the client addresses
// files by Storvia's own identifier and never holds a provider pointer.
type Object struct {
	ObjectID       string `json:"object_id"`
	Name           string `json:"name"`
	MIMEType       string `json:"mime_type"`
	SizeBytes      int64  `json:"size_bytes"`
	Status         string `json:"status"`
	Provider       string `json:"provider"`
	Category       string `json:"category"`
	FallbackReason string `json:"fallback_reason,omitempty"`
	VerifiedAt     string `json:"verified_at,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// GetObject reads an object's metadata.
func (c *Client) GetObject(ctx context.Context, objectID string) (*Object, error) {
	var object Object
	if err := c.do(ctx, http.MethodGet, "/v1/storage/objects/"+objectID, nil, &object); err != nil {
		return nil, err
	}
	return &object, nil
}

// DeleteObject removes an object from its recorded location.
//
// The deletion is pinned to wherever the file was actually written. If Drive has
// recovered since an outage upload, the file is still removed from the fallback,
// because that is where the only copy lives.
func (c *Client) DeleteObject(ctx context.Context, objectID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/storage/objects/"+objectID, nil, nil)
}

// Download streams an object's content.
//
// The returned reader is positioned at the start; the caller closes it. There is
// no second location to fall back to, so a provider outage surfaces here as an
// error rather than as a transparent retry.
func (c *Client) Download(ctx context.Context, objectID string) (*Download, error) {
	if objectID == "" {
		return nil, errors.New("storvia: an object ID is required")
	}
	endpoint := c.baseURL.JoinPath("/v1/storage/objects/" + objectID + "/content")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("storvia: build request: %w", err)
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	request.Header.Set("User-Agent", c.userAgent)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, &APIError{
			Kind:   KindUnavailable,
			Title:  "Download could not start",
			Detail: "the request to the API did not complete",
		}
	}
	if response.StatusCode >= 400 {
		defer response.Body.Close()
		return nil, decodeProblem(response)
	}

	return &Download{
		Body:      response.Body,
		MIMEType:  response.Header.Get("Content-Type"),
		SizeBytes: response.ContentLength,
		ETag:      response.Header.Get("ETag"),
		closeFn:   response.Body.Close,
	}, nil
}

// DownloadRange streams a byte range of an object.
func (c *Client) DownloadRange(ctx context.Context, objectID string, start, end int64) (*Download, error) {
	if start < 0 {
		return nil, errors.New("storvia: a range start cannot be negative")
	}
	if end > 0 && end <= start {
		return nil, errors.New("storvia: a range end must be greater than its start")
	}

	endpoint := c.baseURL.JoinPath("/v1/storage/objects/" + objectID + "/content")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("storvia: build request: %w", err)
	}
	// The header is inclusive at the end, matching HTTP rather than Go slicing.
	if end > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))
	} else {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, &APIError{Kind: KindUnavailable, Title: "Download could not start", Detail: "the request did not complete"}
	}
	if response.StatusCode >= 400 {
		defer response.Body.Close()
		return nil, decodeProblem(response)
	}

	return &Download{
		Body:      response.Body,
		MIMEType:  response.Header.Get("Content-Type"),
		SizeBytes: response.ContentLength,
		ETag:      response.Header.Get("ETag"),
		closeFn:   response.Body.Close,
	}, nil
}

// StorageStatus reports whether Storvia can accept an upload and why.
type StorageStatus struct {
	PrimaryProvider         string `json:"primary_provider"`
	DriveConnected          bool   `json:"drive_connected"`
	FallbackEnabled         bool   `json:"fallback_enabled"`
	FallbackVendor          string `json:"fallback_vendor,omitempty"`
	FallbackReady           bool   `json:"fallback_ready"`
	FallbackRequiresConsent bool   `json:"fallback_requires_consent"`
	// AcceptsUploads is the single answer needed before offering an upload control.
	AcceptsUploads bool   `json:"accepts_uploads"`
	BlockedReason  string `json:"blocked_reason,omitempty"`
	// FallbackStoresOutsideDrive is true when outage uploads may be stored outside
	// the user's Drive. Show the disclosure rather than implying Drive-only storage.
	FallbackStoresOutsideDrive bool `json:"fallback_stores_outside_drive"`
}

// Status reports the storage posture.
func (c *Client) Status(ctx context.Context) (*StorageStatus, error) {
	var status StorageStatus
	if err := c.do(ctx, http.MethodGet, "/v1/storage/status", nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DriveConnection describes a Drive connection without any credential.
type DriveConnection struct {
	Connected     bool     `json:"connected"`
	Status        string   `json:"status"`
	Subject       string   `json:"subject,omitempty"`
	GrantedScopes []string `json:"granted_scopes,omitempty"`
	FolderPresent bool     `json:"folder_present"`
	LastSuccessAt string   `json:"last_success_at,omitempty"`
	Stale         bool     `json:"stale"`
}

// GetDriveConnection reads the Drive connection state.
func (c *Client) GetDriveConnection(ctx context.Context) (*DriveConnection, error) {
	var connection DriveConnection
	if err := c.do(ctx, http.MethodGet, "/v1/storage/connections/google-drive", nil, &connection); err != nil {
		return nil, err
	}
	return &connection, nil
}

// startConnectionResponse carries the consent URL.
type startConnectionResponse struct {
	AuthorizeURL string `json:"authorize_url"`
	ExpiresIn    int    `json:"expires_in_seconds"`
}

// StartDriveConnection begins the Drive consent flow.
//
// Send the browser to AuthorizeURL. The returned URL is shown exactly once and
// should not be logged.
func (c *Client) StartDriveConnection(ctx context.Context) (string, error) {
	var response startConnectionResponse
	if err := c.do(ctx, http.MethodPost, "/v1/storage/connections/google-drive/start", nil, &response); err != nil {
		return "", err
	}
	if response.AuthorizeURL == "" {
		return "", errors.New("storvia: the server returned an empty consent URL")
	}
	return response.AuthorizeURL, nil
}

// DisconnectDrive removes the connection.
//
// New Drive requests and new uploads stop, and the user's own Drive files are
// left untouched. Files already written to the outage fallback remain there and
// are managed under the published retention policy.
func (c *Client) DisconnectDrive(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/v1/storage/connections/google-drive", nil, nil)
}

// WithToken returns a copy of the client using a different credential.
//
// It exists so a caller can hold one configured client and derive per-request
// credentials without racing on a shared mutable field.
func (c *Client) WithToken(token string) *Client {
	return &Client{
		baseURL:    c.baseURL,
		token:      token,
		httpClient: c.httpClient,
		userAgent:  c.userAgent,
	}
}
