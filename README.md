# Google Drive-primary storage with S3-compatible fallback

**Research date:** 2026-10-14  
**Question:** Can the company use each customer's Google Drive as the primary storage layer for that customer's uploaded files, reports, and backups, while retaining an S3-compatible fallback?
**Recommendation:** Yes for a bounded user-owned, Drive-visible feature with policy-controlled AWS S3 or Cloudflare R2 fallback; no as a general replacement for company-controlled object storage.

## Executive decision

Google Drive can support the core mechanics we need:

- user authorization through Google OAuth;
- files created in the user's Drive and owned by that user;
- large and interrupted uploads through Drive resumable upload sessions;
- chunked uploads using `Content-Range`;
- private downloads through the Drive API;
- file IDs and metadata stored in our database;
- change detection through the Drive changes feed and optional webhooks.

The application should also support a provider-neutral fallback path for files that cannot be stored in Drive. AWS S3 and Cloudflare R2 are the first compatible targets. The fallback must be selected before an upload starts; a Drive resumable session cannot be converted into an S3 multipart upload halfway through a transfer.

Terminology: **Cloudflare R2** is the object-storage service. **Cloudflare Durable Objects** are a separate stateful compute/coordination feature and are not a replacement for blob storage.

The important distinction is that this is **Drive-backed user storage**, not a normal object-storage bucket. The user's quota, Drive policies, Google OAuth grant, Google API quotas, and Google Workspace administrator rules become part of our product's availability.

### Recommended product boundary

Use Drive as the **primary connected-storage destination** for:

- user-specific backups;
- reports the user wants to own and see in their Drive;
- low-to-moderate volume documents and attachments;
- export, portability, or disaster-recovery copies.

Keep a provider-neutral storage interface so the company can support S3-compatible storage without exposing provider details to the rest of the application. Use object storage as the default for:

- high-volume uploads or downloads;
- public/static assets and CDN delivery;
- media processing pipelines;
- strict company-controlled retention and deletion;
- predictable throughput and signed URL semantics;
- files that must remain available after a user disconnects or loses Drive capacity.

For this project, "fallback" has two distinct meanings:

- **Failover:** route a new upload to S3/R2 when Drive is unavailable, full, revoked, or blocked. This does not protect an existing Drive-only object if the user later deletes it.
- **Replication:** keep a second, verified S3/R2 copy of selected Drive objects. This protects existing data but creates a company-controlled copy and must be disclosed in consent, privacy, retention, and disconnect policies.

Use failover for ordinary files and replication for business-critical files. If the company needs guaranteed availability, immutable retention, or legal hold, make S3/R2 primary and treat Drive as an export destination instead.

## The proposed architecture in one page

1. **Application account:** Authenticate the application user separately from Drive authorization. Google sign-in identifies the user; Drive authorization is a connected-storage capability.
2. **Consent:** Ask for Drive access when the user enables backup/storage, using the narrowest scope that satisfies the feature. Prefer `drive.file` for files created or opened by the app. Avoid the full `drive` scope unless there is a reviewed requirement to read or manage the user's entire Drive.
3. **Dedicated folder:** Create one visible application folder in the user's My Drive, then store files beneath it. Store the folder ID, not a path.
4. **Backend token broker:** Exchange the authorization code on the backend, securely store the refresh token, refresh access tokens server-side, and never put refresh tokens or client secrets in browser code.
5. **Upload coordinator:** The backend authorizes the upload, selects Drive or an S3-compatible fallback before transfer, mints the provider-specific upload session, stores upload state, and either proxies chunks or returns a carefully protected upload capability.
6. **Database catalog:** Our database remains the source of truth for application ownership, logical object IDs, status, names, MIME types, sizes, hashes, and provider replicas. Drive, S3, or R2 is the binary provider, not the application's relational index.
7. **Private download endpoint:** Authorize the application user against our database, then stream from the selected ready replica. For Drive, refresh the user's Google token and use `files.get(..., alt=media)`; for S3/R2, use a backend stream or short-lived presigned URL. Do not treat a provider URL as an application-level authorization boundary.
8. **Provider routing:** Select Drive or an S3-compatible backend before upload based on file policy, provider health, user connection state, and configured fallback rules. Never silently change the provider for an in-progress upload.
9. **Reconciliation:** Handle user-side rename, move, deletion, quota exhaustion, token revocation, and administrator policy changes. Use the Drive changes feed plus S3/R2 object checks and a periodic reconciliation job as a safety net.

## Feasibility summary

| Requirement | Drive API fit | Design note |
|---|---|---|
| OAuth at account creation | Possible, but should not be mandatory for basic account creation | Separate identity from the optional Drive connection; request storage consent in context. |
| User-owned files | Good | Files created with the user's OAuth credentials are owned by that user and consume their storage quota. |
| App-private files | Possible | `appDataFolder` is hidden and app-only, but files cannot be shared, moved between spaces, or trashed there. It is better for app configuration than user-visible reports. |
| Visible application folder | Good | Use a dedicated folder and `drive.file`; do not scan the whole Drive. |
| Large upload | Good with a coordinator | Use Drive resumable uploads; chunks must be multiples of 256 KiB except the final chunk. |
| Resume after network failure | Good | Query the session status, use the returned `Range`, and never assume the last chunk was fully received. |
| S3-style presigned upload | Not equivalent | Drive has resumable session URLs, but not the same documented app-controlled signed-URL policy model as S3. Treat session URLs as sensitive capabilities. |
| S3-style presigned download | Weak fit | Use an authenticated application download proxy for private data. `webContentLink` is a Drive-generated browser link subject to Drive permissions. |
| Stable path | Poor fit | Drive file IDs are stable for the life of a file; names and folder relationships can change. Store IDs, not paths. |
| Public/CDN delivery | Poor fit | Sharing changes Drive ACLs and can expose user-owned data. Use object storage/CDN for this. |
| New-upload failover | Good with explicit routing | Choose the provider before upload; a Drive session cannot be resumed as an S3 multipart upload. |
| Verified secondary copy | Possible, but policy-sensitive | Store replica state separately; verify size/checksum before reporting the backup as ready. |
| Background processing | Possible, but quota-sensitive | Queue work, limit concurrency, retry with bounded exponential backoff, and reconcile. |
| Strict retention/deletion | Possible but policy-dependent | User or administrator actions can affect access; define disconnect and deletion behavior explicitly. |

## Bottom line

Build a small proof of concept before committing the company to Drive as a production storage service. The proof of concept should demonstrate OAuth verification, folder creation, a 1–10 GB resumable upload, interrupted upload recovery, private download with HTTP ranges, user-side deletion detection, token revocation recovery, and quota/error handling.

If those tests pass and the product is genuinely user-owned backup/export, Drive is reasonable. If the requirement is "our application needs an object store," use S3/R2 as the company-controlled primary and keep Drive as an explicit export or user-owned replica. Do not make a hidden S3 copy without documenting the ownership, access, retention, and deletion consequences.

## Files in this research pack

- [`docs/architecture.md`](./docs/architecture.md) — proposed components, data model, flows, and API shape.
- [`docs/risk-register.md`](./docs/risk-register.md) — failure modes, security/privacy risks, mitigations, and test ideas.
- [`docs/implementation-plan.md`](./docs/implementation-plan.md) — phased MVP plan, acceptance checks, consent copy, and migration seam.
- [`docs/references.md`](./docs/references.md) — official Google sources and developer/community evidence.

## Important assumptions and open decisions

This recommendation assumes:

- each user explicitly connects a Google account and consents to app-managed files;
- the application stores binary files, not arbitrary existing Drive content;
- files are private unless the user deliberately shares them;
- the company can tolerate a provider-specific failure mode and a user-controlled quota;
- the application has a database for metadata and job state.

Before implementation, the product owner must decide:

1. Is Drive required, optional, or one of several storage providers?
2. Should the folder be visible to users or hidden in `appDataFolder`?
3. What happens to files when a user disconnects Google Drive or deletes the app folder?
4. What maximum file size, daily upload volume, download volume, and retention period are supported?
5. Must files be available when a user has no Drive space or an administrator blocks third-party Drive apps?
6. Are any files regulated, sensitive, or subject to a legal hold that makes user-owned storage inappropriate?
7. Does fallback mean failed new uploads only, or should critical Drive objects receive a verified S3/R2 replica?
8. If a file is stored in S3/R2 after Drive fallback, when should it be copied back to Drive, if ever?

This document is technical research, not legal or privacy counsel. Have the company review the consent language, privacy policy, data-processing terms, retention promises, and Google verification requirements before production release.
