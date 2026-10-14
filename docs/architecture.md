# Proposed architecture

This document turns the research into an implementation approach for a backend that catalogs application files in a database while storing the file bytes in the authenticated user's Google Drive by default, with policy-controlled AWS S3 or Cloudflare R2 fallback and optional replication.

## 1. System model

```text
                                  +----------------------+
                                  |  Google OAuth / Drive |
                                  +----------+-----------+
                                             |
                                             | authorization code,
                                             | access + refresh tokens
                                             v
+-------------+       +----------------------+---------------------+
| Web/mobile  |<----->| Application API / storage service         |
| client      |       | - user/session authorization              |
+------+------+       | - OAuth callback and token refresh       |
       |              | - Drive folder/file operations            |
       |              | - provider routing and health             |
       |              | - upload session coordinator              |
       |              | - private download proxy                 |
       |              +------------+------------------+------------+
       |                           |                  |
       |                           |                  |
       v                           v                  v
  upload chunks              +-----+------+     +-----+------+
  or proxy stream            | App DB     |     | Job queue  |
                             | catalog    |     | retries    |
                             +------------+     +------------+
                                      |
                                      v
                              provider object IDs,
                             status, hashes,
                             ownership metadata
```

The application database is authoritative for **which application user owns a logical object**, which provider is currently primary, and which replicas are ready. Each provider is authoritative for **whether its object still exists and what provider metadata it currently has**. The systems are reconciled; neither provider is assumed to be perfectly immutable.

## 1.1 Provider roles and fallback semantics

Google Drive is the default primary for user-visible, user-owned files. AWS S3 or Cloudflare R2 may be selected as a fallback for a new upload when Drive is unavailable, full, revoked, or blocked. The provider is selected before the upload begins; an in-progress Drive resumable session cannot be converted into an S3 multipart upload.

Failover and replication are separate policies:

- **Failover** preserves the logical object but stores the new provider copy in S3/R2. It does not protect an existing Drive-only copy from later user deletion.
- **Replication** creates a second verified provider copy for selected objects. It improves recoverability but creates a company-controlled copy that must be disclosed and included in deletion, retention, and disconnect policy.

Reads use the provider recorded for the ready replica. The application must not blindly try Drive and then S3 unless both copies exist and the fallback policy explicitly permits that behavior.

## 2. Identity and Drive authorization

### Do not conflate sign-in with storage consent

Google sign-in answers: "Who is this person?"  
Drive OAuth authorization answers: "May this application manage specific files in this person's Drive?"

The lowest-friction flow is:

1. Let the user create an application account with the company's normal identity flow.
2. Offer **Connect Google Drive** as an explicit capability during onboarding or when the first backup/report is created.
3. Explain that files will be written to the user's Drive, count against the user's Drive quota, and remain governed by Drive permissions.
4. Request only the scopes needed for the selected feature.
5. Record the granted scopes returned by Google. If a user declines a scope, keep the account usable and disable only Drive-backed features.

Google's server-side OAuth flow requires an exact redirect URI, a random `state` value checked on callback, and `access_type=offline` when the backend needs to act while the user is away. The authorization code should be processed on the backend and followed by a redirect to a clean URL.

### Scope choice

| Scope | Use | Recommendation |
|---|---|---|
| `https://www.googleapis.com/auth/drive.file` | Create new files and modify files created/opened/shared with the app | Default for a visible application folder and app-created files. |
| `https://www.googleapis.com/auth/drive.appdata` | App-only hidden application data | Use only for small configuration/sync state that users should not browse or share. |
| `https://www.googleapis.com/auth/drive` | All Drive files | Avoid for this product unless a reviewed feature genuinely needs it. It is a restricted scope with substantially heavier verification/security implications. |

The exact scope behavior must be validated in a proof of concept with the chosen Google Picker/folder UX. Do not silently switch to the broad `drive` scope because a narrow-scope call failed without first understanding which file was created/opened by the app.

### Token handling

Store only what the backend needs:

- Google subject identifier (`sub`), not an email address as the durable identity key;
- encrypted refresh token;
- granted scope set;
- token status and last successful refresh time;
- connection state and revocation time.

Access tokens are short-lived and should be held in memory only as needed. Refresh tokens are long-lived credentials and should be encrypted using a KMS/envelope-encryption design or the company's existing secrets/data protection service. Never log them, put them in URLs, return them to the browser, or commit them to source control.

## 3. Drive folder layout

For user-visible files, create one application-owned folder in the user's My Drive:

```text
My Drive/
└── Company App/
    ├── reports/
    ├── uploads/
    └── exports/
```

Keep the hierarchy shallow. The user may rename, move, share, or delete it. Store the folder ID and re-discover/recreate only under an explicit recovery policy; do not identify it by name alone because names are not unique.

Use `appDataFolder` only when the user should not interact with the files. It is hidden from the Drive UI and accessible only to the app, but it cannot be shared, moved between storage spaces, or trashed through the API. That makes it a poor home for reports that the user is expected to find and download.

### File naming and identity

- Generate application logical IDs server-side.
- Generate collision-resistant Drive names; do not use a user-provided name as the only identity.
- Store a provider object pointer (`drive_file_id` for Drive or bucket/key for S3/R2), never a path, as a replica attribute.
- Treat the Drive file ID as stable while the file exists; names and parent folders are mutable.
- Optionally store an application object ID in Drive's private application properties when supported, but keep the database mapping authoritative.

## 4. Database model

The following is provider-neutral pseudocode. Adapt names and types to the company's database conventions.

### `storage_connections`

```text
id                         UUID / bigint primary key
user_id                    application user foreign key
provider                   enum('google_drive')
provider_subject           Google OIDC sub, unique per provider
encrypted_refresh_token   ciphertext / key reference
granted_scopes             JSON array
root_folder_id             nullable Drive file ID
status                     connected | reauth_required | revoked | disconnected
last_refresh_at            timestamp nullable
last_success_at            timestamp nullable
created_at, updated_at     timestamps
```

Constraints:

- unique `(user_id, provider)`;
- unique `(provider, provider_subject)` where the product allows one connection per Google account;
- never expose `encrypted_refresh_token` through application APIs.

User OAuth connections and company-managed S3/R2 credentials are different concerns. Keep S3/R2 access keys in a separate backend configuration or secret reference; do not model them as user OAuth connections.

### `storage_backends`

```text
id                         UUID / bigint primary key
scope                      system | tenant
provider                   enum('aws_s3', 'cloudflare_r2')
endpoint                   provider endpoint
bucket                     bucket name
region                     nullable provider region (`auto` for R2)
credential_ref             secret-manager/KMS reference, never raw credentials
capabilities               JSON object for provider-specific feature support
status                     active | draining | disabled
created_at, updated_at     timestamps
```

### `storage_objects`

```text
id                         application logical object ID
user_id                    application user foreign key
logical_key                app-generated key, not a user path
original_name              user-facing name
mime_type                  allow-listed declared type
size_bytes                 expected/verified size
checksum                   optional verified digest
status                     pending | uploading | ready | degraded | failed | deleting | deleted | missing
category                   upload | report | export | backup
created_at, updated_at     timestamps
deleted_at                 nullable timestamp
```

Constraints:

- unique `(user_id, logical_key)` if logical keys are reused;
- ownership checks on every read, download, update, and delete.

### `storage_object_replicas`

```text
id                         UUID / bigint primary key
object_id                  storage object foreign key
role                       primary | fallback | backup
provider                   google_drive | aws_s3 | cloudflare_r2
connection_id              nullable user Drive connection foreign key
backend_id                 nullable system/tenant backend foreign key
provider_file_id           nullable Drive file ID
provider_object_key        nullable S3/R2 bucket key
status                     pending | uploading | ready | missing | deleting | deleted
checksum                   verified digest
size_bytes                 verified provider size
last_verified_at           timestamp nullable
created_at, updated_at     timestamps
```

Constraints:

- exactly one of `connection_id` and `backend_id` is set;
- unique `(provider, provider_file_id)` for Drive replicas;
- unique `(backend_id, provider_object_key)` for S3/R2 replicas;
- at most one active `primary` replica per logical object;
- ownership checks on every read, download, update, and delete.

### `storage_uploads`

```text
id                         upload attempt ID
object_id                  storage object foreign key
replica_id                 storage replica foreign key
provider                   google_drive | aws_s3 | cloudflare_r2
idempotency_key            client/server retry key
provider_session_uri       nullable encrypted Drive capability
provider_multipart_id      nullable S3/R2 multipart upload ID
expected_size              bytes
next_known_offset          nullable bytes
state                      initiated | active | completed | expired | aborted
attempt_count              integer
last_error_code            sanitized provider/application code
expires_at                 timestamp
created_at, updated_at     timestamps
```

Do not return the session URI from ordinary object-listing endpoints. If the URI is stored, encrypt it and expire it with the upload record.

## 5. Upload flow

### API shape

Keep the public API provider-neutral:

```text
POST   /v1/storage/uploads
PATCH  /v1/storage/uploads/{upload_id}
POST   /v1/storage/uploads/{upload_id}/complete
DELETE /v1/storage/uploads/{upload_id}
GET    /v1/storage/objects/{object_id}
GET    /v1/storage/objects/{object_id}/content
DELETE /v1/storage/objects/{object_id}
POST   /v1/storage/connections/google-drive
DELETE /v1/storage/connections/google-drive
```

The API should not expose `drive/v3/files` semantics to the rest of the product. The Google Drive adapter implements those details behind the storage interface.

### Initiation

1. Authenticate the application session.
2. Validate an allow-list of MIME types, maximum size, category, and applicable user/provider quota.
3. Generate the logical object ID, logical key, and provider-safe object name/key on the server.
4. Ask the provider router to select Drive or an S3/R2 backend based on file policy, connection state, provider health, and fallback rules.
5. Insert the object, replica, and upload rows with `pending`/`initiated` status.
6. Start the selected provider upload: Drive resumable upload for Drive, or multipart/presigned upload for S3/R2.
7. Store the provider session or multipart state encrypted with an expiry timestamp.
8. Return only the application upload ID and the minimum data the client needs to send chunks or parts.

The router must select one provider before data transfer begins. A Drive upload session cannot be converted to an S3/R2 multipart upload after a partial transfer. If Drive fails before or during an upload and policy permits failover, retain the same logical object ID, create a new provider-specific upload attempt, and require the client to replay the remaining bytes or restart the upload.

### Failover and replication flow

For **failover**:

1. Retry Drive only for bounded, documented transient failures.
2. If the failure is eligible for fallback, mark the Drive attempt `aborted` or `expired` and retain the logical object/upload ID.
3. Create a new S3/R2 replica and return a new provider-specific upload plan.
4. Verify the S3/R2 object with `HEAD` and checksum/size checks before marking it `ready`.

For **replication**:

1. Mark the Drive primary `ready` only after Drive confirms the final file metadata.
2. Enqueue a copy job using a bounded worker; do not hold the user request open.
3. Stream the Drive object to S3/R2 or use a provider-side copy where supported.
4. Verify size and checksum, then mark the backup replica `ready`.
5. Mark the logical object `degraded` and alert/retry if the backup is not ready within policy.

Replication must be idempotent and must not create a new logical object when a copy job is retried.

### Drive upload mode

There are two viable modes:

#### Mode A — backend-proxied chunks (safer default)

The browser sends chunks to our API. The API validates the application session and forwards each chunk to the stored Drive resumable session URI.

Advantages:

- the Drive session URI never leaves the backend;
- the backend can enforce user, object, size, MIME, rate, and state checks on every chunk;
- the client never receives a provider capability.

Costs:

- application bandwidth and request concurrency are consumed;
- large uploads occupy backend connections for longer;
- infrastructure must support streaming without buffering whole files.

#### Mode B — direct browser-to-Drive session

The backend starts the Drive session and returns the session URI to the authorized browser. The browser sends `PUT` requests directly to that URI.

Advantages:

- lower backend bandwidth and better large-upload scalability;
- the browser can show direct progress.

Costs:

- the session URI becomes a sensitive upload capability;
- application-level authorization and rate limits are enforced only at initiation unless the backend wraps the session;
- CORS/client behavior and retry handling must be tested across browsers;
- the provider session is not the same documented contract as an S3 presigned URL with an application-selected short expiry and policy conditions.

**Recommendation:** start with Mode A for private reports and sensitive files. Add Mode B only after a threat review and load test. If Mode B is used, bind the application upload ID to the session, give the URI only to the intended authenticated client over TLS, never log it, cap the file at the application layer before initiation, and expire/abort the application upload when the client abandons it.

### S3/R2 upload mode

For AWS S3 or Cloudflare R2, prefer direct browser-to-storage multipart uploads using short-lived, provider-generated presigned URLs. The backend must generate the bucket key from the logical object ID and never accept a client-proposed path.

The backend must:

- bind every presigned operation to one logical object, upload attempt, and part number;
- constrain content type where the provider supports signing it;
- keep the bucket private and treat every presigned URL as a bearer capability;
- verify the completed object with `HEAD`, expected size, content type, and checksum before `ready`;
- use idempotency keys for initiation and completion;
- abort abandoned multipart uploads and reconcile orphaned objects;
- use only the common S3 API subset unless the selected backend capability explicitly supports more.

Do not treat an S3/R2 event notification as the database source of truth. Events can trigger asynchronous processing, but the explicit completion path, provider `HEAD`, and periodic reconciliation determine object readiness.

### Chunking and resumption

Drive's documented resumable protocol is:

1. `POST` the upload initiation request and save the `Location` session URI.
2. Upload data with `PUT`.
3. For chunked mode, use chunk sizes that are multiples of 256 KiB except for the final chunk.
4. Send `Content-Length` and `Content-Range` for each chunk.
5. On `308 Resume Incomplete`, inspect the returned `Range` and begin at the next byte. Do not assume all bytes in the previous request were accepted.
6. On a network interruption or 5xx, query status with an empty `PUT` and `Content-Range: bytes */<total>`.
7. On `404`, treat the session as expired and restart with a new session. Drive documents that resumable sessions expire after one week of inactivity.
8. On a successful final response (`200` or `201`), verify the returned file ID and metadata before marking the object `ready`.

Use a bounded retry policy with jitter. Retry transient network errors, 5xx responses, and documented rate-limit responses; do not blindly retry validation, authorization, quota, or invalid-file errors.

### Idempotency

The client may retry after it uploads a chunk but before receiving a response. The server must make retries safe:

- use one stable upload ID per selected file;
- use an idempotency key for initiation and completion;
- do not create a new Drive file on every client retry;
- persist the last known offset and query Drive when uncertain;
- allow exactly one completion transition from `uploading` to `ready`;
- run a cleanup job for abandoned `pending` objects and orphaned Drive files.

## 6. Download flow

### Private application download

`GET /v1/storage/objects/{id}/content` should:

1. authenticate the application user;
2. load the object row and verify `object.user_id == session.user_id`;
3. select a `ready` replica according to the object's provider and fallback policy;
4. for Drive, refresh the Google access token server-side and call `files.get` with `alt=media` and the stored file ID;
5. for S3/R2, stream `GetObject` through the backend or return a short-lived presigned GET URL according to the file's privacy policy;
6. forward safe content headers and support HTTP range requests where the backend/client requires them;
7. translate provider errors into stable application errors without leaking Google token/provider payloads.

Drive's browser `webContentLink` and S3/R2 presigned URLs can be offered only when the user's privacy policy permits them. They are bearer capabilities, not replacements for our authorization check, and they should not be stored as canonical object URLs.

### Public or externally shareable downloads

Do not grant `anyone` permissions merely to imitate a public object URL. A Drive permission is an ACL change on the user's file, can be copied/shared by recipients, and may conflict with Workspace policies. For public assets, use a company-controlled object-storage/CDN path instead.

## 7. Deletion, user edits, and synchronization

Users can act in Drive outside the application. The application must expect:

- a user renaming a file;
- a user moving the folder or file;
- a user sharing a file;
- a user trashing or permanently deleting a file;
- an administrator blocking the app;
- the user revoking OAuth access;
- the folder being deleted and later recreated with the same name.
- a fallback or backup object being deleted, expired, or made unavailable by the backend provider.

Store a Drive changes page token per connection. On a scheduled worker:

1. call `changes.list` from the saved token;
2. process every page in order;
3. update local metadata for known file IDs;
4. mark removed files `missing` rather than silently recreating them;
5. save `newStartPageToken` only after processing the page successfully;
6. use a periodic full reconciliation of the app folder as a backstop.

For S3/R2 replicas, periodically list the configured prefix and compare it with `storage_object_replicas`. Mark missing provider objects explicitly, clean up orphaned objects only after a grace period, and never recreate a deleted Drive object solely because a same-named object exists in a bucket.

Drive push notifications are hints that changes are available, not complete events: the notification body is empty and the application must call the changes feed or `files.get`. Notification channels expire and must be renewed, so do not make a webhook the only consistency mechanism.

### Disconnect policy

When a user disconnects Google Drive:

- stop creating new provider requests immediately;
- revoke the stored refresh token if the product's policy requires revocation;
- mark the connection `disconnected`;
- retain only the minimum metadata needed for support, billing, audit, or a user-requested migration;
- tell the user whether their Drive files remain in their Drive or whether the app will delete them;
- tell the user whether S3/R2 fallback or backup copies remain available, and under which retention policy;
- if deletion is requested, queue deletion across all replicas and report failures rather than claiming success before each provider confirms it.

If a refresh returns `invalid_grant`, transition to `reauth_required`, stop background work, and ask the user to reconnect. Do not retry an invalid refresh token forever.

## 8. Error translation

| Provider signal | Application behavior |
|---|---|
| `401` / `authError` | Refresh once; if refresh fails, mark `reauth_required`. |
| `403 storageQuotaExceeded` | Mark upload blocked by user quota; explain that the user must free Drive space or add capacity. |
| `403 rateLimitExceeded` / `userRateLimitExceeded` | Exponential backoff with bounded retries; queue rather than failing every client request. |
| `403 domainPolicy` | Explain that the user's Workspace administrator blocks the app; provide reconnect/support guidance. |
| `403 insufficientFilePermissions` | Mark the object unavailable and require user/admin permission recovery. |
| `404 notFound` | Re-fetch metadata once; if still missing, mark `missing` and surface a recoverable state. |
| `404` on upload session | Restart the resumable session, not the logical object. |
| `429` | Backoff and retry within a deadline. |
| `500–504` | Resume/query upload state or retry idempotently. |
| S3/R2 `403` / `SignatureDoesNotMatch` | Do not retry indefinitely; refresh the signing/configuration path and mark the provider degraded if the bounded retry budget is exhausted. |
| Provider unavailable after retry budget | If policy permits, create a new provider-specific attempt for the same logical object; never switch providers inside an existing session. |
| invalid MIME, size, or ownership | Reject without retrying. |

Log a stable internal error code, object ID, replica/backend ID, provider object ID where safe, retry count, and request correlation ID. Never log access keys, refresh tokens, session URIs, presigned URLs, authorization codes, or full provider error payloads if they could contain sensitive data.

## 9. Provider abstraction

Define an internal interface such as:

```text
StorageProvider.connect(context, authorization) -> connection
StorageProvider.initiate_upload(connection, object_metadata) -> upload_session
StorageProvider.upload_chunk(upload_session, bytes, content_range) -> progress
StorageProvider.complete_upload(upload_session) -> provider_object
StorageProvider.get_download_stream(connection, provider_object) -> stream
StorageProvider.delete(connection, provider_object) -> result
StorageProvider.reconcile(connection, cursor) -> changes + new_cursor
StorageProvider.head(provider_object) -> verified_metadata
StorageProvider.capabilities() -> capability_set
```

The rest of the application should depend on logical object IDs and application policies, not on Google Drive file IDs, folder paths, bucket keys, Google MIME quirks, S3 ETags, or provider error names. This makes fallback, replication, and later provider migration adapter/data-migration work instead of an application-wide rewrite.
