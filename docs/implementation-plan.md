# Go implementation plan

**Updated:** 2026-10-14

**Status:** Planning only. This file describes work to implement; no Go commands or provider integrations have passed yet.

## 1. Confirmed requirements

- Google Drive is the normal primary destination for new uploads.
- Use one configured S3-compatible backend only when Drive is not responding/available under the classification in [storage policy](storage-policy.md).
- Do not make application-managed duplicate file copies. No dual-write, replication, mirroring, background copy-back, or automatic migration.
- Preserve one logical object and one selected provider location.
- Ambiguous writes stay on the selected provider until reconciled. A timeout is not proof of failure.
- Implementation language is Go.

## 2. Proposed choices still requiring approval

These are recommendations, not confirmed requirements:

- Go module import path and minimum Go version.
- PostgreSQL for metadata and workflow state.
- `net/http` for the API, `chi` or standard `http.ServeMux` for routing, and `slog` for structured logs.
- Google official Go clients where they reduce protocol risk; AWS SDK for Go v2 S3 API with a custom endpoint for R2, subject to compatibility tests.
- Backend-proxied Drive chunks for the first vertical slice; direct presigned multipart S3/R2 only after its security and CORS tests.
- One deployment-wide fallback backend initially, with tenant-scoped eligibility in configuration.
- A three-second Drive preflight timeout, two attempts, and an eight-second routing budget as initial values to load-test.

Do not install dependencies, select a vendor, or provision cloud resources until the owner approves choices that affect cost, data region, or operational authority.

## 3. Acceptance criteria

The first pilot is complete only when:

1. A user can connect Drive, upload a supported file, see it in the Storvia folder, download it, and delete it.
2. The application cannot access another tenant/user's object by changing an opaque object ID.
3. New uploads select Drive after a successful read-only availability check.
4. An eligible Drive preflight outage selects S3/R2 before any provider mutation and records the reason.
5. Quota, revoked consent, admin policy, rate limiting, validation errors, and missing objects do not trigger fallback.
6. A timeout after a possible provider write produces `outcome_unknown` and same-provider reconciliation, not a second write.
7. Every ready object has exactly one recorded provider location and verified size; no application copy exists elsewhere.
8. After recovery, new uploads use Drive again; existing S3 fallback files remain on S3 and are not copied back.
9. Credentials, tokens, session URLs, presigned URLs, raw private payloads, and file bytes do not enter logs or normal API responses.
10. The exact Go checks run for the implemented packages and their results are recorded in Markdown.

## 4. Milestones

### M0 — Product contract and repository hygiene

Deliverables:

- Keep `AGENTS.md`, `README.md`, `docs/storage-policy.md`, and this plan aligned.
- Confirm tenant model, application authentication, supported file classes, limits, Drive folder UX, fallback consent, and deletion wording.
- Choose Go version/module path, database, first fallback vendor, region, secret manager, and deployment target.
- Record accepted versus proposed decisions; no credentials in the repository.

Exit checks:

- The owner agrees that fallback is outage-only and that no copies are retained.
- Data protection/privacy review accepts that an outage upload can be stored outside the user's Drive.

### M1 — Go foundation and policy engine

Deliverables:

- Initialize the Go module and commands under `cmd/storvia-api`.
- Add configuration loading with explicit required/optional fields and redacted startup diagnostics.
- Add domain types for tenant identity, logical objects, upload attempts, provider selection, normalized errors, and state transitions.
- Implement `internal/storage/policy` as a pure package with no network or database dependency.
- Implement routing preconditions, Drive availability classification, retry-budget math, and the single-location invariant.
- Add unit/property tests for every table in `docs/storage-policy.md`.

Exit checks:

```text
go test ./internal/storage/policy/...
go vet ./internal/storage/policy/...
```

These commands are planned until the Go module exists.

### M2 — Database and idempotent workflow state

Deliverables:

- Add migrations for `drive_connections`, `storage_backends`, `storage_objects`, `storage_uploads`, `upload_attempts`, and `storage_jobs` as specified in `docs/architecture.md`.
- Add tenant-scoped repositories with transaction helpers and optimistic row versions.
- Enforce unique idempotency keys and request fingerprints.
- Add attempt leases, operation generations, `mutation_may_have_started`, `outcome_unknown`, and cleanup states.
- Make repository methods require an authenticated tenant/user scope rather than accepting arbitrary authority IDs.

Tests:

- duplicate initiation with the same metadata returns the original upload;
- same idempotency key with different metadata returns conflict;
- two tenants cannot observe or mutate one another's objects;
- a lease expiry cannot authorize a replacement provider mutation while the first attempt is unresolved;
- completion is idempotent and has one ready transition.

### M3 — Drive OAuth and thin vertical slice

Deliverables:

- Configure development-only Google OAuth credentials outside the repository.
- Implement state-bound connect/callback/reconnect flows and encrypted refresh-token persistence through an injected secret service.
- Create/find the visible `Storvia/` folder with `drive.file`; persist the folder ID.
- Implement Drive preflight, resumable session initiation, backend-proxied chunk upload, completion verification, private range download, and delete.
- Translate Drive errors into normalized classes without leaking provider payloads.

Tests and manual checks:

- OAuth state mismatch, account switch, declined consent, revoked grant, and `invalid_grant`.
- tenant isolation and application authorization on every object operation.
- successful small upload/download/delete against a controlled Drive test account.
- interrupted chunk after body-before-response: query status and resume from Drive's confirmed range.
- Drive quota, admin policy, missing folder, missing file, 401, 403, 404, 429, and 5xx handling.

Do not claim M3 complete without a real controlled provider smoke test; mocked provider tests alone are insufficient.

### M4 — S3-compatible outage fallback

Deliverables:

- Implement `internal/storage/provider/s3compat` against the common S3 API surface.
- Configure private bucket/prefix, region/endpoint, server-generated tenant-scoped keys, lifecycle cleanup for abandoned multipart uploads, and credential references.
- Validate AWS S3 first or R2 first according to the approved vendor decision; do not imply both are verified by one implementation.
- Implement bounded Drive preflight and circuit evidence. The circuit is scoped to the Drive connection/runtime context and cannot be opened by quota, auth, or validation errors.
- Select S3 only before a provider mutation when the policy classifies Drive as unavailable.
- Implement multipart initiation/part completion or a bounded backend-proxy path, `HeadObject` verification, private streaming download, and delete.
- Pin an upload attempt to S3 after initiation may have started; no retry on Drive after S3 selection.

Provider contract tests:

- server-generated key cannot be changed by the client;
- private bucket and short-lived capability configuration;
- content length/type/checksum verification;
- lost completion response is safe to retry;
- `NoSuchKey`/404 is missing, not a reason to recreate on Drive;
- credentials/signature/configuration failures do not open Drive fallback or cause an unbounded retry;
- multipart abort and orphan cleanup use a grace period;
- the same test suite runs against the selected real provider in a non-production bucket.

### M5 — Ambiguous-result recovery and reconciliation

Deliverables:

- Implement same-provider upload status inspection for `outcome_unknown`.
- For Drive, query the resumable session, inspect final metadata, and reconcile file ID/size/MIME.
- For S3, check the exact key/multipart upload state, never a second provider key.
- Add jobs with leases, bounded attempts, backoff, and sanitized errors.
- Add Drive changes-feed reconciliation for known app files; use notifications only as wake-up hints.
- Add selected-provider orphan cleanup only after a conservative grace period.
- Ensure deletion jobs use the recorded location and do not cascade to a different provider.

Required fault tests:

| Fault | Expected result |
|---|---|
| Drive preflight timeout before initiation | Select S3 once, record outage reason, no Drive write |
| Drive upload timeout after initiation | Keep Drive attempt `outcome_unknown`, reconcile Drive, no S3 write |
| S3 completion timeout after multipart initiation | Reconcile exact S3 key/upload; no Drive write |
| DB response lost after provider commit | Idempotent completion/reconciliation returns one ready object |
| Drive recovers while S3 upload is active | Finish S3; new uploads use Drive; no copy-back |
| Drive quota error | User-facing quota state; no S3 fallback |
| Invalid grant/admin policy | Reconnect/block state; no S3 fallback |
| Provider object missing | Mark selected location missing; do not recreate automatically |
| Client retries with changed metadata | Idempotency conflict; no new provider mutation |

### M6 — Security, operations, and pilot gate

Deliverables:

- Add redacted structured logs, metrics, correlation IDs, provider health evidence, fallback counts, and unresolved-attempt alerts.
- Add graceful HTTP shutdown, request cancellation, bounded outbound clients, connection reuse, and separate upload/download deadlines.
- Review CORS only for approved upload origins; never expose provider credentials.
- Add secret rotation and emergency disablement runbooks for Google OAuth and S3/R2.
- Document user-visible Drive/fallback storage, data region, access, retention, and deletion/disconnect behavior.
- Run dependency/license/security review and verify production configuration has no test credentials or broad Drive scopes.

Pilot gate:

- [ ] M0 decisions approved.
- [ ] `gofmt`, `go vet`, unit tests, integration tests, and race tests have actual recorded results.
- [ ] OAuth/tenant isolation/security tests pass.
- [ ] Drive happy path passes against a controlled account.
- [ ] S3/R2 happy path passes against the selected provider's non-production bucket.
- [ ] Failure matrix passes with no duplicate application-managed file location.
- [ ] Ambiguous writes remain same-provider until reconciliation.
- [ ] Quota/auth/admin/validation errors do not trigger fallback.
- [ ] New uploads return to Drive after recovery; no copy-back job exists.
- [ ] Orphan cleanup has dry-run/audit behavior.
- [ ] Privacy/legal and operational runbooks are approved.

## 5. Proposed Go checks

The repository has no Go module yet, so these are planned commands, not passed checks:

```text
gofmt -w ./cmd ./internal
go test ./...
go test -race ./...
go vet ./...
go test ./... -cover
```

Add provider integration checks only behind explicit environment configuration and a non-production account/bucket. Never put credentials or signed URLs in test fixtures. A test that uses only mocks must be labeled unit/contract coverage, not a real provider check.

## 6. Documentation update rule

After each meaningful implementation change, update the relevant Markdown with:

- confirmed requirement versus proposed choice;
- changed package/API/schema behavior;
- actual commands and results, including failures;
- remaining risk or blocked decision;
- the exact next step for a future session.

Never write "passed" for a planned check. Keep provider incidents and raw private payloads out of this documentation.

## 7. Immediate next step

Before scaffolding code, approve the proposed choices in section 2, especially the Go module path, database, first fallback vendor, deployment target, and exact meaning of a Drive preflight outage. Then create the Go module and implement M1 policy tests first.
