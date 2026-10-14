# Risk register and mitigation plan

The main risk is not whether a single file can be uploaded. Google Drive supports that. The main risk is allowing a user-controlled productivity product to behave like a company-controlled object store without adopting the reliability, privacy, and lifecycle controls that object storage normally provides.

## Risk matrix

| Risk | Severity | What can happen | Mitigation | Proof/test |
|---|---:|---|---|---|
| Broad OAuth scope | Critical | The app can see or modify unrelated Drive data; users distrust it; verification/security-assessment work expands. | Start with `drive.file` or `drive.appdata`; request in context; document exact use. | Inspect consent screen and verify the app cannot list unrelated files. |
| Refresh-token compromise | Critical | An attacker can access the user's app-authorized Drive files while the grant remains valid. | Encrypt tokens with KMS/envelope encryption; restrict DB access; rotate keys; redact logs; alert on abnormal use. | Secret-scanning, access-review, token-revocation drill. |
| Broken OAuth callback | High | Authorization code interception, account mix-up, or CSRF. | Random server-side `state`; exact redirect URI; HTTPS in production; process then redirect to a clean URL. | Automated state mismatch and account-switch tests. |
| Consent wording is incomplete | Critical | Users do not understand where files go, who can access them, how quota is used, or what disconnect means. | Add prominent in-product notice and privacy-policy section; obtain privacy/legal review. | Review copy against actual API behavior and data flows. |
| User's Drive is full | High | Uploads fail with `storageQuotaExceeded`; report generation becomes unavailable. | Detect and translate the error; show clear remediation; offer another provider or export; never retry forever. | Fill a test account near quota and test user messaging. |
| Fallback semantics are ambiguous | High | The team assumes S3/R2 fallback protects existing Drive files, but it only handles new uploads. | Define failover and replication separately; record provider/replica state per logical object; expose degraded and backup-pending states. | Delete a Drive-only object and verify the product does not claim it can restore it without a replica. |
| Mid-upload provider switch | High | A partial Drive upload is incorrectly treated as resumable in S3/R2, producing corruption or duplicate data. | Select the provider before transfer; create a new provider-specific attempt and replay bytes when failover is required. | Fail Drive after several chunks and verify the client receives a new upload plan. |
| Hidden company-controlled copy | Critical | Files expected to be user-owned in Drive are also stored in S3/R2 without clear consent, retention, or deletion policy. | Disclose fallback/replication, regions, access, retention, and disconnect behavior; obtain privacy/legal review. | Review consent text and run a disconnect/delete test with a ready replica. |
| Workspace admin blocks the app | High | Company, school, or managed accounts cannot connect or access Drive. | Treat connection as optional; show `domainPolicy` guidance; support another storage method. | Test with a managed account or documented policy simulation. |
| User revokes access | High | Background jobs fail with expired/invalid refresh credentials. | Mark `reauth_required`; stop jobs; preserve pending state; ask for reconnection. | Revoke grant in Google account settings and run a queued job. |
| User deletes or moves app folder | High | Stored folder/file IDs become unavailable; same-name recreation is mistaken for the original. | Store IDs; detect 404; reconcile; ask before recreating; do not assume names identify folders. | Delete/move/rename files from Drive UI and run reconciliation. |
| User changes or shares a file | High | App data may be exposed or no longer match the catalog. | Default to private; do not add `anyone`; record provider state; explain user-controlled sharing. | Share a file with another test account and verify app behavior. |
| Drive URL treated as a signed URL | High | A copied link may work longer or differently than intended, or an app returns a URL without rechecking ownership. | Use an application download endpoint; authorize every request; store file ID, not canonical URL. | Request download as another user and after permission removal. |
| Resumable session URI leaks | High | A leaked session capability may allow unintended writes to an in-progress upload. | Proxy chunks in MVP; if direct upload is used, treat URI as secret, never log it, expire records, bind to a single upload, and cap initiation. | Log review, browser devtools review, abandoned-session test. |
| Session expires | Medium | A paused upload cannot resume. | Store expiry; query status; restart the same logical upload with a new provider session. | Pause longer than the configured session lifetime in a controlled test. |
| Incorrect chunk offsets | High | Corrupted or duplicated data after network failure. | Use `308`/`Range` state from Drive; do not assume last request was fully received. | Inject connection loss after every chunk boundary. |
| Duplicate files after retries | High | One user action creates multiple Drive files and duplicate DB rows. | Idempotency key, one logical object ID, unique constraints, and orphan cleanup. | Retry initiation/completion after dropped responses. |
| Quota/rate throttling | High | Bursty users or background sync cause 403/429 failures and cascading retries. | Queue work, cap concurrency, use jittered exponential backoff, batch metadata operations, and monitor quota. | Load test with realistic concurrent uploads and a forced 429 response. |
| Per-project egress limit | High | Downloads or exports stop at the project-level daily limit. | Enforce download budgets; cache only where allowed; monitor egress; keep large delivery on object storage. | Simulate budget exhaustion in a quota-aware integration test. |
| S3/R2 provider feature drift | Medium | AWS-only features such as Object Lock, versioning, tagging, or notifications are assumed to work on R2. | Use a common S3 subset; expose capability flags; contract-test every enabled feature per provider. | Run the same adapter contract suite against AWS S3 and R2. |
| Fallback credentials unavailable | High | A Drive outage occurs while the S3/R2 backend is misconfigured or its credentials are expired. | Store credentials through a secret manager, monitor health, rotate them, and fail closed rather than exposing a public bucket. | Disable/rotate credentials in staging and verify actionable degraded behavior. |
| Too many small files | Medium | Metadata/list operations become slow and quota-expensive; Drive becomes a poor database. | Store metadata in the application DB; avoid one tiny Drive file per row; batch/reconcile incrementally. | Test N files and measure list/read quotas and job duration. |
| Too many folder children/deep hierarchy | Medium | Folder/item/depth limits are reached. | Keep a shallow bounded layout; partition by category/time only when needed. | Generate near-limit synthetic folder layouts in staging. |
| Google Workspace file conversion | Medium | A Google Doc/Sheet is not a normal binary blob and cannot be downloaded with `alt=media`. | Store binary reports as PDF/CSV/etc.; use export for native Workspace documents; validate MIME type. | Upload/download each supported MIME type. |
| Malicious or oversized upload | Critical | Malware, decompression bombs, abusive content, cost/quota exhaustion, or slow processing. | Allow-list MIME/size; rate-limit; scan after upload if required; never trust client MIME; isolate processing; apply retention. | Upload mismatched MIME, huge declared size, and archive test files. |
| User-owned retention mismatch | Critical | Company promises retention or deletion that cannot be enforced if the user controls Drive. | Make ownership and retention explicit; do not promise immutable retention; use company storage for regulated/legal-hold content. | Legal/privacy review of retention and deletion language. |
| No independent backup | High | A user's deletion or account loss removes the only copy. | Label Drive as backup/export, not the sole durable copy for critical data; add another provider for business-critical data. | Restore test from a second copy. |
| Service-account misconception | High | Files are created under an unsuitable service account, lack storage quota, or disappear with the account. | Use user OAuth for user-owned storage; use shared drives/organizational storage only with a deliberate design. | Verify ownership and quota on test credentials. |
| Provider lock-in | Medium | Drive file IDs, scopes, ACLs, and provider errors leak through the application. | Provider-neutral interface, logical object IDs, migration metadata, and adapter-specific error mapping. | Contract test the storage interface with a fake provider. |
| Webhook assumed to be durable | Medium | Missed/empty notifications cause stale metadata. | Use notifications only as a wake-up signal; poll changes feed and run periodic reconciliation. | Drop webhook requests and verify later reconciliation repairs state. |

## Security baseline

### Tokens and credentials

- Keep OAuth client secrets outside the source tree and public static assets.
- Encrypt refresh tokens at rest and limit decryption to a small storage worker/API role.
- Keep access tokens out of application logs, analytics, tracing, support tickets, and browser local storage.
- Keep AWS/R2 access keys in a secret manager or KMS-backed reference; never return them to clients.
- Treat presigned URLs and multipart session identifiers as bearer capabilities; keep them short-lived and out of logs.
- Use secret rotation and a revocation runbook.
- Separate production OAuth credentials from development/test credentials.

### Tenant isolation

Every operation must derive the application user from the authenticated session, then load the storage connection and object through that user. Never accept `user_id`, folder ID, or Drive file ID as an authority claim from the browser.

Before every download, mutation, and delete:

```text
session.user_id
  == object.user_id
  == storage_connection.user_id
```

Also check that the provider file ID is linked to that same connection. This prevents a valid Drive token from being used to access a different application's row through an IDOR/BOLA bug.

### File handling

- Treat filenames as display data; generate provider names server-side.
- Do not create public/`anyone` permissions by default.
- Validate size before initiation and verify actual size after upload.
- Verify S3/R2 object size, content type, and checksum before marking a replica ready.
- Validate content after upload if the product processes or displays it.
- Keep file bytes out of ordinary request logs and error reports.
- Separate upload processing from the request-serving process when scanning/transcoding is expensive.

## Privacy and policy checklist

Before production:

- [ ] Explain that the app creates/manages files in the user's Drive.
- [ ] Explain whether the folder is visible and how it is named.
- [ ] Explain that files consume the user's Drive quota.
- [ ] Explain whether the company can read the file content, and for which user-facing purpose.
- [ ] Explain when files may be stored in company-managed S3/R2 fallback or backup storage.
- [ ] Disclose the S3/R2 provider, storage region, encryption model, and access implications where required.
- [ ] Explain whether employees, processors, scanners, or AI systems can access the content.
- [ ] Explain sharing, retention, deletion, backup, and disconnect behavior.
- [ ] Publish a privacy policy with the exact Google data use.
- [ ] Complete the relevant Google OAuth verification/brand process for the selected scopes.
- [ ] Check enterprise administrator restrictions and support managed accounts.
- [ ] Obtain legal/privacy signoff for regulated or sensitive files.

## Go/no-go criteria

### Go for a limited pilot if

- the feature is optional user-owned backup/export;
- the narrow scope works for the folder/file workflow;
- the product can tolerate Drive quota and admin-policy failures;
- the company has a token-security and revocation process;
- private downloads go through an application authorization boundary;
- upload/reconciliation tests pass;
- fallback and replication semantics are documented and tested;
- every ready replica can be deleted or retained according to the stated policy.

### Do not use Drive as the primary storage if

- the company must guarantee availability independent of the user's Google account;
- files are subject to immutable retention, legal hold, or strict company deletion control;
- the product requires public URLs, CDN delivery, or many concurrent downloads;
- storage traffic is expected to be high or bursty;
- the product cannot support Google OAuth verification and privacy obligations;
- a user losing Drive access would cause unacceptable data loss.
