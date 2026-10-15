package persistence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func newJobRepo(t *testing.T) (*JobRepository, *Store) {
	t.Helper()
	store := newTestStore(t)
	return NewJobRepository(store), store
}

func TestJobEnqueueAndClaim(t *testing.T) {
	repo, _ := newJobRepo(t)
	ctx := context.Background()

	id, err := repo.Enqueue(ctx, Job{TenantID: "tenant-a", Kind: JobReconcile})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	job, err := repo.Claim(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if job.ID != id {
		t.Errorf("claimed %q, want %q", job.ID, id)
	}
	if job.State != JobLeased {
		t.Errorf("state = %q, want leased", job.State)
	}
	if job.Owner != "worker-1" {
		t.Errorf("owner = %q, want worker-1", job.Owner)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 after a claim", job.Attempts)
	}
}

// TestClaimIsExclusive is the property that stops two workers deleting the same object.
// If two claims could succeed, a retry could delete a file twice.
func TestClaimIsExclusive(t *testing.T) {
	repo, _ := newJobRepo(t)
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, Job{TenantID: "tenant-a", Kind: JobDelete}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := repo.Claim(ctx, "worker", time.Minute)
			results[n] = err
		}(i)
	}
	wg.Wait()

	claimed := 0
	for _, err := range results {
		switch {
		case err == nil:
			claimed++
		case errors.Is(err, ErrNoJob):
		default:
			t.Errorf("unexpected claim error: %v", err)
		}
	}
	if claimed != 1 {
		t.Errorf("%d workers claimed the single job, want exactly 1", claimed)
	}
}

// TestAnExpiredLeaseIsRecovered is why claiming is a lease and not a delete: a worker
// that dies mid-job must not strand the work.
func TestAnExpiredLeaseIsRecovered(t *testing.T) {
	repo, _ := newJobRepo(t)
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, Job{TenantID: "tenant-a", Kind: JobCleanup}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := repo.Claim(ctx, "worker-dies", time.Minute); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// A live lease is not claimable by anyone else.
	if _, err := repo.Claim(ctx, "worker-2", time.Minute); !errors.Is(err, ErrNoJob) {
		t.Errorf("a live lease was stealable: %v", err)
	}

	// Once it lapses the work is recoverable. The lease is moved into the past
	// directly rather than by sleeping, so the test is fast and deterministic.
	if _, err := repo.store.db.ExecContext(ctx,
		`UPDATE storage_jobs SET lease_expires_at = ?`,
		time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("expire the lease: %v", err)
	}

	job, err := repo.Claim(ctx, "worker-2", time.Minute)
	if err != nil {
		t.Fatalf("an expired lease must be recoverable: %v", err)
	}
	if job.Attempts < 2 {
		t.Errorf("attempts = %d, want the retry counted", job.Attempts)
	}
}

// TestFinishRequiresOwnership. A worker that lost its lease must not be able to mark
// the job done, because another worker may already be acting on it.
func TestFinishRequiresOwnership(t *testing.T) {
	repo, _ := newJobRepo(t)
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, Job{TenantID: "tenant-a", Kind: JobDelete}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, err := repo.Claim(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := repo.Finish(ctx, job.ID, "someone-else"); !errors.Is(err, ErrJobLeaseLost) {
		t.Errorf("a non-owner finishing returned %v, want ErrJobLeaseLost", err)
	}
	if err := repo.Finish(ctx, job.ID, "worker-1"); err != nil {
		t.Errorf("the owner must be able to finish: %v", err)
	}

	// And it is not claimable again.
	if _, err := repo.Claim(ctx, "worker-2", time.Minute); !errors.Is(err, ErrNoJob) {
		t.Errorf("a finished job was claimable: %v", err)
	}
}

func TestRenewExtendsOnlyForTheOwner(t *testing.T) {
	repo, _ := newJobRepo(t)
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, Job{TenantID: "tenant-a", Kind: JobReconcile}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, err := repo.Claim(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := repo.Renew(ctx, job.ID, "worker-2", time.Minute); !errors.Is(err, ErrJobLeaseLost) {
		t.Errorf("a non-owner renewing returned %v, want ErrJobLeaseLost", err)
	}
	if err := repo.Renew(ctx, job.ID, "worker-1", 2*time.Minute); err != nil {
		t.Errorf("the owner must be able to renew: %v", err)
	}

	renewed, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if renewed.LeaseTill == nil {
		t.Fatal("renew did not set an expiry")
	}
	if time.Until(*renewed.LeaseTill) < 90*time.Second {
		t.Errorf("lease expires at %v, want it extended", renewed.LeaseTill)
	}
}

// TestFailRetriesThenParks. A job that always fails is a problem for a person, not
// something to spin on forever.
func TestFailRetriesThenParks(t *testing.T) {
	repo, _ := newJobRepo(t)
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, Job{TenantID: "tenant-a", Kind: JobReconcile}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var jobID string
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			// A retry is scheduled into the future; move it back so the next
			// iteration can claim it without sleeping.
			if _, err := repo.store.db.ExecContext(ctx,
				`UPDATE storage_jobs SET run_after = ?`, time.Now().UTC().Add(-time.Minute)); err != nil {
				t.Fatalf("make retry due: %v", err)
			}
		}
		job, err := repo.Claim(ctx, "worker", time.Minute)
		if err != nil {
			t.Fatalf("claim %d: %v", attempt, err)
		}
		jobID = job.ID
		if err := repo.Fail(ctx, job.ID, "worker", "provider unavailable", time.Minute, 3); err != nil {
			t.Fatalf("Fail %d: %v", attempt, err)
		}
	}

	parked, err := repo.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if parked.State != JobFailed {
		t.Errorf("state = %q, want failed after the attempt budget", parked.State)
	}
	if parked.LastError == "" {
		t.Error("a failed job must record why")
	}
	if _, err := repo.Claim(ctx, "worker", time.Minute); !errors.Is(err, ErrNoJob) {
		t.Errorf("a parked job was claimable: %v", err)
	}
}

// TestEnqueueIsIdempotentPerAttempt guards against a retrying client producing two
// jobs that both delete the same object.
func TestEnqueueIsIdempotentPerAttempt(t *testing.T) {
	repo, _ := newJobRepo(t)
	ctx := context.Background()

	// The attempt row must exist: the foreign key is what stops a job referencing an
	// attempt that was rolled back.
	attemptID := "upl_attempt_1"
	seedAttempt(t, repo.store, attemptID)

	for i := 0; i < 3; i++ {
		if _, err := repo.Enqueue(ctx, Job{TenantID: "tenant-a", Kind: JobDelete, AttemptID: &attemptID}); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	var count int
	if err := repo.store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM storage_jobs WHERE attempt_id = ?`, attemptID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("%d jobs for one attempt, want 1", count)
	}
}

func TestJobsNotDueAreNotClaimed(t *testing.T) {
	repo, _ := newJobRepo(t)
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, Job{
		TenantID: "tenant-a", Kind: JobReconcile,
		RunAfter: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := repo.Claim(ctx, "worker", time.Minute); !errors.Is(err, ErrNoJob) {
		t.Errorf("a future job was claimed: %v", err)
	}
}

func TestClaimRequiresAnOwner(t *testing.T) {
	repo, _ := newJobRepo(t)
	if _, err := repo.Claim(context.Background(), "  ", time.Minute); err == nil {
		t.Fatal("an unowned claim must be refused; otherwise no worker can be identified")
	}
}

func TestPendingCountsRunnableWork(t *testing.T) {
	repo, _ := newJobRepo(t)
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, Job{TenantID: "tenant-a", Kind: JobReconcile}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	count, err := repo.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if count != 1 {
		t.Errorf("pending = %d, want 1", count)
	}
}

// seedAttempt inserts the minimum upload_attempts row a job can reference.
func seedAttempt(t *testing.T, store *Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := store.DB().Exec(`
		INSERT INTO tenants (id, name, created_at, updated_at) VALUES ('tenant-a','tenant-a',?,?)
		ON CONFLICT (id) DO NOTHING`, now, now); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := store.DB().Exec(`
		INSERT INTO storage_objects
			(id, tenant_id, user_id, logical_key, original_name, mime_type, category,
			 expected_size, provider, status, row_version, created_at, updated_at)
		VALUES ('obj_seed','tenant-a','user-1','key','a.bin','text/plain','report',
			0,'s3_compatible','uploading',1,?,?)`, now, now); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	if _, err := store.DB().Exec(`
		INSERT INTO storage_uploads
			(id, object_id, tenant_id, user_id, idempotency_key, request_fingerprint,
			 state, row_version, created_at, updated_at)
		VALUES ('upl_seed','obj_seed','tenant-a','user-1','k','f','uploading',1,?,?)`, now, now); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	if _, err := store.DB().Exec(`
		INSERT INTO upload_attempts
			(id, upload_id, provider, state, operation_generation, created_at, updated_at)
		VALUES (?, 'upl_seed', 's3_compatible', 'selected', 1, ?, ?)`, id, now, now); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
}
