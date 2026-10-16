// Package model holds the provider-neutral domain types for Storvia storage.
//
// Nothing in this package mentions Drive or S3. A logical object is a Storvia
// concept; where its bytes live is a provider pointer attached to that object,
// and the invariant enforced across the system is that a ready object has
// exactly one such pointer.
package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Provider identifies where an object's bytes live.
type Provider string

const (
	// ProviderUnset means no provider has been chosen yet. Only a pending object
	// may have this value.
	ProviderUnset Provider = ""
	// ProviderGoogleDrive is the user's connected Drive.
	ProviderGoogleDrive Provider = "google_drive"
	// ProviderS3Compatible is the configured S3-compatible outage fallback.
	ProviderS3Compatible Provider = "s3_compatible"
)

// String implements fmt.Stringer.
func (p Provider) String() string { return string(p) }

// Valid reports whether p is a known provider, including ProviderUnset.
func (p Provider) Valid() bool {
	switch p {
	case ProviderUnset, ProviderGoogleDrive, ProviderS3Compatible:
		return true
	}
	return false
}

// ObjectStatus is the lifecycle state of a logical object.
type ObjectStatus string

const (
	ObjectPending        ObjectStatus = "pending"
	ObjectUploading      ObjectStatus = "uploading"
	ObjectOutcomeUnknown ObjectStatus = "outcome_unknown"
	ObjectReady          ObjectStatus = "ready"
	ObjectBlocked        ObjectStatus = "blocked"
	ObjectFailed         ObjectStatus = "failed"
	ObjectDeleting       ObjectStatus = "deleting"
	ObjectDeleted        ObjectStatus = "deleted"
	ObjectMissing        ObjectStatus = "missing"
)

// AttemptState is the lifecycle state of one provider upload attempt.
type AttemptState string

const (
	AttemptSelected       AttemptState = "selected"
	AttemptInitiating     AttemptState = "initiating"
	AttemptActive         AttemptState = "active"
	AttemptVerifying      AttemptState = "verifying"
	AttemptOutcomeUnknown AttemptState = "outcome_unknown"
	AttemptCompleted      AttemptState = "completed"
	AttemptCleanupPending AttemptState = "cleanup_pending"
	AttemptAborted        AttemptState = "aborted"
	AttemptFailed         AttemptState = "failed"
)

// FallbackReason records why the outage fallback was selected, for the routing
// evidence row and the fallback-count metric. An empty value means Drive was
// selected.
type FallbackReason string

const (
	FallbackNone                 FallbackReason = ""
	FallbackPreflightTimeout     FallbackReason = "drive_preflight_timeout"
	FallbackPreflightTransport   FallbackReason = "drive_preflight_transport_error"
	FallbackPreflightServerError FallbackReason = "drive_preflight_server_error"
	FallbackCircuitOpen          FallbackReason = "drive_circuit_open"
)

// LogicalObject is the tenant-scoped record of one file.
//
// Exactly one of DriveConnectionID/ProviderFileID or BackendID/ProviderObjectKey
// is populated once the object is ready. That is the single-location invariant;
// there is deliberately no replica table to hold a second copy.
type LogicalObject struct {
	ID           string
	TenantID     string
	UserID       string
	LogicalKey   string
	OriginalName string
	MIMEType     string
	Category     string

	ExpectedSize      int64
	VerifiedSize      int64
	ChecksumAlgorithm string
	VerifiedChecksum  string

	Provider          Provider
	DriveConnectionID *string
	BackendID         *string
	ProviderFileID    *string
	ProviderObjectKey *string

	Status         ObjectStatus
	FallbackReason FallbackReason
	SelectedAt     *time.Time
	VerifiedAt     *time.Time

	RowVersion int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

// Location returns the single provider location for a ready object and an error
// if the object has none or an ambiguous one. Callers must use this rather than
// reading the pointer fields directly, so that an inconsistent row fails loudly.
func (o *LogicalObject) Location() (Provider, error) {
	if o.Status != ObjectReady {
		return ProviderUnset, fmt.Errorf("object %s is %s, not ready", o.ID, o.Status)
	}
	drive := o.DriveConnectionID != nil && o.ProviderFileID != nil
	s3 := o.BackendID != nil && o.ProviderObjectKey != nil
	switch {
	case drive && s3:
		return ProviderUnset, fmt.Errorf("object %s has both a Drive and an S3 location", o.ID)
	case drive:
		return ProviderGoogleDrive, nil
	case s3:
		return ProviderS3Compatible, nil
	default:
		return ProviderUnset, fmt.Errorf("object %s is ready but has no provider location", o.ID)
	}
}

// ReachedProvider reports whether any provider reference was ever recorded.
//
// It exists because status alone cannot answer this. An object that never left
// Storvia has no provider reference at all, while one whose upload was interrupted
// may be `uploading` and still hold a real Drive file id. Deleting the first needs no
// provider call; deleting the second must go to the provider or it leaves a file
// behind.
//
// It deliberately reads the reference fields rather than calling Location, which
// refuses anything that is not ready.
func (o *LogicalObject) ReachedProvider() bool {
	return o.ProviderFileID != nil || o.ProviderObjectKey != nil
}

// Upload is the durable, idempotent record of one logical upload request. It
// outlives any single attempt.
type Upload struct {
	ID                 string
	ObjectID           string
	TenantID           string
	UserID             string
	IdempotencyKey     string
	RequestFingerprint string
	ActiveAttemptID    *string
	State              ObjectStatus
	RowVersion         int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// UploadAttempt is one attempt to place an object's bytes on one provider.
type UploadAttempt struct {
	ID       string
	UploadID string
	// Provider is set at selection time and never changes for this attempt.
	Provider Provider
	// DriveConnectionID or BackendID identifies the exact provider account.
	DriveConnectionID *string
	BackendID         *string
	// PlannedProviderIdentity is the file ID or object key Storvia intends to
	// create. The server derives it; a client can never supply it.
	PlannedProviderIdentity *string
	// EncryptedSessionURI is the Drive resumable session URI, encrypted at rest.
	// It never leaves the server.
	EncryptedSessionURI *string
	MultipartUploadID   *string

	State AttemptState
	// MutationMayHaveStarted is the switch that decides whether a cross-provider
	// write is still permissible. Once true it is never reset.
	MutationMayHaveStarted bool
	ConfirmedOffset        int64
	ExpectedSize           int64
	PartsMetadata          []byte
	LastSanitizedError     *string
	// OperationGeneration increments on every new attempt for the same upload so
	// a stale worker cannot act on a newer attempt's identity.
	OperationGeneration int

	LeaseOwner     *string
	LeaseExpiresAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UploadState is what a provider reports about an in-flight upload when the
// result of a write is unknown.
type UploadState struct {
	// Exists reports whether the provider still has a resumable session or
	// multipart upload.
	Exists bool
	// ConfirmedOffset is the number of bytes the provider has durably accepted.
	ConfirmedOffset int64
	// Completed reports whether the provider finished the object.
	Completed bool
	// Expired reports that the session can no longer be resumed.
	Expired bool
}

// ByteRange is an inclusive-start, exclusive-end byte range. A zero value means
// "the whole object".
type ByteRange struct {
	Start int64
	End   int64
	// HasRange distinguishes an explicit 0-to-end request from "no range".
	HasRange bool
}

// FullRange returns an unbounded range.
func FullRange() *ByteRange { return nil }

// String renders the range as an HTTP Range header value.
func (b *ByteRange) String() string {
	if b == nil || !b.HasRange {
		return ""
	}
	if b.End <= b.Start {
		return fmt.Sprintf("bytes=%d-", b.Start)
	}
	return fmt.Sprintf("bytes=%d-%d", b.Start, b.End-1)
}

// ParseByteRange parses a single-range HTTP Range header value. Multi-range
// requests are rejected: Storvia serves one contiguous range per request.
func ParseByteRange(header string) (*ByteRange, error) {
	value := strings.TrimSpace(header)
	if value == "" {
		return nil, nil
	}
	const prefix = "bytes="
	if !strings.HasPrefix(value, prefix) {
		return nil, fmt.Errorf("unsupported range unit")
	}
	spec := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if strings.Contains(spec, ",") {
		return nil, errors.New("multiple ranges are not supported")
	}
	startText, endText, found := strings.Cut(spec, "-")
	if !found {
		return nil, errors.New("malformed range")
	}
	if startText == "" {
		// A suffix range such as bytes=-500 depends on the total size, which is
		// the object row's job, not the transport's.
		return nil, errors.New("suffix ranges are not supported")
	}
	var start int64
	if _, err := fmt.Sscanf(startText, "%d", &start); err != nil || start < 0 {
		return nil, errors.New("malformed range start")
	}
	parsed := &ByteRange{Start: start, HasRange: true}
	if endText != "" {
		var end int64
		if _, err := fmt.Sscanf(endText, "%d", &end); err != nil || end < 0 {
			return nil, errors.New("malformed range end")
		}
		parsed.End = end + 1 // HTTP end is inclusive; Go's slicing is not.
	}
	return parsed, nil
}

// Valid reports whether the range is internally consistent.
func (b *ByteRange) Valid(totalSize int64) bool {
	if b == nil || !b.HasRange {
		return true
	}
	if b.Start < 0 || b.Start >= totalSize {
		return false
	}
	if b.HasRange && b.End != 0 && b.End <= b.Start {
		return false
	}
	if b.End > totalSize {
		return false
	}
	return true
}

// Chunk is a slice of the file being sent to a provider.
type Chunk struct {
	Offset int64
	Data   []byte
}

// ObjectMetadata is the provider-neutral description Storvia sends when it
// creates an object. Name and MIME come from validated server-side values.
type ObjectMetadata struct {
	OriginalName string
	MIMEType     string
	SizeBytes    int64
	// ChecksumAlgorithm and Checksum carry the provider's verified integrity
	// value. They are evidence of a successful transfer, not ownership proof and
	// not a file copy.
	ChecksumAlgorithm string
	Checksum          string
	// Category is Storvia-internal metadata. It is never sent to a provider as
	// user content.
	Category string
	// InAppFolder reports that the provider confirmed the object lives in the
	// folder Storvia manages, rather than anywhere else in the account.
	//
	// It exists because a provider-reported object ID is untrusted input. Without
	// this check a client could point an upload at any other file it happens to
	// have, and Storvia would adopt it.
	InAppFolder bool
	// Trashed reports that the provider considers the object deleted. A trashed
	// file must never be committed as ready.
	Trashed bool
}

// SizeMatches reports whether a verified size equals the expected size.
func (m ObjectMetadata) SizeMatches(expected int64) bool {
	return m.SizeBytes == expected
}

// Session is an in-flight provider upload.
type Session struct {
	// URI is the provider's resume handle. For Drive this is the resumable
	// session URI, which must stay server-side and is never returned to a client.
	URI string
	// ExpiresAt is when the provider will no longer accept a resume.
	ExpiresAt *time.Time
	// PlannedIdentity is the file ID or object key the session will produce.
	PlannedIdentity string
	// MultipartUploadID is set for S3 multipart uploads.
	MultipartUploadID string
}

// ProviderObject is a resolved provider location.
type ProviderObject struct {
	Provider          Provider
	FileID            string
	ObjectKey         string
	Bucket            string
	MIMEType          string
	SizeBytes         int64
	ETag              string
	Checksum          string
	ChecksumAlgorithm string
	ModifiedAt        *time.Time
}

// AvailabilityScope narrows an availability check to one Drive connection and
// runtime context, so one user's outage never opens a global circuit.
type AvailabilityScope struct {
	TenantID          string
	UserID            string
	DriveConnectionID string
	// EgressContext is the runtime environment identifier, for example a
	// deployment region. The circuit is scoped to it as well as the connection.
	EgressContext string
}

// Key returns the cache and circuit key for this scope.
func (s AvailabilityScope) Key() string {
	return strings.Join([]string{"drive", s.EgressContext, s.TenantID, s.UserID, s.DriveConnectionID}, ":")
}

// Progress is a provider's confirmation of bytes accepted so far.
type Progress struct {
	ConfirmedOffset int64
	Completed       bool
	// ETag or FileID present on completion.
	ETag   string
	FileID string
}

// UploadMode is the transfer mechanism advertised to a client. The modes are
// deliberately distinct: a Drive resumable session is not an S3 presigned URL, and
// forcing both into one shape would mislead the client.
//
// There is no proxy mode. It was removed because a resumable session makes it
// impossible: the server holds no copy of the bytes, so there is nothing to forward.
// An endpoint that could only ever answer "not implemented" is worse than no
// endpoint, because it advertises a capability that does not exist.
type UploadMode string

const (
	// ModeS3PresignedParts hands the client short-lived, operation-scoped URLs.
	ModeS3PresignedParts UploadMode = "s3_presigned_parts"

	// ModeDriveResumableSession hands the client a Drive resumable session URI to
	// PUT chunks to. It is Drive's equivalent of an S3 presigned URL: the provider
	// mints the capability and Storvia only relays it.
	ModeDriveResumableSession UploadMode = "drive_resumable_session"
)
