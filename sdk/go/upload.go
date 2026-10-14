package storvia

import (
	"context"
	"io"
	"net/http"
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

// Upload is a convenience wrapper for the Drive proxy upload flow.
//
// It is provided because the proxy flow is the common case: chunks travel through
// the API so the Drive session URI never leaves the server. The S3 presigned flow
// needs no helper, because the client uploads parts directly with the URLs the
// server hands back.
type Upload struct {
	client *Client
	result *CreateUploadResult
	offset int64
	size   int64
}

// NewUpload starts an upload and returns a handle for streaming its bytes.
func (c *Client) NewUpload(ctx context.Context, idempotencyKey string, req CreateUploadRequest) (*Upload, error) {
	result, err := c.CreateUpload(ctx, idempotencyKey, req)
	if err != nil {
		return nil, err
	}
	if result.Mode != ModeDriveProxy {
		return nil, &APIError{
			Kind:   KindUnavailable,
			Title:  "Upload mode not supported by this helper",
			Detail: "the server selected presigned parts; upload directly with the returned URLs",
		}
	}
	return &Upload{
		client: c,
		result: result,
		offset: result.UploadedBytes,
		size:   req.SizeBytes,
	}, nil
}

// ID returns the upload identifier.
func (u *Upload) ID() string { return u.result.UploadID }

// ObjectID returns the logical object identifier.
func (u *Upload) ObjectID() string { return u.result.ObjectID }

// UploadedBytes returns the provider-confirmed offset.
//
// It is the value to resume from after an interruption. Never assume more bytes
// than this: the provider only confirms what it durably accepted.
func (u *Upload) UploadedBytes() int64 { return u.offset }

// Write sends the next chunk.
//
// The caller decides the chunk boundaries; the server accepts the server-provided
// chunk size. On error the returned offset is authoritative, and the caller should
// resume from it rather than resending from the start.
func (u *Upload) Write(ctx context.Context, chunk io.Reader, length int64) (int64, error) {
	chunkSize := int64(u.result.ChunkSize)
	if chunkSize <= 0 {
		chunkSize = 8 << 20
	}
	if length > chunkSize {
		length = chunkSize
	}

	path := "/v1/storage/uploads/" + u.result.UploadID + "/chunks"
	var view UploadView
	err := u.client.doRaw(ctx, http.MethodPatch, path, chunk, &view, contentTypeBinary)
	if err != nil {
		return u.offset, err
	}
	if view.UploadedBytes > 0 {
		u.offset = view.UploadedBytes
	}
	return u.offset, nil
}

// Complete finalizes the upload and returns the stored object.
//
// It is idempotent: repeating it returns the existing verified object or its
// current pending state, and never creates a second location.
func (u *Upload) Complete(ctx context.Context) (*Object, error) {
	var object Object
	path := "/v1/storage/uploads/" + u.result.UploadID + "/complete"
	if err := u.client.do(ctx, http.MethodPost, path, nil, &object); err != nil {
		return nil, err
	}
	return &object, nil
}

// Status returns the current upload state.
func (u *Upload) Status(ctx context.Context) (*UploadView, error) {
	return u.client.GetUpload(ctx, u.result.UploadID)
}
