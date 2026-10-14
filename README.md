# Storvia — Drive-first file storage in Go

**Updated:** 2026-10-14

**Project state:** Planning documents only. There is no Go application, module, or runnable test suite yet.

## Confirmed direction

- **Google Drive is the primary destination for new files.**
- **S3-compatible storage is an outage fallback only:** use AWS S3, Cloudflare R2, or another tested compatible backend when Drive is not responding.
- **No application-managed copies:** no dual writes, backup replicas, mirroring, or background copy-back.
- **Backend language: Go.**
- Keep requirements, design changes, checks, and next steps in Markdown for future sessions.

The detailed [storage policy](docs/storage-policy.md) takes precedence over earlier recommendations. In particular, the previous replication model and S3-first workload routing have been removed from the active plan.

## How it works

```text
New upload -> authenticate + validate -> read-only Drive availability check
                                         |
                           +-------------+--------------+
                           |                            |
                       responding                  unavailable
                           |                            |
                       Google Drive             configured S3 backend
                           |                            |
                           +---- one saved location ----+
                                          |
                         DB stores owner, status, provider pointer
```

1. The user signs in to Storvia and separately connects Drive with narrow OAuth access.
2. Storvia checks permissions, limits, consent, and Drive availability before starting a write.
3. The Go backend uploads to one provider and verifies completion before marking the file ready.
4. Downloads and deletes always use that file's recorded location.

Cloudflare **R2** stores file objects. **Durable Objects** is a different Cloudflare service and is not needed for this design.

### Important limits

- A full Drive, revoked authorization, admin block, missing file, or rate-limit response is not a service outage. These errors do **not** trigger S3 writes under the current plan.
- A timeout after a write might mean the write succeeded. Keep it pending and reconcile on the same provider; do not immediately send another copy to S3.
- Drive-only files cannot be served from S3 during an outage because there is no second copy.
- Proposed recovery behavior: new uploads return to Drive, while files already stored in S3 remain there. No automatic copying or moving back.
- Neither Drive nor S3 is the application's ownership database. The database holds tenant-scoped metadata and upload state, not file bytes.

## Go build plan

The [implementation plan](docs/implementation-plan.md) defines the proposed stack, package layout, milestones, test matrix, deployment gates, and future commands.

Suggested sequence:

1. Go foundation and tested storage-routing policy.
2. Durable upload state, idempotency, and tenant isolation.
3. Drive OAuth and upload/download/delete vertical slice.
4. S3-compatible adapter with outage-only routing.
5. Resumption, uncertain-result recovery, and reconciliation without copying.
6. Failure-injection, security, and pilot deployment checks.

Go is confirmed. PostgreSQL, library choices, hosting, exact timeout values, and the first S3 vendor are proposals/open choices, not accepted requirements.

## Documentation map

| File | Purpose |
|---|---|
| [AGENTS.md](AGENTS.md) | Persistent working rules, including Markdown updates |
| [docs/storage-policy.md](docs/storage-policy.md) | Confirmed storage contract, error classification, no-copies rules |
| [docs/architecture.md](docs/architecture.md) | Components, single-location schema, API, and lifecycle |
| [docs/implementation-plan.md](docs/implementation-plan.md) | Go build plan, acceptance tests, actual checks, next step |
| [docs/risk-register.md](docs/risk-register.md) | Failure modes, privacy/security controls, launch gates |
| [docs/references.md](docs/references.md) | Official API references and bounded community evidence |

## Decisions still needed before implementation/pilot

- Application sign-in/session integration and tenant model.
- Database acceptance, module import path, pinned Go/dependency versions, hosting target.
- First fallback backend, bucket ownership, region, cost limits, and credential delivery.
- File types, maximum size, concurrency, retention, and deletion/disconnect wording.
- Visible Drive folder UX and supported consumer/Workspace accounts.

Next step: approve the proposed implementation choices, then scaffold the Go module and implement the policy tests before calling a live provider. This documentation update does not provision cloud services or create application code.
