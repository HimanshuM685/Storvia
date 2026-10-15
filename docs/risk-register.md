# Risk register and mitigation plan

**Updated:** 2026-10-14

This register follows the confirmed rule: Drive is normal storage, S3-compatible storage is an outage-only fallback, and Storvia does not maintain duplicate file copies. Provider-internal durability or redundancy is outside Storvia's application-managed location model.

## Risk matrix

| Risk | Severity | What can happen | Mitigation | Proof/test |
|---|---:|---|---|---|
| Broad OAuth scope | Critical | The app can see or modify unrelated Drive data; verification/security work expands. | Start with `drive.file`; request access in context; document exact use. | Consent review and unrelated-file isolation test. |
| Refresh-token compromise | Critical | An attacker can access app-authorized Drive files while the grant is valid. | Encrypt refresh tokens; restrict decrypt access; rotate keys; redact logs; maintain revocation runbook. | Secret scan, access review, token-revocation drill. |
| Broken OAuth callback | High | Authorization code interception, account mix-up, or CSRF. | Random server-side state, exact redirect URI, HTTPS, session binding, clean redirect. | State mismatch and account-switch tests. |
| Incomplete fallback consent | Critical | Users believe data is only in Drive while an outage upload goes to company-managed S3/R2. | Disclose fallback provider, region, access, retention, deletion, and outage trigger. | Privacy copy review and user acceptance test. |
| Development admin panel exposed outside localhost | High | A test-only panel can mint a session for its configured development identity. | Enable only in `development`, require non-empty fixed test IDs, use a separate callback, and keep production config disabled. | Config validation test and production-config check. |
| Drive unavailable before write | High | New uploads fail unless routing detects the outage correctly. | Bounded read-only preflight, explicit availability classifier, scoped circuit breaker, configured S3 backend. | Inject DNS/connect timeout and 5xx during preflight. |
| False outage classification | Critical | Quota, auth, admin-policy, or validation failures silently send files to S3 against policy. | Classify provider reason and operation phase; fallback only for eligible availability failures. | Table-driven classifier tests for every policy row. |
| Fallback backend unavailable | High | Both locations are unavailable and uploads are lost or loop forever. | One bounded fallback attempt, clear pending/failure state, credentials health check, no provider cascade. | Disable fallback credentials/provider and observe terminal behavior. |
| Mid-upload provider switch | Critical | Partial Drive data is incorrectly resumed in S3, causing corruption or duplicate locations. | Pin attempt after mutation may start; reconcile same provider; never blind-switch. | Drop Drive response after initiation/chunks and assert no S3 mutation. |
| Ambiguous provider write | Critical | A timeout may hide a successful write; a retry creates a duplicate. | `outcome_unknown`, same-provider status query, idempotent completion, cleanup only after evidence. | Lose response after Drive/S3 commit and reconcile exact location. |
| DB commit lost after provider success | Critical | Provider contains an object but catalog says pending; a second attempt is started. | Persist attempt before mutation; idempotent completion; reconcile before replacement. | Commit DB failure after provider 2xx, then retry. |
| Duplicate logical upload | High | Browser retries initiation and creates multiple files. | Stable idempotency key, request fingerprint, unique constraints, one unresolved attempt. | Repeat requests with same and changed metadata. |
| User's Drive is full | High | Uploads fail with `storageQuotaExceeded`. | Show actionable quota state; do not use S3 fallback under current policy. | Near-quota controlled account test. |
| Rate limiting | High | Drive 403/429 retries cascade and amplify outages. | Bounded backoff/jitter, request budgets, queues, per-connection limits. | Forced 429/rate-limit integration test. |
| OAuth revoked or admin blocked | High | Background/API requests fail with `invalid_grant` or `domainPolicy`. | Mark reconnect/blocked; stop Drive work; do not treat as availability fallback. | Revoke grant and simulate managed-account policy. |
| User deletes/moves folder/file | High | Provider pointer is missing or same-name folder is mistaken for original. | Store IDs, reconcile changes, explicit recovery, no silent recreation. | Delete/move/rename and reconcile. |
| Provider URL treated as authorization | High | A copied Drive session URL or signed S3 URL grants unintended access. | Backend authorization boundary; short-lived capabilities; no URLs in logs/responses except explicit upload action. | URL leakage review and cross-tenant download test. |
| Tenant isolation failure | Critical | One user can access another user's object/provider pointer. | Derive tenant/user from session; scope every query and provider operation; never trust client IDs. | IDOR/BOLA and cross-tenant backend tests. |
| S3/R2 compatibility drift | Medium | R2 is assumed to support every AWS S3 feature. | Common API subset, capability tests, selected-vendor integration check. | Run adapter contract suite against selected provider. |
| Endpoint/credential misconfiguration | High | SSRF, public bucket exposure, signature failures, or writes to wrong tenant bucket. | Operator-only endpoint config, allow-listed schemes/hosts, private bucket, secret references, startup validation. | Malicious endpoint/configuration tests. |
| Too many small files | Medium | Drive metadata/quota behavior becomes slow or expensive. | Use Drive for logical files, not a database/chunk store; record metadata in DB. | Volume test with representative file count. |
| Malicious or oversized upload | Critical | Malware, decompression bomb, quota/cost exhaustion, or slow processing. | Allow-list type/size, rate limits, streaming limits, optional scanning, isolate processing. | Mismatched MIME, oversized declaration, archive tests. |
| No independent backup | High | The only Drive file is lost if the user deletes it or loses the account. | Explicitly accept that no application backup exists; do not promise restore/DR from S3. | Product/legal review of loss wording. |
| No copy-back guarantee | Medium | An S3 fallback file remains there after Drive recovers, surprising users or operators. | Document stay-in-place rule; new uploads return to Drive; no automatic migration. | Recovery test asserts no copy-back job or second object. |
| Webhook assumed durable | Medium | Missed notifications leave stale metadata or cleanup state. | Use notifications only as wake-up hints; poll changes/inspect exact provider object; periodic reconciliation. | Drop notifications and recover via scheduled job. |

## Error policy invariant

The same operation must not write to Drive and S3 merely because the first response was unclear. A timeout is evidence of **uncertainty**, not evidence that the provider failed before accepting the write. The coordinator must know whether mutation could have started before deciding whether fallback is allowed.

## Security baseline

### Credentials and capabilities

- Keep Google client secrets, refresh tokens, S3 access keys, and encryption keys outside source control and public assets.
- Store refresh tokens and provider credentials through a KMS/secret-manager boundary; application rows contain references/ciphertext only.
- Keep access tokens, signed URLs, Drive session URIs, multipart IDs where sensitive, authorization codes, and raw provider error bodies out of logs, traces, analytics, tickets, and browser storage.
- Use separate development, staging, and production credentials and buckets.
- Rotate secrets with an overlap/revocation runbook; fail closed on invalid configuration.

### Tenant isolation

Every operation derives identity from the authenticated application session:

```text
session.tenant_id == object.tenant_id
session.user_id   == object.user_id
provider pointer  belongs to the object's selected connection/backend
```

The browser never supplies a trusted tenant, provider, Drive file ID, bucket, key, or endpoint. Object keys and Drive names are generated server-side from opaque logical IDs and attempt generations.

### File handling

- Enforce declared and streamed size limits before and during transfer.
- Treat client MIME as untrusted metadata; sniff/scan according to product requirements.
- Keep bytes out of logs and ordinary error reports.
- Do not spool full payloads locally or in a queue as a hidden backup.
- Verify provider-confirmed size and checksum where available before `ready`.
- Use safe `Content-Disposition`, content type, range, and cache headers on downloads.

## Privacy and policy checklist

Before production:

- [ ] Explain that the app creates/manages files in the user's Drive.
- [ ] Explain the visible folder and Drive quota behavior.
- [ ] Explain that only when Drive is unavailable, eligible new uploads may be stored in company-managed S3/R2.
- [ ] Disclose provider, region, encryption, operator/processor access, retention, cost responsibility, and deletion behavior.
- [ ] Explain that Storvia does not maintain a second copy and cannot restore a Drive-only file from S3.
- [ ] Explain that an upload already committed to S3 remains there after Drive recovers, if that is the approved stay-in-place rule.
- [ ] Explain disconnect/reconnect behavior and ensure it matches the implementation.
- [ ] Publish privacy policy and Google API data-use description.
- [ ] Complete applicable Google OAuth brand/verification review.
- [ ] Obtain legal/privacy signoff for sensitive or regulated files.

## Go/no-go criteria

### Go for a limited pilot if

- Drive is the normal primary and fallback is outage-only;
- the fallback backend is configured privately and its credentials are rotatable;
- classifier tests distinguish availability from quota/auth/policy/validation failures;
- tenant isolation, idempotency, same-provider uncertain-result recovery, and delete tests pass;
- product accepts no independent application backup and the documented stay-in-place behavior;
- private downloads use an application authorization boundary;
- real controlled Drive and selected S3/R2 smoke tests pass.

### Do not use this design if

- the company must guarantee recovery after Drive-only data loss;
- every file must remain in the user's Drive;
- files require company-controlled immutable retention/legal hold;
- the product requires seamless reads during Drive outages for existing Drive files;
- storage traffic is high-volume/bursty enough that Drive should not be the normal primary;
- the team cannot operate OAuth verification, provider credentials, and reconciliation.
