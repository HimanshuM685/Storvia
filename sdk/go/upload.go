package storvia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Download is a streamed response.
//
// Close it when finished; not closing it leaks the connection and prevents reuse.
type Download struct {
	Body     io.ReadCloser
	MIMEType string
	// SizeBytes is -1 when the server did not send a Content-Length.
	SizeBytes int64
	ETag      string

	closeFn func() error
}

// Close releases the response body.
func (d *Download) Close() error {
	if d == nil || d.closeFn == nil {
		return nil
	}
	return d.closeFn()
}

// DriveSession is a Drive resumable upload session.
//
// The URL is a write capability for exactly one file: anyone holding it can write to
// that file until the session expires. Treat it as a secret. Storvia hands it out once
// and never stores it, so it cannot be recovered if lost.
type DriveSession struct {
	URL       string    `json:"url"`
	ChunkSize int       `json:"chunk_size"`
	ExpiresAt time.Time `json:"expires_at"`
}

// driveCompleteResponse is what Drive returns from the final chunk of a resumable
// upload. Storvia does not read it — the browser does, and reports the id back — but
// the client needs it to complete.
type driveCompleteResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size string `json:"size"`
	MD5  string `json:"md5Checksum"`
}

// Upload handles both transfer modes.
//
// Drive is uploaded through a resumable session that the client PUTs to directly, and
// the fallback through presigned part URLs. Both are direct-to-provider: no file bytes
// pass through Storvia.
type Upload struct {
	client *Client
	result *CreateUploadResult
	size   int64
	offset int64
}

// NewUpload starts an upload and returns a handle for moving its bytes.
//
// The mode is whatever the server selected. Use DriveMode or PresignedMode to branch.
func (c *Client) NewUpload(ctx context.Context, idempotencyKey string, req CreateUploadRequest) (*Upload, error) {
	result, err := c.CreateUpload(ctx, idempotencyKey, req)
	if err != nil {
		return nil, err
	}
	return &Upload{client: c, result: result, size: req.SizeBytes}, nil
}

// ID returns the upload identifier.
func (u *Upload) ID() string { return u.result.UploadID }

// ObjectID returns the logical object identifier.
func (u *Upload) ObjectID() string { return u.result.ObjectID }

// UploadedBytes returns how many bytes this client has successfully sent.
//
// This is local accounting, not a provider-confirmed offset. Storvia does not keep the
// Drive session, so it cannot report what Google durably accepted, and a number that
// looked provider-confirmed but was not would be worse than none.
func (u *Upload) UploadedBytes() int64 { return u.offset }

// DriveMode reports whether this upload goes through a Drive resumable session.
func (u *Upload) DriveMode() bool {
	return u.result.Mode == ModeDriveResumableSession
}

// PresignedMode reports whether this upload goes through fallback presigned parts.
func (u *Upload) PresignedMode() bool {
	return u.result.Mode == ModeS3PresignedParts
}

// UploadAll sends every byte of r to the provider, in the mode the server chose.
//
// It is the whole transfer in one call. On error the upload handle is still usable:
// the offset is preserved and the transfer can be resumed.
func (u *Upload) UploadAll(ctx context.Context, r io.Reader) error {
	switch u.result.Mode {
	case ModeDriveResumableSession:
		return u.uploadDrive(ctx, r)
	case ModeS3PresignedParts:
		return u.uploadPresigned(ctx, r)
	default:
		return &APIError{
			Kind:   KindUnavailable,
			Title:  "Unsupported upload mode",
			Detail: fmt.Sprintf("the server selected %q, which this client does not implement", u.result.Mode),
		}
	}
}

// uploadDrive streams the file to Google's resumable session.
//
// Every chunk except the last is a multiple of 256 KiB, because Drive rejects
// anything else, and each chunk declares its own byte range so a retry cannot
// duplicate data.
func (u *Upload) uploadDrive(ctx context.Context, r io.Reader) error {
	if u.result.Session == nil || u.result.Session.URL == "" {
		return &APIError{
			Kind:   KindUnavailable,
			Title:  "No Drive session",
			Detail: "the server did not return a resumable session for this upload",
		}
	}

	chunkSize := u.result.ChunkSize
	if chunkSize <= 0 {
		chunkSize = u.result.Session.ChunkSize
	}
	if chunkSize <= 0 {
		chunkSize = 256 * 1024
	}

	buffer := make([]byte, chunkSize)
	sent := int64(0)

	for {
		n, readErr := io.ReadFull(r, buffer)
		if n > 0 {
			if err := u.putDriveChunk(ctx, buffer[:n], sent); err != nil {
				return err
			}
			sent += int64(n)
			u.offset = sent
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("storvia: reading the file: %w", readErr)
		}
	}
}

// putDriveChunk sends one chunk with its absolute byte range.
func (u *Upload) putDriveChunk(ctx context.Context, chunk []byte, offset int64) error {
	end := offset + int64(len(chunk)) - 1

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.result.Session.URL, bytes.NewReader(chunk))
	if err != nil {
		return fmt.Errorf("storvia: building the drive chunk request: %w", err)
	}
	req.ContentLength = int64(len(chunk))
	req.Header.Set("Content-Type", "application/octet-stream")
	// Declaring the exact range is what makes a retry safe: without it Drive cannot
	// tell a resent chunk from the original and the file ends up with a hole in it.
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, end, u.size))

	resp, err := u.client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("storvia: sending a chunk to google drive: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()

	// 308 means "more to come", which is expected for every chunk but the last.
	// 200 and 201 mean the file is complete.
	if resp.StatusCode == http.StatusPermanentRedirect ||
		resp.StatusCode == http.StatusOK ||
		resp.StatusCode == http.StatusCreated {
		return nil
	}
	return &APIError{
		Kind:   KindUnavailable,
		Title:  "Drive rejected a chunk",
		Detail: fmt.Sprintf("google returned %d for the byte range %d-%d", resp.StatusCode, offset, end),
	}
}

// uploadPresigned sends the file to the fallback using the presigned part URLs.
func (u *Upload) uploadPresigned(ctx context.Context, r io.Reader) error {
	parts := u.result.Parts
	if len(parts) == 0 {
		return &APIError{
			Kind:   KindUnavailable,
			Title:  "No presigned parts",
			Detail: "the server returned no upload capabilities for this upload",
		}
	}

	buffer := make([]byte, u.result.ChunkSize)
	sent := int64(0)

	for _, part := range parts {
		n, readErr := io.ReadFull(r, buffer)
		if n == 0 {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("storvia: reading the file: %w", readErr)
		}
		if err := u.putPresignedPart(ctx, part, buffer[:n]); err != nil {
			return err
		}
		sent += int64(n)
		u.offset = sent
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	return nil
}

// putPresignedPart uploads one part to the fallback provider.
func (u *Upload) putPresignedPart(ctx context.Context, part PresignedPart, chunk []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, part.URL, bytes.NewReader(chunk))
	if err != nil {
		return fmt.Errorf("storvia: building the part request: %w", err)
	}
	req.ContentLength = int64(len(chunk))
	for key, values := range part.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := u.client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("storvia: sending a part to the fallback provider: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode/100 != 2 {
		return &APIError{
			Kind:   KindUnavailable,
			Title:  "Fallback provider rejected a part",
			Detail: fmt.Sprintf("part %d returned %d", part.PartNumber, resp.StatusCode),
		}
	}
	return nil
}

// Complete finalizes a presigned upload and returns the stored object.
//
// It is idempotent: repeating it returns the existing verified object and never
// creates a second location.
func (u *Upload) Complete(ctx context.Context) (*Object, error) {
	if u.result.Mode == ModeDriveResumableSession {
		return nil, &APIError{
			Kind:   KindValidation,
			Title:  "Drive uploads need a file id",
			Detail: "call CompleteDrive with the file id google returned from the final chunk",
		}
	}
	var object Object
	path := "/v1/storage/uploads/" + u.result.UploadID + "/complete"
	if err := u.client.do(ctx, http.MethodPost, path, nil, &object); err != nil {
		return nil, err
	}
	return &object, nil
}

// CompleteDrive finalizes a Drive upload.
//
// driveFileID is the id Google returned in the final chunk response. Storvia treats it
// as untrusted input and re-reads the file from Drive, adopting it only if it is inside
// the Storvia folder, is not trashed, and has the declared size.
func (u *Upload) CompleteDrive(ctx context.Context, driveFileID string) (*Object, error) {
	if driveFileID == "" {
		return nil, &APIError{
			Kind:   KindValidation,
			Title:  "A Drive file id is required",
			Detail: "google returns the file id in the final chunk response of a resumable upload",
		}
	}
	body := map[string]string{"drive_file_id": driveFileID}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("storvia: encoding the completion request: %w", err)
	}

	var response completeResponse
	path := "/v1/storage/uploads/" + u.result.UploadID + "/complete"
	if err := u.client.do(ctx, http.MethodPost, path, bytes.NewReader(encoded), &response); err != nil {
		return nil, err
	}
	return &response.Object, nil
}

// completeResponse is the shape of the complete endpoint's reply.
type completeResponse struct {
	Object          Object `json:"object"`
	AlreadyComplete bool   `json:"already_complete"`
}

// Status returns the current upload state.
func (u *Upload) Status(ctx context.Context) (*UploadView, error) {
	return u.client.GetUpload(ctx, u.result.UploadID)
}

// Reconcile asks the server what actually happened to an unfinished upload.
//
// It never retries and never re-uploads. Read the outcome before deciding to start
// again: an upload that is merely unknown may well have succeeded.
func (u *Upload) Reconcile(ctx context.Context) (*ReconcileResult, error) {
	return u.client.ReconcileUpload(ctx, u.result.UploadID)
}
