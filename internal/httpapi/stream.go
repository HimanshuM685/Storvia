package httpapi

import (
	"io"
	"strconv"
)

// maxStreamBytes bounds a single streamed response body.
//
// The limit is a backstop against an unbounded or looping provider response. It
// sits well above any legitimate object size, so it only ever trips on a fault.
const maxStreamBytes = 8 << 30 // 8 GiB

// copyBounded streams src into dst, stopping at limit bytes.
//
// The limit is applied on the read side so an oversized provider response cannot
// exhaust the process, and the returned count lets the caller log a mismatch.
func copyBounded(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit < 0 {
		limit = maxStreamBytes
	}
	// CopyN with a bound larger than the body simply stops at EOF, so a declared
	// size that overstates the real one is harmless.
	written, err := io.Copy(dst, io.LimitReader(src, limit))
	if err != nil {
		return written, err
	}
	return written, nil
}

// formatInt renders an int64 without pulling in fmt at call sites.
func formatInt(value int64) string { return strconv.FormatInt(value, 10) }
