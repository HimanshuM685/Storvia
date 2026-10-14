# Storvia — Drive-first file storage in Go

**Updated:** 2026-10-14

**Project state:** Working Go implementation with tests. The routing policy engine,
metadata store, HTTP API, configuration system, and both client SDKs are built and
passing checks. The Drive and S3 transfer verbs (resumable upload, media download,
delete) are **not yet implemented**; those endpoints report that plainly rather
than pretending to work. See [Implementation status](#implementation-status).

## Confirmed direction

- **Google Drive is the primary destination for new files.**
- **S3-compatible storage is an outage fallback only:** use AWS S3, Cloudflare R2, or
  another tested compatible backend when Drive is not responding.
- **No application-managed copies:** no dual writes, backup replicas, mirroring, or
  background copy-back.
- **Backend language: Go.**
- Keep requirements, design changes, checks, and next steps in Markdown for future
  sessions.

The detailed [storage policy](docs/storage-policy.md) takes precedence over earlier
recommendations. In particular, the previous replication model and S3-first
workload routing have been removed from the active plan.

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

Cloudflare **R2** stores file objects. **Durable Objects** is a different Cloudflare
service and is not needed for this design.

### Important limits

- A full Drive, revoked authorization, admin block, missing file, or rate-limit response
  is not a service outage. These errors do **not** trigger S3 writes.
- A timeout after a write might mean the write succeeded. The upload stays pending and
  is reconciled on the same provider; no copy is sent to S3.
- Drive-only files cannot be served from S3 during an outage because there is no second copy.
- New uploads return to Drive after recovery; files already stored in S3 remain there. No
  automatic copying or moving back.
- Neither Drive nor S3 is the application's ownership database. The database holds
  tenant-scoped metadata and upload state, not file bytes.

## Repository layout

```text
cmd/storvia-api/       API entry point
internal/
  app/                 dependency wiring and lifecycle
  apperr/              shared error vocabulary and RFC 9457 kinds
  auth/                application sessions, tenant/user identity
  cache/               cache interface, in-memory TTL, Redis
  config/              JSON config, .env loading, validation
  httpapi/             routes, middleware, global error handler
  idid/                opaque identifier generation
  logger/              structured logging with mandatory redaction
  persistence/         repositories, migrations, dialect handling
  secrets/             AES-GCM keyring for refresh tokens
  storage/
    model/             domain types and provider-neutral states
    policy/            pure routing engine: classification, budgets, circuit
    service/           upload coordinator, object reads and deletes
    provider/          provider interfaces
      drive/           OAuth, connection registry, availability, token refresh
      s3compat/        S3-compatible adapter (AWS S3 and Cloudflare R2)
migrations/            per-dialect SQL, embedded at build time
sdk/go/                Go client SDK
sdk/ts/                TypeScript client SDK
config/                JSON configuration files
docs/                  storage policy, architecture, plan, risk register, references
```

## Configuration

Configuration is split deliberately:

- **JSON files** (`config/*.json`) hold structure and non-secret values, and are safe to
  review and commit.
- **`.env` files** hold every API key, password, and signing material, and never enter
  version control.

A JSON string of the form `${VAR}` or `${VAR:-fallback}` is resolved from the
environment at load time. A reference with no value and no default is a startup
error, so a missing key fails loudly instead of becoming an empty credential.

```sh
cp .env.example .env.development    # then fill in the values
make check-config                   # validate without serving
make run                            # serve on 127.0.0.1:8080
```

To enable outage fallback, also set `fallback.enabled` to `true` and `disabled` to
`false` for the backend in the JSON file. The loader refuses to enable fallback
while `consent_required` is `true`: outage uploads must not be switched on before
the disclosure is accepted.

## Go build plan

The [implementation plan](docs/implementation-plan.md) defines the stack, package
layout, milestones, test matrix, and deployment gates.

Suggested sequence:

1. Go foundation and tested storage-routing policy. **Done.**
2. Durable upload state, idempotency, and tenant isolation. **Done** (schema and
   repositories; worker leases still to come).
3. Drive OAuth and upload/download/delete vertical slice. **Partially done:** OAuth,
   the connection registry, token refresh, and the availability check are built.
   Resumable upload, media download, and delete are not.
4. S3-compatible adapter with outage-only routing. **Partially done:** the adapter,
   key generation, multipart, presigning, and error classification are built. It has
   not been exercised against a real bucket.
5. Resumption, uncertain-result recovery, and reconciliation without copying. **Not started.**
6. Failure-injection, security, and pilot deployment checks. **Not started.**

## Implementation status

Recorded honestly, per the documentation rule in `AGENTS.md`. "Done" means the code
exists and its tests pass; it does not mean a real provider has been exercised.

### Working and tested

| Area | Detail |
|---|---|
| Routing policy | Table-driven tests mirror `docs/storage-policy.md`; fallback is selected only for `ClassUnavailable` in the read-only phase |
| Error vocabulary | `internal/apperr`, one kind per HTTP status, RFC 9457 problem bodies |
| Global error handler | `internal/httpapi`; every failure funnels through one renderer that never serializes a cause |
| Configuration | JSON + `.env`, strict decoding, endpoint allow-lists, refusal of the broad Drive scope |
| Metadata store | SQLite and PostgreSQL dialects, migrations, tenant-scoped repositories, optimistic row versions |
| Single-location invariant | Enforced by database `CHECK` constraints, not only by application code |
| Idempotency | Unique `(tenant, user, idempotency_key)` plus a request fingerprint; reuse with different metadata conflicts |
| Tenant isolation | Repositories require a verified `Scope`; a cross-tenant read returns `ErrNotFound`, never the row |
| Drive OAuth | State-bound consent, single-use state store, browser binding, narrow scope enforcement, refresh-token encryption and rotation |
| S3 adapter | Common S3 subset for AWS S3 and R2, server-generated tenant keys, bounded SDK retries, error classification |
| Caching | TTL interface with in-memory default and Redis implementation |
| Logging | `slog` JSON with mandatory redaction of tokens, secrets, session URIs, and signatures |
| SDKs | Go and TypeScript clients sharing the server's error vocabulary |

### Not yet implemented

| Area | Effect |
|---|---|
| Drive resumable upload | `PATCH /uploads/{id}/chunks` returns `provider_not_configured` |
| Drive media download | Served locations work; Drive content reads are not implemented |
| Drive and S3 delete | `DELETE /objects/{id}` is wired for S3; Drive delete is not implemented |
| Upload completion | `POST /uploads/{id}/complete` returns `provider_not_configured` |
| Reconciliation jobs | `POST /uploads/{id}/reconcile` returns `provider_not_configured` |
| Job workers and leases | `storage_jobs` exists; no worker claims from it |
| Change-feed reconciliation | Changes cursor column exists; no poller |
| Metrics | `/metrics` is a stub; no counters yet |
| Multi-tenant session store | `MemorySessions` only; a production deployment needs a durable store |

### Checks actually run

```text
make check        # gofmt -l, go vet, go test ./...   -> pass
make test-race    # go test -race ./...                -> pass
sdk/ts: npx tsc --project tsconfig.json               -> pass
sdk/ts: node --test --experimental-strip-types test/  -> 19 tests, 19 pass
```

No test has been run against a live Google account, a real S3 bucket, or R2. The
pilot gate in `docs/implementation-plan.md` explicitly forbids claiming M3 or M4
complete on mocked tests alone.

## Documentation map

| File | Purpose |
|---|---|
| [AGENTS.md](AGENTS.md) | Persistent working rules, including Markdown updates |
| [CHANGELOG.md](CHANGELOG.md) | What changed, what broke, what was verified, and what is still missing |
| [docs/storage-policy.md](docs/storage-policy.md) | Confirmed storage contract, error classification, no-copies rules |
| [docs/architecture.md](docs/architecture.md) | Components, single-location schema, API, and lifecycle |
| [docs/implementation-plan.md](docs/implementation-plan.md) | Go build plan, acceptance tests, actual checks, next step |
| [docs/risk-register.md](docs/risk-register.md) | Failure modes, privacy/security controls, launch gates |
| [docs/references.md](docs/references.md) | Official API references and bounded community evidence |

## Decisions still needed before implementation/pilot

Settled during the current milestone:

- Database: **SQLite for development, PostgreSQL for production**, behind one
  repository interface with per-dialect SQL.
- Fallback backend: **adapter-abstracted across AWS S3 and Cloudflare R2**, targeting
  the common S3 subset. Neither vendor has been verified yet.
- SDKs: **Go and TypeScript** for v1.
- Cache: **interface with an in-memory default and a Redis implementation.**
- Go module path: **`github.com/shovan04/storvia`.**

Still open:

- Application sign-in/session integration and the production session store.
- Hosting target and deployment environment.
- Fallback bucket ownership, region, cost limits, and credential delivery in
  production.
- File types, maximum size, concurrency, retention, and deletion/disconnect wording.
- Visible Drive folder UX and supported consumer/Workspace accounts.

Next step: implement the Drive resumable upload and media download verbs, then run
the M3 smoke test against a controlled Google account before enabling fallback.