// Package apperr defines the single error vocabulary of the Storvia API.
//
// Every error that reaches an HTTP boundary is an *Error. Errors are matched by
// kind with errors.Is, mapped to an RFC 9457 "application/problem+json" body by
// the global handler in internal/httpapi, and never leak a provider payload, a
// credential, or a token to the client.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind is the coarse, stable classification of a failure. It drives the HTTP
// status code and the machine-readable problem type, so it must stay small and
// must not be reused for two different meanings.
type Kind string

const (
	// KindValidation is a client mistake: bad input, bad size, bad MIME type,
	// failed tenant authorization. Never retried, never routed to a fallback.
	KindValidation Kind = "validation"

	// KindAuthentication is a missing or unverifiable application session.
	KindAuthentication Kind = "authentication"

	// KindAuthorization is a verified session attempting something the tenant is
	// not allowed to do, including reading another tenant's object.
	KindAuthorization Kind = "authorization"

	// KindNotFound is a missing object, upload, or connection. Provider 404s are
	// normalized into this kind by provider adapters.
	KindNotFound Kind = "not_found"

	// KindConflict is an idempotency conflict or an illegal state transition,
	// such as reusing an idempotency key with different metadata.
	KindConflict Kind = "conflict"

	// KindQuota is a user or provider quota limit: Drive full, storage
	// quota exceeded, Drive daily upload limit.
	KindQuota Kind = "quota"

	// KindRateLimited is provider or platform throttling. The caller should back
	// off; Storvia does not fall back to a different storage provider.
	KindRateLimited Kind = "rate_limited"

	// KindUnavailable is service or transport unavailability. This is the only
	// kind that may make a *new upload* eligible for the S3-compatible fallback,
	// and only before any provider mutation may have started.
	KindUnavailable Kind = "unavailable"

	// KindUncertain is an ambiguous provider write: the bytes may or may not
	// have been accepted. It maps to outcome_unknown and is never eligible for
	// cross-provider failover.
	//
	// It is a 409 rather than a 2xx: an RFC 9457 problem document is an error
	// response, and reporting an unknown outcome under a success status would let a
	// client mistake it for a completed upload.
	KindUncertain Kind = "uncertain"

	// KindProviderNotConfigured means the required backend is disabled,
	// credentialed incorrectly, or absent. Configuration failures never trigger
	// a fallback to a different provider.
	KindProviderNotConfigured Kind = "provider_not_configured"

	// KindConsentRequired means Drive is not connected, consent was declined,
	// the grant was revoked, or a Workspace policy blocks access.
	KindConsentRequired Kind = "consent_required"

	// KindInternal is an unexpected server fault. The cause is logged, never
	// returned.
	KindInternal Kind = "internal"
)

// String implements fmt.Stringer.
func (k Kind) String() string { return string(k) }

// httpStatus maps each kind to its HTTP status. Keeping this table in one place
// is what stops the handler layer from inventing statuses per route.
var httpStatus = map[Kind]int{
	KindValidation:            http.StatusBadRequest,
	KindAuthentication:        http.StatusUnauthorized,
	KindAuthorization:         http.StatusForbidden,
	KindNotFound:              http.StatusNotFound,
	KindConflict:              http.StatusConflict,
	KindQuota:                 http.StatusInsufficientStorage,
	KindRateLimited:           http.StatusTooManyRequests,
	KindUnavailable:           http.StatusServiceUnavailable,
	KindUncertain:             http.StatusConflict,
	KindProviderNotConfigured: http.StatusFailedDependency,
	KindConsentRequired:       http.StatusPreconditionRequired,
	KindInternal:              http.StatusInternalServerError,
}

// problemTypeBase is the prefix for the problem type URNs. The final segment is
// the kind, so clients can branch on the type without parsing the title.
const problemTypeBase = "https://storvia.dev/problems/"

// Error is the only error type the service returns from its use cases.
type Error struct {
	Kind Kind
	// Title is a short, stable, human-readable summary safe to show a client.
	Title string
	// Detail explains this specific occurrence. It must never contain a
	// credential, token, signed URL, session URI, or raw provider payload.
	Detail string
	// Cause is the wrapped internal error. It is logged and never serialized.
	Cause error
	// Fields carries optional structured context, for example the field name
	// that failed validation. Values here are operator-authored, never raw
	// user input or provider data.
	Fields map[string]string
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Detail, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
}

func (e *Error) Unwrap() error { return e.Cause }

// Status returns the HTTP status for this error's kind.
func (e *Error) Status() int {
	if s, ok := httpStatus[e.Kind]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// ProblemType returns the RFC 9457 type URN for this error.
func (e *Error) ProblemType() string { return problemTypeBase + string(e.Kind) }

// New builds an Error without a cause.
func New(kind Kind, title, detail string) *Error {
	return &Error{Kind: kind, Title: title, Detail: detail}
}

// Wrap builds an Error that wraps cause. The cause is used for logs and
// errors.Is chains only; it is never sent to the client.
func Wrap(cause error, kind Kind, title, detail string) *Error {
	return &Error{Kind: kind, Title: title, Detail: detail, Cause: cause}
}

// WithField returns a copy of e with one extra structured field.
func (e *Error) WithField(key, value string) *Error {
	clone := *e
	clone.Fields = make(map[string]string, len(e.Fields)+1)
	for k, v := range e.Fields {
		clone.Fields[k] = v
	}
	clone.Fields[key] = value
	return &clone
}

// KindOf reports the kind of err, following the wrap chain. It returns
// KindInternal for nil and for any error that is not an *Error, which is what
// makes a bare fmt.Errorf from a deep package safe rather than a leak.
func KindOf(err error) Kind {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	if err == nil {
		return KindInternal
	}
	return KindInternal
}

// Is reports whether err carries kind k.
func Is(err error, k Kind) bool { return KindOf(err) == k }

// Constructors for the kinds used across more than one package. Each pairs a
// title that is safe to show a user with a detail template the caller fills in.

func Validation(detail string) *Error {
	return New(KindValidation, "Request validation failed", detail)
}

func Authentication(detail string) *Error {
	return New(KindAuthentication, "Authentication required", detail)
}

func Authorization(detail string) *Error {
	return New(KindAuthorization, "Not permitted for this tenant", detail)
}

func NotFound(detail string) *Error {
	return New(KindNotFound, "Resource not found", detail)
}

func Conflict(detail string) *Error {
	return New(KindConflict, "Conflicting request", detail)
}

func Quota(detail string) *Error {
	return New(KindQuota, "Storage quota exhausted", detail)
}

func RateLimited(detail string) *Error {
	return New(KindRateLimited, "Rate limited", detail)
}

func Unavailable(detail string) *Error {
	return New(KindUnavailable, "Storage provider unavailable", detail)
}

func Uncertain(detail string) *Error {
	return New(KindUncertain, "Upload outcome is not yet known", detail)
}

func ConsentRequired(detail string) *Error {
	return New(KindConsentRequired, "Google Drive connection required", detail)
}

func ProviderNotConfigured(detail string) *Error {
	return New(KindProviderNotConfigured, "Storage backend is not usable", detail)
}

func Internal(detail string) *Error {
	return New(KindInternal, "Internal error", detail)
}
