# Storage decision: Drive first, outage-only fallback, no copies

**Updated:** 2026-10-14

**Status:** The Drive-first, S3-compatible fallback, no-copies requirement and Go language are confirmed by the user. The operational rules below are the conservative implementation plan for those requirements.

This document supersedes earlier recommendations for automatic replication, fallback on full/revoked Drive accounts, and S3-first workload classes. Those recommendations are not part of the current project.

## 1. Confirmed product contract

1. Use the user's connected Google Drive for normal new uploads.
2. Use one configured S3-compatible backend, such as AWS S3 or Cloudflare R2, only when Drive is not responding/available.
3. Keep one application-managed file location per logical object. Do not mirror files, maintain backup copies, dual-write, or stage every upload in S3.
4. Implement the backend in Go; keep plans and decisions in Markdown.

R2 is Cloudflare's object-storage service. Durable Objects are a separate compute/coordination service and are not required for this plan.

## 2. Meaning of "Drive isn't responding"

The plan treats this as service/transport unavailability, not every failed operation. Classify the error reason and operation phase, not just its HTTP status.

| Observed condition | New-upload behavior | S3 fallback? |
|---|---|---|
| Drive responds successfully | Upload to Drive | No |
| Drive read-only preflight times out or has transient network/DNS/connectivity failures after bounded retry | Choose the configured S3 backend before a provider write | Yes, if consent and account gates pass |
| Drive read-only preflight repeatedly returns 500/502/503/504 within the retry budget | Treat as service unavailability | Yes, before a provider write |
| Recent, scoped Drive-unavailability circuit is open | Route eligible new uploads to fallback during a short cooldown | Yes, before a provider write |
| Drive storage is full or an upload/daily quota is exhausted | Ask the user to free space or wait for quota reset | No |
| Drive returns 429 or a 403 rate-limit reason | Honor retry/backoff and surface throttling if the budget expires | No |
| Consent declined, no connection, disconnect, revoked grant, or `invalid_grant` | Connect/reconnect flow; block new storage uploads | No |
| File permission error, Workspace admin policy, missing folder/file | Explain and resolve the specific problem | No |
| Invalid MIME, oversize file, tenant authorization failure | Reject the request | No |
| Client cancellation, our DB failure, invalid endpoint, or TLS certificate validation failure | Cancel/fail safely; investigate local/configuration errors | No |
| Write may already have succeeded, but response was lost | Keep the upload on its selected provider and reconcile | No automatic provider switch |

Account existence, application authorization, known Drive connection state, known quota/policy blocks, fallback consent, and fallback budget are checked before routing. A circuit breaker must not override these gates. A token-service outage alone is not evidence of Drive unavailability in this MVP.

## 3. Single-copy behavior and unavoidable trade-offs

The invariant is one intended application storage location, not control over Google/AWS/Cloudflare's internal redundancy or copies independently made by the user. Metadata and bounded transient transport buffers are not retained backup files. Do not persist uploaded payloads in local spools, queues, logs, or a second provider.

### New upload

- Persist an idempotent logical upload and perform a read-only Drive preflight, such as reading the app-managed folder metadata with minimal fields.
- Choose Drive on success. Choose fallback only for the eligible availability failures above.
- Persist that choice before the first provider mutation, including session initiation. The client cannot request S3 directly.
- Once a provider mutation may have been sent, pin the upload to that provider in the MVP. Do not use `try Drive write; on any error write S3`.

### Interrupted or ambiguous write

A timeout can mean "Drive stored the file but the acknowledgement was lost." Writing the bytes to S3 immediately could create two files.

- Record `outcome_unknown` when the write result cannot be proved, and query the existing provider session/file instead of opening another location.
- Resume on the same provider using provider-confirmed progress. If it completed, verify and publish that file.
- If a session expires, establish that no committed object or capable in-flight writer remains before creating another attempt. An unexplained file `404` can also mean lost access; it is not sufficient proof of deletion.
- If certainty is unavailable, leave the upload pending and expose a retryable status. Do not trade the no-copies requirement for instant failover.
- A replacement upload can start only after the prior attempt is terminal and cleanup is confirmed. Route it afresh under this policy and resend the full file; Drive byte offsets cannot be transferred to S3.

This deliberately sacrifices immediate failover for interrupted writes. We cannot promise both zero duplicate writes and uninterrupted switching after an ambiguous remote write.

### Existing file and recovery

- Reads and deletes use the object's recorded location, not today's preferred provider.
- A Drive-only file is temporarily unavailable while Drive is down. S3 has no copy to serve and cannot restore a deleted Drive file.
- A file uploaded to S3 during an outage stays there and continues to be read/deleted there after Drive recovers.
- Once Drive health recovers, **new uploads return to Drive**. Finish existing S3 sessions on S3.
- No automatic move-back, copy-back, migration, or content synchronization. A future move would require a separately approved policy because a safe copy-verify-delete move temporarily creates two locations.

The stay-in-place rule for existing fallback files is a proposed MVP consequence of "no copies," not a claim that the user chose permanent S3 migration.

## 4. Operational defaults to validate

These are initial engineering values, not provider guarantees or measured production targets:

- Drive preflight: 3-second per-call timeout, at most two calls, jittered delay, maximum 8-second overall routing budget.
- Circuit: scoped to the Drive connection and runtime egress context; open after two consecutive eligible availability failures, cooldown 30 seconds, allow one half-open read-only probe. A successful probe closes it immediately for subsequent uploads.
- Do not classify a slow client upload as a Drive outage. Chunk/stream deadlines are separate from metadata preflight deadlines.
- Avoid nested retry multiplication: the coordinator owns the total budget and constrains SDK retries.
- Persist/cross-check circuit evidence for multi-instance deployments; rate-limit probes and do not open a global outage circuit because one user is rate-limited.
- Use one fallback backend per deployment initially. If it is also unavailable or its budget is exhausted, report failure/pending; do not cascade writes across buckets or providers.

## 5. Privacy and scope boundaries

Disclose the fallback destination, regions, operator access, encryption, retention, costs, and deletion/disconnect behavior before enabling outage uploads. No file can be advertised as "only ever on your Drive" when fallback is enabled.

Account creation may remain usable without Drive consent, but new storage uploads require a connected Drive account. Proposed disconnect behavior: stop new Drive requests and new uploads, leave user-owned Drive files untouched, and keep already-created fallback files manageable under the application's published retention/deletion policy. No new S3 writes are justified by disconnect itself.

This product does not provide an independent backup, seamless cross-provider read failover, immutable retention, public/CDN storage, or a recovery guarantee after loss of the only file. If those become requirements, revisit the product contract rather than adding hidden copies.
