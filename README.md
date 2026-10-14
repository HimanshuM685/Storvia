# Google Drive as per-user application storage

**Research date:** 2026-10-14  
**Question:** Can the company use each customer's Google Drive as the storage layer for that customer's uploaded files, reports, and backups?  
**Recommendation:** Yes for a bounded user-owned backup/archive feature; no as a general replacement for S3-style object storage.

## Executive decision

Google Drive can support the core mechanics we need:

- user authorization through Google OAuth;
- files created in the user's Drive and owned by that user;
- large and interrupted uploads through Drive resumable upload sessions;
- chunked uploads using `Content-Range`;
- private downloads through the Drive API;
- file IDs and metadata stored in our database;
- change detection through the Drive changes feed and optional webhooks.

The important distinction is that this is **Drive-backed user storage**, not a normal object-storage bucket. The user's quota, Drive policies, Google OAuth grant, Google API quotas, and Google Workspace administrator rules become part of our product's availability.

### Recommended product boundary

Use Drive as an **optional connected-storage destination** for:

- user-specific backups;
- reports the user wants to own and see in their Drive;
- low-to-moderate volume documents and attachments;
- export, portability, or disaster-recovery copies.

Keep a provider-neutral storage interface so the company can later add S3-compatible storage. Use object storage as the default for:

- high-volume uploads or downloads;
- public/static assets and CDN delivery;
- media processing pipelines;
- strict company-controlled retention and deletion;
- predictable throughput and signed URL semantics;
- files that must remain available after a user disconnects or loses Drive capacity.

## The proposed architecture in one page

1. **Application account:** Authenticate the application user separately from Drive authorization. Google sign-in identifies the user; Drive authorization is a connected-storage capability.
2. **Consent:** Ask for Drive access when the user enables backup/storage, using the narrowest scope that satisfies the feature. Prefer `drive.file` for files created or opened by the app. Avoid the full `drive` scope unless there is a reviewed requirement to read or manage the user's entire Drive.
3. **Dedicated folder:** Create one visible application folder in the user's My Drive, then store files beneath it. Store the folder ID, not a path.
4. **Backend token broker:** Exchange the authorization code on the backend, securely store the refresh token, refresh access tokens server-side, and never put refresh tokens or client secrets in browser code.
5. **Upload coordinator:** The backend authorizes the upload, mints the Drive resumable session, stores upload state, and either proxies chunks or returns the session URL to the browser as a carefully protected upload capability.
6. **Database catalog:** Our database remains the source of truth for application ownership, logical object IDs, status, names, MIME types, sizes, hashes, and Drive file IDs. Drive is the binary/metadata provider, not the application's relational index.
7. **Private download endpoint:** Authorize the application user against our database, refresh the user's Google token, and stream `files.get(..., alt=media)` from Drive. Do not treat a Drive web link as an application-level authorization boundary.
8. **Reconciliation:** Handle user-side rename, move, deletion, quota exhaustion, token revocation, and administrator policy changes. Use the Drive changes feed for synchronization and a periodic reconciliation job as a safety net.

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
| Background processing | Possible, but quota-sensitive | Queue work, limit concurrency, retry with bounded exponential backoff, and reconcile. |
| Strict retention/deletion | Possible but policy-dependent | User or administrator actions can affect access; define disconnect and deletion behavior explicitly. |

## Bottom line

Build a small proof of concept before committing the company to Drive as a production storage service. The proof of concept should demonstrate OAuth verification, folder creation, a 1–10 GB resumable upload, interrupted upload recovery, private download with HTTP ranges, user-side deletion detection, token revocation recovery, and quota/error handling.

If those tests pass and the product is genuinely user-owned backup/export, Drive is reasonable. If the requirement is "our application needs an object store," start with a provider-neutral interface and plan for S3-compatible storage instead of making Drive the hidden default.

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

This document is technical research, not legal or privacy counsel. Have the company review the consent language, privacy policy, data-processing terms, retention promises, and Google verification requirements before production release.
