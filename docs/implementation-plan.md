# Implementation plan and future reference

This plan is intentionally provider-neutral at the application boundary and Google-Drive-specific only inside the storage adapter.

## Phase 0 — confirm the product contract

Write down these answers before coding:

- Is Drive optional or required?
- Are stored files user-visible, hidden app data, or both?
- What file types and maximum size are allowed?
- What are the daily upload/download and per-user limits?
- Are files user-owned backups, or company-owned primary records?
- What happens when the user disconnects, deletes the folder, loses quota, or revokes access?
- Does the company need a second copy?
- Which users are in scope: consumer Google accounts, Workspace accounts, or both?

**Decision gate:** If the answer is "company-owned primary records" or "strict retention," use a company-controlled object store and consider Drive only as an export destination.

## Phase 1 — Google Cloud and OAuth setup

1. Create separate Google Cloud projects/credentials for development, staging, and production.
2. Enable the Google Drive API.
3. Configure the OAuth consent screen, authorized domains, support email, privacy policy URL, and product name.
4. Register exact HTTPS redirect URIs for production and exact localhost URIs for development.
5. Start with `drive.file`; add `drive.appdata` only if hidden app configuration is needed.
6. Test consent for a new user, an existing grant, a declined scope, a revoked grant, and a managed Workspace account.
7. Document the selected scopes and submit the app for the applicable verification/brand review before a broad public rollout.

### Suggested consent copy

> Connect Google Drive to save files created by **[Company App]** in a dedicated folder in your Google Drive. These files use your Google Drive storage quota. We do not need access to unrelated Drive files for this feature. You can disconnect Google Drive at any time from your account settings; see the privacy policy for what happens to files already saved in Drive.

Do not ship this copy unchanged. It must match the actual scope, folder visibility, content processing, employee/processor access, retention, and deletion behavior.

## Phase 2 — thin vertical slice

Implement one complete path before adding all file categories:

1. connect Google Drive;
2. create the application folder;
3. initiate a small upload;
4. upload it through the backend proxy;
5. mark the DB object `ready` only after provider confirmation;
6. download through the private application endpoint;
7. delete through the application endpoint;
8. show the file in both the application and the user's Drive.

### Minimum acceptance criteria

- No refresh token or access token appears in browser storage, logs, URLs, or API responses.
- The application cannot read a second user's object by changing an object ID.
- A Drive file ID is stored and used; a path is not treated as identity.
- A user can see which folder and file category the app created.
- A user with a full Drive receives an actionable message.
- A user who declines Drive consent can still use non-Drive account features.

## Phase 3 — resumable and large-file behavior

Add resumable upload support in this order:

1. choose a chunk size that is a multiple of 256 KiB; begin with a conservative configured size such as 8 MiB or 16 MiB;
2. persist the provider session URI and known offset encrypted;
3. use `Content-Range` for each chunk;
4. handle `308` and the returned `Range` header;
5. query upload status after ambiguous network failures;
6. restart the provider session on session `404` while retaining the same logical object/upload ID;
7. mark only the final verified response as `ready`;
8. add bounded retries for 5xx/429/rate-limit responses;
9. add idempotency for initiation and completion;
10. delete or reconcile abandoned provider files.

### Failure-injection test matrix

| Test | Expected result |
|---|---|
| Disconnect before first chunk | Upload remains resumable or is safely aborted. |
| Disconnect after chunk body, before response | Client queries status and resumes at provider-reported byte. |
| 308 with partial `Range` | Next request starts at the exact next byte. |
| 5xx from provider | Bounded retry/resume; no duplicate logical object. |
| 404 expired session | New provider session, same logical object, no silent duplicate catalog row. |
| Client retries `complete` | One ready object and one DB transition. |
| Wrong MIME declaration | Rejected or quarantined; not trusted as a security check. |
| Size exceeds policy | Rejected before provider upload. |
| Provider file deleted during download | Stable application `missing` state and user-facing recovery. |

## Phase 4 — reconciliation and operations

Implement a worker per connected Drive account or a bounded shared queue:

- save a Drive changes page token;
- process changes page-by-page;
- renew notification channels before expiration if push is enabled;
- run a periodic full app-folder reconciliation;
- detect DB rows with missing Drive files;
- detect Drive files with no DB row and apply a grace-period cleanup policy;
- surface `reauth_required`, `quota_exceeded`, `admin_blocked`, and `missing` states;
- emit metrics for upload time, retry count, Drive error reason, bytes, egress, and queue age.

### Operational dashboards

Track at minimum:

- connected Drive accounts by state;
- OAuth refresh failures and `invalid_grant` rate;
- upload initiation success/failure;
- upload completion rate and p50/p95/p99 duration;
- bytes uploaded/downloaded;
- 403 quota/rate-limit counts;
- 404 missing-file counts;
- abandoned uploads and orphan cleanup count;
- reconciliation lag per connection;
- notification channel renewal failures;
- application download authorization failures.

### Runbooks

Prepare runbooks for:

1. Google OAuth client secret rotation;
2. refresh-token encryption-key rotation;
3. user reconnect after `invalid_grant`;
4. a Workspace admin blocks the app;
5. Drive quota exhaustion;
6. Drive API rate limiting;
7. an upload session expires;
8. a user deletes the app folder;
9. cleanup of orphaned provider files;
10. emergency revocation of all grants for a user or environment.

## API contract example

### Start an upload

```http
POST /v1/storage/uploads
Authorization: Bearer <application-session>
Idempotency-Key: <stable-client-attempt-id>
Content-Type: application/json

{
  "name": "report.pdf",
  "mime_type": "application/pdf",
  "size_bytes": 10485760,
  "category": "report"
}
```

```json
{
  "upload_id": "app-upload-id",
  "object_id": "app-object-id",
  "mode": "proxy",
  "chunk_size_bytes": 8388608,
  "expires_at": "2026-10-14T12:00:00Z"
}
```

### Send a chunk through the proxy

```http
PATCH /v1/storage/uploads/app-upload-id
Authorization: Bearer <application-session>
Content-Length: 8388608
Content-Range: bytes 0-8388607/10485760
Content-Type: application/octet-stream

<chunk bytes>
```

The backend forwards the chunk to Drive and returns the provider-confirmed next offset. The client must use the returned offset instead of incrementing blindly.

### Complete

```http
POST /v1/storage/uploads/app-upload-id/complete
Authorization: Bearer <application-session>
Idempotency-Key: <same-or-derived-attempt-id>
```

The backend verifies the Drive file ID, expected size, MIME type, ownership mapping, and DB transition before returning the object record.

## Migration path to object storage

Keep these fields from day one so migration is possible:

- `provider`;
- `provider_file_id` or equivalent provider key;
- `logical_key`;
- `checksum`;
- `size_bytes`;
- `mime_type`;
- `created_at` and `updated_at`;
- `retention_class`;
- `encryption_state`;
- `status`.

Migration sequence:

1. add a second provider adapter;
2. route new uploads for selected tenants/features to the new provider;
3. copy ready Drive objects in a background job;
4. verify size/checksum and record the new provider pointer;
5. switch reads using a per-object provider field;
6. retain or delete Drive copies according to the user-visible policy;
7. remove the Drive connection only after the user has been informed.

## Definition of done for production pilot

- [ ] OAuth, privacy, consent, and verification review complete.
- [ ] Token encryption and redaction reviewed.
- [ ] Tenant-isolation tests pass.
- [ ] Small and large uploads pass with failure injection.
- [ ] Private range downloads pass.
- [ ] Quota, rate-limit, 401/403/404/429/5xx translation is implemented.
- [ ] Reauth and disconnect workflows are user-tested.
- [ ] Changes feed and reconciliation worker are running.
- [ ] Orphan cleanup has a dry-run mode and audit trail.
- [ ] Metrics, alerting, and runbooks exist.
- [ ] Product has explicitly accepted Drive's user-controlled availability and quota.
- [ ] A provider-neutral storage interface exists before application features depend on Drive details.
