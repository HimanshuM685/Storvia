package service

import (
	"context"
	"io"
	"log/slog"

	"github.com/shovan04/storvia/internal/apperr"
	"github.com/shovan04/storvia/internal/persistence"
	"github.com/shovan04/storvia/internal/storage/model"
	"github.com/shovan04/storvia/internal/storage/policy"
)

// ContentSource resolves a recorded provider location to a readable stream.
type ContentSource interface {
	// Open streams a recorded object from its own provider. It never falls back
	// to a second provider: Storvia holds no second copy to fall back to.
	Open(ctx context.Context, object model.LogicalObject, byteRange *model.ByteRange) (io.ReadCloser, error)
	// Delete removes an object from its recorded provider only.
	Delete(ctx context.Context, object model.LogicalObject) error
}

// ObjectService implements the object read and delete use cases.
type ObjectService struct {
	objects *persistence.ObjectRepository
	uploads *persistence.UploadRepository
	source  ContentSource
	// jobs schedules deferred work for an operation that did not complete. It is nil
	// when the deployment runs no worker, and then a failed delete stays failed
	// rather than being retried.
	jobs   *persistence.JobRepository
	logger *slog.Logger
}

// NewObjectService builds the service.
func NewObjectService(objects *persistence.ObjectRepository, uploads *persistence.UploadRepository, source ContentSource) *ObjectService {
	return &ObjectService{
		objects: objects,
		uploads: uploads,
		source:  source,
		logger:  slog.Default(),
	}
}

// WithJobs attaches the retry queue.
func (s *ObjectService) WithJobs(jobs *persistence.JobRepository, logger *slog.Logger) *ObjectService {
	s.jobs = jobs
	if logger != nil {
		s.logger = logger
	}
	return s
}

// GetObject returns one object, scoped to the tenant.
func (s *ObjectService) GetObject(ctx context.Context, scope persistence.Scope, objectID string) (model.LogicalObject, error) {
	object, err := s.objects.Get(ctx, scope, objectID)
	if err != nil {
		// A cross-tenant read is reported as not found rather than forbidden. A
		// forbidden response would confirm the ID exists, turning the endpoint
		// into an existence oracle.
		return model.LogicalObject{}, apperr.NotFound("no such object for this account")
	}
	return object, nil
}

// ContentStream is a resolved download.
type ContentStream struct {
	Body      io.ReadCloser
	Name      string
	MIMEType  string
	SizeBytes int64
	ETag      string
}

// OpenContent streams an object from its recorded location.
func (s *ObjectService) OpenContent(ctx context.Context, scope persistence.Scope, objectID string, byteRange *model.ByteRange) (ContentStream, error) {
	object, err := s.GetObject(ctx, scope, objectID)
	if err != nil {
		return ContentStream{}, err
	}

	provider, err := object.Location()
	if err != nil {
		// A ready row with no location, or with two, is a data integrity fault.
		// It is reported as an internal error rather than being papered over.
		return ContentStream{}, apperr.Wrap(err, apperr.KindInternal, "Internal error",
			"the object's storage location is inconsistent")
	}

	if object.Status == model.ObjectMissing {
		return ContentStream{}, apperr.NotFound("the stored file is no longer present at its provider")
	}
	if object.Status != model.ObjectReady {
		return ContentStream{}, apperr.New(apperr.KindUncertain, "File is not ready",
			"the upload has not finished; retry once it reaches the ready state")
	}
	if byteRange != nil && byteRange.HasRange && !byteRange.Valid(object.VerifiedSize) {
		return ContentStream{}, apperr.Validation("the requested range is outside the file")
	}

	body, sourceErr := s.source.Open(ctx, object, byteRange)
	if sourceErr != nil {
		return ContentStream{}, providerErrorToAppError(provider, "read", sourceErr)
	}

	return ContentStream{
		Body:      body,
		Name:      object.OriginalName,
		MIMEType:  object.MIMEType,
		SizeBytes: object.VerifiedSize,
		ETag:      object.VerifiedChecksum,
	}, nil
}

// DeleteObject removes an object from its recorded provider location.
//
// The deletion is pinned to the location the object was written to. If Drive has
// recovered since an S3 fallback upload, the file still goes to S3: there is no
// copy on Drive to delete, and deleting a same-named object on another provider
// would destroy unrelated user data.
func (s *ObjectService) DeleteObject(ctx context.Context, scope persistence.Scope, objectID string) error {
	object, err := s.GetObject(ctx, scope, objectID)
	if err != nil {
		return err
	}
	if object.Status == model.ObjectDeleted {
		// Already gone: deletion is idempotent, and a second call is not an error.
		return nil
	}
	if object.Status == model.ObjectDeleting {
		return apperr.Conflict("a deletion for this object is already in progress")
	}

	provider, err := object.Location()
	if err != nil && object.Status == model.ObjectReady {
		return apperr.Wrap(err, apperr.KindInternal, "Internal error", "the object's storage location is inconsistent")
	}

	if err := s.objects.UpdateStatus(ctx, scope, object.ID, object.RowVersion, model.ObjectDeleting); err != nil {
		if persistence.IsConflict(err) {
			return apperr.Conflict("the object changed while the deletion was being prepared; retry")
		}
		return apperr.Wrap(err, apperr.KindInternal, "Internal error", "the deletion could not be started")
	}

	if err := s.source.Delete(ctx, object); err != nil {
		var providerErr *policy.ProviderError
		if errorsAs(err, &providerErr) && providerErr.Class == policy.ClassMissing {
			// The provider says the object is already absent. That is a documented
			// already-deleted outcome, not a failure, and it is not grounds to
			// delete anything elsewhere.
			return s.finishDelete(ctx, scope, object, model.ObjectMissing)
		}
		// The delete did not happen. Schedule it rather than leaving the object
		// stuck in `deleting`, which is what a caller retrying would then see as a
		// permanent conflict.
		//
		// The object stays in `deleting` on purpose: the job is pinned to this
		// object and this provider, so retrying cannot move the deletion somewhere
		// else.
		s.scheduleRetry(scope, object, persistence.JobDelete)
		return providerErrorToAppError(provider, "delete", err)
	}
	return s.finishDelete(ctx, scope, object, model.ObjectDeleted)
}

// scheduleRetry queues deferred work for an operation that did not complete.
//
// A failure here is logged rather than returned: the caller already has an error, and
// replacing it with "could not schedule" would hide the real reason the delete failed.
func (s *ObjectService) scheduleRetry(scope persistence.Scope, object model.LogicalObject, kind string) {
	if s.jobs == nil {
		return
	}
	objectID := object.ID
	if _, err := s.jobs.Enqueue(context.Background(), persistence.Job{
		ObjectID: &objectID,
		TenantID: scope.TenantID,
		Kind:     kind,
	}); err != nil {
		s.logger.Error("could not schedule a retry job",
			"object_id", object.ID, "kind", kind, "error", err.Error())
	}
}

// finishDelete records the terminal deletion state.
func (s *ObjectService) finishDelete(ctx context.Context, scope persistence.Scope, object model.LogicalObject, status model.ObjectStatus) error {
	// Re-read to get the row version the deleting update produced, so the
	// terminal update is not fighting its own optimistic lock.
	current, err := s.objects.Get(ctx, scope, object.ID)
	if err != nil {
		return apperr.NotFound("no such object for this account")
	}
	if err := s.objects.UpdateStatus(ctx, scope, current.ID, current.RowVersion, status); err != nil {
		if persistence.IsConflict(err) {
			return apperr.Conflict("the object changed while the deletion was being recorded; retry")
		}
		return apperr.Wrap(err, apperr.KindInternal, "Internal error", "the deletion could not be recorded")
	}
	return nil
}

// providerErrorToAppError maps a normalized provider error onto the shared error
// vocabulary, so the client sees a stable problem type regardless of provider.
func providerErrorToAppError(provider model.Provider, operation string, err error) error {
	var providerErr *policy.ProviderError
	if !errorsAs(err, &providerErr) {
		return apperr.Wrap(err, apperr.KindInternal, "Internal error", "the storage provider could not complete the request")
	}

	detail := operation + " failed: " + providerErr.SafeDetail
	if detail == operation+" failed: " {
		detail = operation + " failed"
	}

	switch providerErr.Class {
	case policy.ClassQuota:
		return apperr.Quota(detail)
	case policy.ClassRateLimited:
		return apperr.RateLimited(detail)
	case policy.ClassMissing:
		return apperr.NotFound("the stored file is no longer present at its provider")
	case policy.ClassUnavailable:
		return apperr.Unavailable("the storage provider is currently unavailable; no second copy exists to serve from")
	case policy.ClassUncertain:
		return apperr.Uncertain("the provider result is not yet known; retry rather than re-uploading elsewhere")
	case policy.ClassConsentRequired, policy.ClassAuthentication:
		return apperr.ConsentRequired("the google drive connection needs to be re-established")
	case policy.ClassAuthorization:
		return apperr.Authorization("the connected account may not perform this operation")
	case policy.ClassConfiguration:
		return apperr.ProviderNotConfigured(detail)
	case policy.ClassValidation:
		return apperr.Validation(detail)
	case policy.ClassConflict:
		return apperr.Conflict(detail)
	default:
		return apperr.Wrap(err, apperr.KindInternal, "Internal error",
			"the storage provider returned an unclassified failure")
	}
}
