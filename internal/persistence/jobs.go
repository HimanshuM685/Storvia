package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNoJob reports that no job could be claimed.
var ErrNoJob = errors.New("persistence: no claimable job")

// Job kinds. These are constrained by the table's CHECK, so a typo fails at insert
// rather than producing a job nothing ever claims.
const (
	JobReconcile = "reconcile"
	JobDelete    = "delete"
	JobCleanup   = "cleanup"
)

// Job states, also constrained by the table.
const (
	JobPending = "pending"
	JobLeased  = "leased"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// Job is one unit of deferred work.
type Job struct {
	ID        string
	ObjectID  *string
	AttemptID *string
	TenantID  string
	Kind      string
	State     string
	RunAfter  time.Time
	Owner     string
	LeaseTill *time.Time
	Attempts  int
	LastError string
}

// Expired reports whether the lease has lapsed and the job may be retaken.
func (j Job) Expired(now time.Time) bool {
	return j.LeaseTill == nil || j.LeaseTill.Before(now)
}

// JobRepository claims and completes deferred work.
//
// Claiming is a lease, not a delete: a crashed worker's jobs become claimable again
// when the lease expires rather than being lost. That is why lease_expires_at is
// written rather than the row being deleted on claim.
type JobRepository struct {
	store *Store
}

// NewJobRepository builds the repository.
func NewJobRepository(store *Store) *JobRepository { return &JobRepository{store: store} }

// Enqueue schedules work.
//
// It is idempotent per (attempt, kind) because the table has a unique index on that
// pair, so a retrying request cannot produce two jobs that both delete the same object.
func (r *JobRepository) Enqueue(ctx context.Context, job Job) (string, error) {
	kind := strings.TrimSpace(job.Kind)
	if kind == "" {
		return "", errors.New("persistence: a job needs a kind")
	}
	runAfter := job.RunAfter
	if runAfter.IsZero() {
		runAfter = time.Now().UTC()
	}

	id := job.ID
	if id == "" {
		id = newJobID()
	}

	_, err := r.store.db.ExecContext(ctx, r.store.Rebind(`
		INSERT INTO storage_jobs
			(id, object_id, attempt_id, tenant_id, kind, state, run_after,
			 attempts, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT (attempt_id, kind) WHERE attempt_id IS NOT NULL DO NOTHING`),
		id, job.ObjectID, job.AttemptID, job.TenantID, kind, JobPending, runAfter,
		time.Now().UTC(), time.Now().UTC())
	if err != nil {
		return "", fmt.Errorf("persistence: enqueue %s job: %w", kind, err)
	}
	return id, nil
}

// Claim leases the next runnable job for a worker.
//
// Two conditions make a job runnable: it is due, and it is either unleased or its lease
// has lapsed. The second is what recovers work from a worker that died mid-job.
//
// The update is conditional on the row still being claimable, so two workers racing for
// the same row cannot both win: the loser's UPDATE matches zero rows and it tries the
// next one.
func (r *JobRepository) Claim(ctx context.Context, owner string, lease time.Duration) (Job, error) {
	if strings.TrimSpace(owner) == "" {
		return Job{}, errors.New("persistence: claiming needs a worker owner")
	}
	now := time.Now().UTC()
	expires := now.Add(lease)

	rows, err := r.store.db.QueryContext(ctx, `
		SELECT id FROM storage_jobs
		WHERE state IN ('pending', 'leased')
		  AND run_after <= ?
		  AND (lease_expires_at IS NULL OR lease_expires_at <= ?)
		ORDER BY run_after, created_at
		LIMIT 5`, now, now)
	if err != nil {
		return Job{}, fmt.Errorf("persistence: list claimable jobs: %w", err)
	}
	defer rows.Close()

	var candidates []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return Job{}, fmt.Errorf("persistence: scan job id: %w", err)
		}
		candidates = append(candidates, id)
	}
	if err := rows.Err(); err != nil {
		return Job{}, fmt.Errorf("persistence: list claimable jobs: %w", err)
	}
	if len(candidates) == 0 {
		return Job{}, ErrNoJob
	}

	for _, id := range candidates {
		res, err := r.store.db.ExecContext(ctx, r.store.Rebind(`
			UPDATE storage_jobs
			SET state = ?, lease_owner = ?, lease_expires_at = ?,
			    attempts = attempts + 1, updated_at = ?
			WHERE id = ?
			  AND state IN ('pending', 'leased')
			  AND run_after <= ?
			  AND (lease_expires_at IS NULL OR lease_expires_at <= ?)`),
			JobLeased, owner, expires, now, id, now, now)
		if err != nil {
			return Job{}, fmt.Errorf("persistence: claim job: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return Job{}, fmt.Errorf("persistence: claim job: %w", err)
		}
		if affected == 0 {
			// Another worker won the race for this row.
			continue
		}
		return r.Get(ctx, id)
	}
	return Job{}, ErrNoJob
}

// Renew extends a lease so a long job is not taken from under its worker.
//
// Without this, a reconcile that lists a large prefix would lose its lease partway
// through and another worker would start the same job.
func (r *JobRepository) Renew(ctx context.Context, jobID, owner string, lease time.Duration) error {
	res, err := r.store.db.ExecContext(ctx, r.store.Rebind(`
		UPDATE storage_jobs
		SET lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND lease_owner = ? AND state IN ('leased', 'running')`),
		time.Now().UTC().Add(lease), time.Now().UTC(), jobID, owner)
	if err != nil {
		return fmt.Errorf("persistence: renew job lease: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("persistence: renew job lease: %w", err)
	}
	if affected == 0 {
		return ErrJobLeaseLost
	}
	return nil
}

// ErrJobLeaseLost reports that a lease expired and the job was taken by another worker.
//
// A worker holding this must stop. Continuing would mean two workers acting on one
// object, which is the exact double-delete this lease exists to prevent.
var ErrJobLeaseLost = errors.New("persistence: the job lease was lost")

// Finish marks a job done.
func (r *JobRepository) Finish(ctx context.Context, jobID, owner string) error {
	return r.setTerminal(ctx, jobID, owner, JobDone, "")
}

// Fail records a failed attempt and schedules a retry.
//
// After MaxAttempts the job is parked as failed rather than retried forever, because a
// job that always fails is a problem for a person to look at, not something to spin on.
func (r *JobRepository) Fail(ctx context.Context, jobID, owner, reason string, retryIn time.Duration, maxAttempts int) error {
	job, err := r.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Attempts >= maxAttempts {
		return r.setTerminal(ctx, jobID, owner, JobFailed, truncate(reason, 500))
	}
	if retryIn <= 0 {
		retryIn = time.Minute
	}

	_, err = r.store.db.ExecContext(ctx, r.store.Rebind(`
		UPDATE storage_jobs
		SET state = ?, lease_owner = NULL, lease_expires_at = NULL,
		    run_after = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND lease_owner = ?`),
		JobPending, time.Now().UTC().Add(retryIn), truncate(reason, 500),
		time.Now().UTC(), jobID, owner)
	if err != nil {
		return fmt.Errorf("persistence: reschedule job: %w", err)
	}
	return nil
}

func (r *JobRepository) setTerminal(ctx context.Context, jobID, owner, state, reason string) error {
	res, err := r.store.db.ExecContext(ctx, r.store.Rebind(`
		UPDATE storage_jobs
		SET state = ?, lease_owner = NULL, lease_expires_at = NULL,
		    last_error = ?, updated_at = ?
		WHERE id = ? AND lease_owner = ?`),
		state, reason, time.Now().UTC(), jobID, owner)
	if err != nil {
		return fmt.Errorf("persistence: finish job: %w", err)
	}
	affected, err := res.RowsAffected()
	if affected == 0 {
		return ErrJobLeaseLost
	}
	return nil
}

// Get reads one job.
func (r *JobRepository) Get(ctx context.Context, id string) (Job, error) {
	row := r.store.db.QueryRowContext(ctx, r.store.Rebind(`
		SELECT id, object_id, attempt_id, tenant_id, kind, state, run_after,
		       lease_owner, lease_expires_at, attempts, last_error
		FROM storage_jobs WHERE id = ?`), id)

	var (
		job      Job
		objectID sql.NullString
		attempt  sql.NullString
		owner    sql.NullString
		lease    sql.NullTime
		lastErr  sql.NullString
	)
	err := row.Scan(&job.ID, &objectID, &attempt, &job.TenantID, &job.Kind, &job.State,
		&job.RunAfter, &owner, &lease, &job.Attempts, &lastErr)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNoJob
	}
	if err != nil {
		return Job{}, fmt.Errorf("persistence: get job: %w", err)
	}
	job.ObjectID = nullableString(objectID)
	job.AttemptID = nullableString(attempt)
	job.Owner = owner.String
	job.LastError = lastErr.String
	if lease.Valid {
		t := lease.Time
		job.LeaseTill = &t
	}
	return job, nil
}

// Pending counts runnable jobs, which is what an operator wants to see when the queue
// is not draining.
func (r *JobRepository) Pending(ctx context.Context) (int, error) {
	var count int
	err := r.store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM storage_jobs WHERE state IN ('pending','leased')`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("persistence: count pending jobs: %w", err)
	}
	return count, nil
}

// newJobID mints an identifier. Jobs use a plain prefix rather than the idid generator
// so a job id is readable in a log line without a lookup.
func newJobID() string {
	return "job_" + fmt.Sprintf("%d", time.Now().UnixNano())
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
