package drive

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

// errorsAs is a thin wrapper so the tests read consistently.
func errorsAs(err error, target any) bool { return errors.As(err, target) }

// responseWithBody builds a minimal HTTP response for the error classifier.
func responseWithBody(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}
}
