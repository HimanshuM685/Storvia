# Proposed Go architecture

**Updated:** 2026-10-14

**Status:** Design, not implemented. [Storage policy](storage-policy.md) defines the confirmed direction and conservative routing rules. The schema, package boundaries, and API below are implementation proposals.

## 1. Components and authority

```text
Client
  |
  v
Go HTTP API: authentication, tenant checks, validation, streaming
  |
  v
Upload coordinator + Drive availability classifier
  |                       |
  v                       v
Drive adapter         S3-compatible adapter
(normal writes)       (eligible outage writes only)
  \                       /
   \--- one location -----/

Go API + workers -> metadata DB
                   objects, uploads, attempts, jobs, health evidence
```

- The database controls ownership, idempotency, selected location, and durable workflow state.
- The selected provider controls actual bytes and provider metadata. Reconciliation observes that provider; it never copies payloads elsewhere.
- Workers resolve ambiguous results, confirm deletion, and clean abandoned uploads. There are no content-copy or migration workers.
- Read-only preflight is an availability hint, not a guarantee that the next Drive write succeeds. Pin writes as soon as session initiation may have been sent.

## 2. Identity, OAuth, and secrets

- Keep application sign-in separate from Drive authorization. Resolve the tenant/user from a verified application session, never from a supplied `user_id`.
- Start with `drive.file` for app-created files in a visible folder; do not broaden scope to list unrelated Drive contents. Hidden app data is not part of the proposed MVP.
- Exchange codes server-side; validate one-time OAuth state, exact redirect URI, browser-session binding, and PKCE where supported by the selected flow. Redirect to a clean URL afterward.
- Encrypt refresh tokens with an externally managed encryption key. Persist scopes and connection status; keep access tokens short-lived and server-side.
- Handle refresh under a per-connection lock; preserve a valid refresh token when a response omits it. `invalid_grant` requires reconnection, not fallback.
- Company-managed S3 credentials are secret references, separate from per-user OAuth connections. Never send raw credentials to clients.
- Redact tokens, codes, session URIs, signed URLs, user file names where sensitive, and raw error bodies in logs/traces.

### Configuration boundary

Checked-in JSON contains only non-secret deployment settings, and nothing else. There
is no field in it that names a secret: no `client_secret_ref`, no `*_ref` of any kind.
An earlier design allowed a reference field naming the environment variable; it was
removed because a committed file should be unambiguously clean for anyone reviewing
it, with nothing secret-adjacent to interpret.

Ignored environment files hold the actual values for Google OAuth client credentials,
S3/R2 access keys, database passwords, Redis passwords, encryption keys, and
session-signing keys. The variable names the code reads are a convention, declared in
`internal/config/secrets_names.go` and listed by `AllSecretEnvVars()`, so validation,
redaction, and tests all read one source. Fallback credentials are chosen by vendor
rather than by backend id, so an `aws_s3` backend cannot be pointed at the R2 keys.

`TestCheckedInJSONHasNoSecretFields` enforces this against the real shipped files.

For the proposed visible folder, create `Storvia/` in the user's My Drive at connection setup. Save the folder ID; names are not unique. If the user deletes the folder, request explicit recovery rather than silently creating a replacement or falling back to S3. Folder creation needs its own idempotency/reconciliation handling.

### Development admin panel

The development-only `/admin` panel is a testing surface, not a second identity
system. It starts a Drive OAuth flow with a separate registered development callback
URI, binds the state to a short-lived HttpOnly browser cookie, and after a successful
code exchange issues a normal Storvia session for configured development tenant/user
IDs. The panel calls the existing connection service and displays only status,
subject, scope, and folder-presence metadata. Production configuration rejects an
enabled panel. The panel does not implement file transfer.

## 3. Single-location database model

Proposed relational tables; PostgreSQL is recommended but not yet selected by the user. Do not introduce `storage_object_replicas` or primary/backup roles.

### Connections and fallback configuration

```text
tenants
  id, name, created_at, updated_at
```

The tenant is Storvia's own identity boundary, created by the application and never
by a provider callback. Every scoped row below hangs off it, so a missing tenant
cannot be papered over with a bare string.

```text
drive_connections
  id, tenant_id, user_id, google_subject
  encrypted_refresh_token, key_reference, granted_scopes
  root_folder_id, changes_cursor
  status: connected | reauth_required | disconnected | blocked
  last_success_at, created_at, updated_at

storage_backends
  id, scope: deployment | tenant, tenant_id (required for tenant scope)
  provider: s3_compatible, vendor: aws_s3 | cloudflare_r2 | other
  endpoint, region, bucket, vendor (selects which credential variable name applies)
  status: active | disabled, capabilities, created_at, updated_at
```

The deployment chooses one fallback backend initially. Tenant eligibility is enforced even for a shared bucket. Only trusted operator configuration can select endpoints; prevent SSRF through client-controlled endpoints or redirects.

### Objects

```text
storage_objects
  id, tenant_id, user_id, logical_key, original_name, mime_type
  expected_size, verified_size, checksum_algorithm, verified_checksum
  provider: null | google_drive | s3_compatible
  drive_connection_id (nullable), backend_id (nullable)
  provider_file_id (nullable), provider_object_key (nullable)
  status: pending | uploading | outcome_unknown | ready | blocked |
          failed | deleting | deleted | missing
  fallback_reason (nullable), selected_at, verified_at
  row_version, created_at, updated_at, deleted_at
```

Constraints and checks:

- A ready object has exactly one complete provider location, with matching verified size. Pending objects may have no location yet.
- A Drive location has a tenant/user-matching Drive connection and file ID, with no S3 backend/key. An S3 location has one authorized backend/key and no Drive file pointer.
- Unique provider identity per connection/backend; unique `(tenant_id, user_id, logical_key)`.
- A ready location does not change on Drive recovery. API callers use logical IDs, not bucket keys or Drive file IDs as authority.
- No file bodies in the metadata database. A checksum alone is neither a file copy nor sufficient ownership proof.

### Uploads, attempts, and jobs

```text
storage_uploads
  id, object_id, tenant_id, user_id, idempotency_key, request_fingerprint
  active_attempt_id, state, row_version, created_at, updated_at

upload_attempts
  id, upload_id, provider, connection_or_backend_reference
  planned_provider_identity, encrypted_session_uri, multipart_upload_id
  state: selected | initiating | active | verifying | outcome_unknown |
         completed | cleanup_pending | aborted | failed
  mutation_may_have_started, confirmed_offset, expected_size
  parts_metadata, last_sanitized_error, operation_generation
  lease_owner, lease_expires_at, created_at, updated_at

storage_jobs
  id, object_id, attempt_id, kind: reconcile | delete | cleanup
  state, run_after, lease_owner, lease_expires_at, attempts, last_error
```

Use a unique `(tenant_id, user_id, idempotency_key)` and bind the key to immutable request metadata. Reusing a key with different data returns conflict. Keep its mapping while an object/result or unresolved attempt still exists; retry-key expiry must not permit a duplicate provider write.

Enforce one active or unresolved attempt per logical upload and keep historical attempts as metadata only. A DB lease expiring does not cancel an in-flight remote request. A replacement worker must reconcile that attempt before issuing any new mutation; row versions alone cannot fence a write already accepted by Drive.

## 4. Go package layout

Proposed module layout; keep provider details behind interfaces and keep HTTP handlers thin:

```text
cmd/storvia-api/main.go
internal/
  app/              dependency wiring and lifecycle
  apperr/           shared error vocabulary: kinds, HTTP statuses, RFC 9457 types
  auth/             application sessions, tenant/user identity
  cache/            cache interface, in-memory TTL default, Redis implementation
  config/           JSON configuration, .env loading, validation
  httpapi/          routes, request decoding, global error handler, middleware
  idid/             opaque identifier generation
  logger/           structured logging with mandatory redaction
  persistence/      repositories, migrations, dialect handling
  secrets/          AES-GCM keyring for refresh tokens, rotation overlap
  storage/
    model/          logical objects, uploads, attempts, provider states
    policy/         error classification, routing, retry budgets, transitions
    service/        use cases and transaction boundaries
    provider/       interfaces and capability-neutral types
      drive/        OAuth, connection registry, availability, token refresh
      s3compat/     S3 API adapter, multipart/presigned operations
migrations/         per-dialect SQL, embedded at build time
  sql/sqlite/
  sql/postgres/
sdk/go/             Go client SDK
sdk/ts/             TypeScript client SDK
config/             JSON configuration files
docs/
```

Cross-cutting packages worth naming explicitly, because each exists to make one
class of mistake impossible rather than merely discouraged:

- `apperr` gives the service one error vocabulary. Every failure reaching HTTP is an
  `*apperr.Error`, matched by kind with `errors.Is`, and mapped to an RFC 9457 body
  by one handler. An error that is not an `*apperr.Error` is coerced to a generic 500,
  so a bare `fmt.Errorf` from a deep package cannot become a client-visible string.
- `logger` applies redaction in the handler rather than asking callers to be careful.
  Sensitive attribute names are replaced with a marker before any record is written,
  including inside groups and through `With`.
- `config` refuses configuration that would be unsafe or non-functional: the broad
  Drive scope, a non-allow-listed endpoint, a retry budget that cannot fit its own
  attempts, and enabling fallback while the disclosure is unaccepted.
- `idid` mints unguessable opaque identifiers. No identifier encodes a tenant, a user,
  a file name, or a sequence number.
- `secrets` encrypts refresh tokens on the way into the database and decrypts them
  only inside the Drive adapter, so no other layer ever holds a usable token.

Suggested interfaces:

```go
type Provider interface {
	Name() string
	Availability(ctx context.Context, scope AvailabilityScope) error
	Initiate(ctx context.Context, meta ObjectMetadata) (Session, error)
	UploadChunk(ctx context.Context, session Session, chunk Chunk) (Progress, error)
	Complete(ctx context.Context, session Session) (ProviderObject, error)
	InspectUpload(ctx context.Context, session Session) (UploadState, error)
	Head(ctx context.Context, object ProviderObject) (ObjectMetadata, error)
	Open(ctx context.Context, object ProviderObject, byteRange *ByteRange) (io.ReadCloser, error)
	Delete(ctx context.Context, object ProviderObject) error
	Abort(ctx context.Context, session Session) error
}

type Router interface {
	Select(ctx context.Context, request UploadRequest) (Selection, error)
}
```

The Go service owns policy, transitions, idempotency, and tenant checks. Providers own HTTP details, OAuth token use, signing, status parsing, and provider-specific error normalization. Provider adapters must never update application rows directly.

The implementation exposes Drive and S3/R2 through **different** upload mechanics behind the same service: a Drive resumable session URI relayed to the client, and short-lived presigned/multipart URLs for the fallback. Do not force both providers into a misleading common resumable protocol.

Both are **direct to provider**. No file bytes pass through the Storvia API. The
`Provider` interface above is the original proposal and was deliberately not adopted
verbatim: a single `UploadChunk` would have implied a proxy path, which a session-based
transfer cannot support because the server holds no copy of the bytes. The implemented
split is `DriveSessionPort` (create a session, read back verified metadata) and
`FallbackUploader` (plan presigned capabilities, verify), with the coordinator owning
every decision between them.

## 5. HTTP API proposal

The API is provider-neutral and tenant-scoped:

```text
GET    /v1/storage/status
GET    /v1/storage/connections/google-drive
POST   /v1/storage/connections/google-drive/start
GET    /v1/storage/connections/google-drive/callback
DELETE /v1/storage/connections/google-drive

POST   /v1/storage/uploads
GET    /v1/storage/uploads/{upload_id}
POST   /v1/storage/uploads/{upload_id}/complete
POST   /v1/storage/uploads/{upload_id}/reconcile
DELETE /v1/storage/uploads/{upload_id}

GET    /v1/storage/objects/{object_id}
GET    /v1/storage/objects/{object_id}/content
DELETE /v1/storage/objects/{object_id}
```

Rules:

- All object/upload identifiers are opaque application IDs. Never accept provider IDs, bucket keys, or a client-selected provider as authority claims.
- `POST /uploads` requires a stable `Idempotency-Key`, immutable metadata, tenant authorization, and configured fallback consent.
- The response identifies the selected mode (`drive_resumable_session` or `s3_presigned_parts`) without returning refresh tokens or S3 credentials through list/detail endpoints. A Drive session URL is returned once, in the create response only, and is never persisted. without returning refresh tokens or S3 credentials through list/detail endpoints. A Drive session URL is returned once, in the create response only, and is never persisted.
- A fallback selection response is a new upload attempt for the same logical object; it is not a second ready object.
- `complete` is idempotent. Repeating it returns the existing verified object or its current pending/unknown status.
- `reconcile` is authenticated and rate-limited. It checks only the selected provider and updates `outcome_unknown`; it does not create a second location.
- Content download authorizes the application user first, then streams the recorded provider. Range requests must preserve safe headers and tenant checks.

### Upload request

```json
{
  "name": "report.pdf",
  "mime_type": "application/pdf",
  "size_bytes": 10485760,
  "category": "report"
}
```

The server generates `object_id`, `logical_key`, and provider-safe names. It validates declared size and type before any provider mutation and applies server-side byte limits while streaming. Client MIME is metadata, not malware/content validation.

## 6. Routing and upload state machine

### Preflight routing

1. Authenticate the application session and resolve tenant/user from the session.
2. Check allowed category, declared size, MIME allow-list, rate limits, fallback consent, and active provider configuration.
3. Load the user's Drive connection and run a bounded, read-only Drive availability check. Use narrow fields and the app folder; do not list unrelated user files.
4. Classify the result using `docs/storage-policy.md`.
5. Select Drive on success. Select the configured S3-compatible provider only for an eligible availability failure and only before a provider mutation.
6. Write the selected provider, provider identity, and routing evidence to the DB transaction before starting provider upload initiation.

The preflight result is not a lease on Drive. If Drive fails after initiation starts, the attempt remains pinned to Drive until its result is reconciled. The router never performs a blind second write.

### Drive path

1. Create or find the app folder using the stored ID and explicit recovery policy.
2. Start a Drive resumable upload with server-generated metadata, parented to the app folder.
3. **Relay the session URI to the client once and store nothing.** This step deviates
   from the original plan, which stored the session encrypted. A Drive session URI is a
   write capability for the file: persisting it so it survives a restart would mean
   holding that capability at rest. The cost is real and is accepted — an interrupted
   Drive upload cannot be resumed by Storvia, and reconciliation reports `unknown`
   rather than guessing.
4. The client `PUT`s each chunk with its own `Content-Range`; Storvia sees no bytes.
5. The client reports the file ID Google returned at completion. **That ID is untrusted
   input** and is treated as a lookup key: Storvia re-reads the file and adopts it only
   if it is inside the app folder, is not trashed, and has the declared size.
6. On success, commit one `ready` location.

### S3-compatible fallback path

1. Select the configured backend and generate a tenant-scoped key from the logical object ID and attempt generation.
2. Create one multipart upload or signed upload plan. The browser receives only short-lived, operation-scoped capabilities.
3. Complete the multipart upload idempotently.
4. `HeadObject`/equivalent and compare expected size, content type, and checksum where available.
5. Commit the same logical object as one `ready` S3 location. It stays on S3 after Drive recovers.

Do not enable a copy job, dual-write, provider-side replication, or automatic S3-to-Drive migration as part of this project. Provider durability/replication internal to the selected vendor is outside the application-managed copy invariant.

### State transitions

```text
pending -> initiating -> active -> verifying -> ready
             |            |          |
             |            |          +-> outcome_unknown
             |            +-> outcome_unknown
             +-> failed / blocked

outcome_unknown -> active     (same provider, after reconciliation)
outcome_unknown -> verifying  (same provider, after reconciliation)
outcome_unknown -> failed     (confirmed no committed object)
failed/blocked -> initiating  (new attempt only after cleanup/reconciliation)
ready -> deleting -> deleted | missing
```

`outcome_unknown` is not a provider outage classification. It means Storvia cannot yet know whether the selected provider accepted a mutation. It is never eligible for immediate cross-provider failover.

The table is enforced by `policy.CanTransition` and covered by tests. Two properties
are worth restating, because they are the ones a well-meaning change would break:

- **`ready` has no transition back to any pre-transfer state.** Re-adding `ready -> uploading` would let one logical object hold a second storage location.
- **`outcome_unknown` cannot reach `ready` directly.** It must pass through a provider inspection, because the entire point of the state is that the outcome is not yet known.

## 7. Failure classification and retry budgets

Provider adapters return normalized errors containing operation, provider, HTTP/status class, provider reason where safe, retryability, and `mutation_may_have_started`. They do not expose raw provider payloads to clients.

| Class | Examples | Coordinator action |
|---|---|---|
| Validation | invalid MIME, size, tenant, object state | Reject; no provider retry |
| Authentication/authorization | expired token, `invalid_grant`, admin block, insufficient permission | Reconnect or resolve policy; no S3 fallback |
| User/provider quota | Drive full, `storageQuotaExceeded`, Drive rate limit | Explain/backoff; no S3 fallback under current policy |
| Availability before mutation | bounded DNS/connect timeout, connection reset, 500/502/503/504 during read-only preflight | Eligible for S3 selection if all gates pass |
| Availability after mutation | timeout or transport error after request may have been sent | `outcome_unknown`; same-provider reconciliation |
| Provider transient after confirmed session | 5xx/rate-limit during Drive/S3 transfer | Bounded retry/resume on same provider |
| Missing selected object | Drive/S3 404 after reconciliation | Mark `missing`; do not recreate or switch location automatically |
| Configuration/security | invalid endpoint, signature mismatch, TLS verification failure, missing credentials | Disable/degrade backend, alert; no blind retry |

Use context deadlines, bounded exponential backoff with jitter, and one coordinator-wide budget. The AWS SDK's retryer must not create unbounded or multiplied retries. A timeout is never treated as proof that a write failed.

## 8. Downloads, deletion, and reconciliation

### Download

- Load object by tenant-scoped logical ID and require `ready`.
- Open only the recorded provider location. Do not try a second provider because Drive is currently down; there is no copy.
- For Drive, refresh OAuth server-side and use media download with range support where needed.
- For S3/R2, proxy `GetObject` by default; use a short-lived presigned GET only where the privacy model explicitly allows it.
- Translate provider missing/auth/quota errors into stable application states without exposing credentials or signed URLs.

### Delete

- Set the logical object to `deleting` in a transaction and pin deletion to its recorded provider.
- Treat a confirmed provider `404` as already absent only after checking the provider error semantics; do not claim deletion before a confirmed result or a documented already-absent result.
- Retry a failed delete through a job. Never delete a same-named object in another provider.
- After deletion, retain only minimal metadata required by legal, audit, support, or idempotency policy. Do not retain bytes.

### Reconciliation

- Drive: store and advance a changes cursor only after each page is processed; use push notifications only as wake-up hints.
- S3/R2: inspect only the selected key on ambiguous attempts and periodically list the Storvia-controlled prefix for orphan detection. Listing is cleanup evidence, not permission to recreate objects.
- A reconciliation job may conclude `ready`, `failed`, `missing`, `cleanup_pending`, or `outcome_unknown`; it may not write content to a different provider.
- Orphan cleanup needs a grace period and audit record because a delayed DB commit or lost response can make a valid object look orphaned.

## 9. Observability and security

Metrics:

- Drive preflight success, timeout, transient failure, latency, and circuit state by tenant/provider region;
- upload initiation, chunk/part, completion, verification, and outcome-unknown counts;
- fallback count and reason, with no file content or credentials;
- provider retry counts, queue age, reconciliation age, orphan cleanup, and missing-file count;
- bytes sent/received and request duration percentiles.

Every state transition gets a correlation ID, tenant ID, logical object ID, upload/attempt ID, provider, operation, normalized error class, and attempt generation. Avoid provider file IDs and names in broad telemetry unless needed and redacted.

### What `/metrics` actually exposes

Prometheus text format 0.0.4, so an existing scraper reads it with no configuration. The
series are chosen to answer one question: where did this upload go, and did it finish.

| Metric | Type | Labels | Answers |
|---|---|---|---|
| `storvia_uploads_started_total` | counter | `provider` | How much traffic lands on each provider. |
| `storvia_uploads_finished_total` | counter | `provider`, `outcome` | Did uploads complete, and on what. |
| `storvia_uploads_outcome_unknown_total` | counter | `provider` | Work for a person. Each one is a real incident. |
| `storvia_bytes_stored_total` | counter | `provider` | Where the bytes actually went. |
| `storvia_fallback_routings_total` | counter | `reason` | Why Drive was skipped, not merely how often. |
| `storvia_provider_calls_total` | counter | `provider`, `operation`, `class` | "How often is Drive unavailable" versus "refusing us". |
| `storvia_job_runs_total` | counter | `kind`, `outcome` | Whether the queue is draining. |
| `storvia_orphans_found_total` | counter | `provider` | Provider bytes with no Storvia record. |
| `storvia_upload_duration_seconds` | histogram | `provider` | Whether a slow upload is the client's pace or Drive's health. |
| `storvia_fallback_consent` | gauge | `tenant` | Whether the fallback is currently open. |
| `storvia_objects_stored` | gauge | `provider`, `state` | Tenant object counts. |
| `storvia_build_info` | gauge | `version`, `go`, `pid`, `environment` | Which build answered, without correlating against a deploy time. |

Three properties are load-bearing rather than incidental:

- **Series keys are built from sorted label names.** Go map iteration order is randomised,
  so a key built by ranging over the map would mint a new series roughly half the time and
  a counter would appear to reset at random.
- **`Gather` is deterministic.** Families in registration order, label sets sorted. A
  scrape that reorders between calls makes diffs useless.
- **Every recorder tolerates a nil receiver.** Instrumentation that can panic takes down
  the request it measures. A nil guard is only as deep as the first dereference — this bit
  me when the guard existed on the leaf counter but not on the collector holding it.

Counters that are obviously zero everywhere are omitted rather than shipped.

### Operator routing control

Two switches exist and they are not the same thing.

**Fault injection** pretends Drive is broken so the fallback path can be exercised on
demand. The provider is healthy; the service is lying to itself. It is gated on by
environment, refused outside development, and driven at runtime through an atomic flag.

**Forced fallback** actually prefers the fallback. It answers a different question:
during an incident, "does the fallback work at all?" and "can we keep serving users?"
are both unanswerable while routing still prefers Drive. It is `fallback.force` in
configuration with a live override from the console, where the override is tri-state
(force, prefer Drive, follow config) because "stop overriding me" is not expressible as a
boolean.

Two limits are deliberate and tested:

- **Consent still applies.** An operator override changes which provider wins; it does
  not manufacture the tenant's agreement to have files stored elsewhere.
- **An in-flight mutation stays pinned to Drive.** The override chooses a provider for a
  *new* upload. It cannot relocate bytes that may already have reached Drive, because
  that is how a duplicate file gets created — the single failure this design exists to
  prevent.

Ordering inside `Evaluate` is the contract: hard stops first (validity, authorization),
then the mutation check, then the operator override, then consent and configuration
gates, then the provider preflight. An override that can do anything is not an override;
it is a way to disable the safety rules.

Forcing also **skips the Drive preflight** rather than merely ignoring its result. During
an incident a wasted round trip to a struggling provider is not free.

### Background work

The `storage_jobs` table is a lease queue, and the lease is the point.

- **Claiming is a lease, not a delete.** A crashed worker's jobs become claimable again
  when the lease expires rather than being lost. The `UPDATE` is conditional on the row
  still being claimable, so two workers racing for one row cannot both win.
- **Finishing and renewing require ownership.** A worker that lost its lease stops, because
  finishing would mark another worker's in-flight work as done. This is the double-delete
  the lease exists to prevent.
- **A failing job retries with backoff and is then parked** as `failed`. A job that always
  fails is a problem for a person to look at, not something to spin on.
- **Enqueue is idempotent per `(attempt_id, kind)`**, so a retrying client cannot produce
  two jobs that both delete the same object.
- A retrying delete stays pinned to the recorded provider, so it cannot move the deletion
  somewhere else.
- **Background work owns a cancellable context**, and `Stop` cancels before it waits.
  Waiting for a goroutine to finish is only correct if something has cancelled it;
  relying on the process signal handler having run first deadlocks every caller that is
  not a signal.

The orphan sweep **reports and never deletes**. Listing the provider is cleanup
*evidence*, not permission to remove: Storvia keeps no second copy, and a key it does not
recognise may be a file whose database commit is still in flight. Acting on that would be
the worst thing this service could do. A grace period and an explicit decision are
required first.

The Drive change log is polled from a stored cursor rather than driven by push
notifications, because a push is only a hint: it can arrive twice, be missed while a
deployment is down, and arrive for accounts Storvia no longer cares about. **The cursor is
advanced after the page is processed, never before** — advancing first and then failing
skips changes permanently, while advancing second re-reads a page on a crash, and every
change is idempotent. Re-reading is the safe direction.

Security tests must cover OAuth state/tenant mix-up, IDOR by object ID, cross-tenant backend selection, signed URL leakage, session URI leakage, client-controlled object key, size-limit bypass, replayed completion, duplicate idempotency keys, and deletion authorization.
