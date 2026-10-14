# Storvia change log

**Purpose:** a durable, append-only record of what changed in this repository and
why, written for a future session that has none of the context of the session that
made the change.

This file records **actual** work. Entries must not claim a check passed unless the
command was run and its output seen. Where something is known to be incomplete, the
entry says so rather than leaving it to be discovered.

Related documents:

- `README.md` — current shape of the project and how to run it
- `docs/implementation-plan.md` — milestones, acceptance criteria status, checks run
- `docs/storage-policy.md` — the confirmed storage contract
- `docs/architecture.md` — components, schema, and design decisions

---

## 2026-10-14 — Open source the project: Apache-2.0 licence and public README

Commit: *docs: add Apache-2.0 licence and rewrite the README for open source*

### Why

The owner intends to publish the repository. That requires a licence, and the
existing README was written for a single reader continuing one piece of work rather
than for an outside contributor who has never seen the project.

### Decisions settled in this milestone

| Decision | Value | Why |
|---|---|---|
| Licence | Apache License 2.0 | Permissive, and the explicit patent grant matters for a project integrating third-party storage APIs that someone may deploy commercially |

MIT was the other candidate and is also permissive. Apache-2.0 was chosen because the
patent grant is explicit, which is the relevant concern for an infrastructure
project wrapping other vendors' APIs.

**Not chosen: AGPL-3.0.** It would require anyone offering the service over a network
to publish their modifications. That may be what the owner wants, but it is a
business decision rather than a technical default, so it was not applied unilaterally.

### Added

- `LICENSE` — the verbatim Apache License 2.0 text, with the appendix copyright line
  filled in as "Copyright 2026 Storvia contributors". The text was copied from a
  known-unmodified Apache-2.0 file rather than transcribed, to avoid the corruption
  that hand-copying a legal document invites.

### Changed

- `README.md` rewritten for an outside audience. It now leads with what the product
  does and what it deliberately refuses to do, rather than opening with internal
  project state. Specific changes:
  - Added a licence badge and corrected the Go and TypeScript version badges. The Go
    version was initially written as "1.24+" from memory and corrected to 1.27 to
    match `go.mod`.
  - Added an explicit table of what the no-copies rule costs a user: a failed download
    during a Drive outage, fallback files staying on S3 permanently, and no recovery
    after the loss of the only file. This belongs in the README rather than only in
    the planning documents, because it is the first thing an adopter needs to know.
  - Added requirements, quick start, Google OAuth setup, SDK usage, the API surface,
    and a security-model section.
  - Added contributing guidance pointing at `AGENTS.md` and the storage contract.
  - Added a licence section naming Apache-2.0 and stating why, plus the licences of
    the direct dependencies. **These were verified by reading each module's licence
    file**, not from memory: AWS SDK for Go v2 is Apache-2.0, `pgx` is MIT,
    `modernc.org/sqlite` and `go-redis` are BSD-3-Clause.
  - Kept the status table honest. It states plainly that the S3 adapter has never been
    run against a real bucket and that no path has touched a live Google account or
    PostgreSQL.
- `sdk/ts/package.json` — `license` changed from `UNLICENSED` to `Apache-2.0`, and
  `private: true` removed so the package can actually be published. Added repository,
  bugs, and homepage fields.

### Verification gaps

No code changed, so no provider or database verification is implicated. The
dependency licence claims in the README were checked against the licence file in each
module's directory under the module cache.

Still outstanding for a public repository, and worth adding before the first release
tag:

- A `SECURITY.md` with a private disclosure contact. This project handles Google
  refresh tokens and enforces tenant isolation, so a disclosure route is expected
  rather than optional.
- A `CONTRIBUTING.md` expanding the short section currently in the README.
- GitHub issue and pull request templates.
- A decision on publishing the SDKs to their public registries
  (`github.com/shovan04/storvia/sdk/go` and `@storvia/sdk` on npm). The TypeScript
  package name is currently unscoped and unclaimed.

### Next step, in order

Unchanged from the previous entry, and still ahead of any release work:

1. Apply `migrations/postgres/0001_init.sql` to a real PostgreSQL instance.
2. Implement the Drive resumable upload verbs.
3. Run the M3 smoke test against a controlled Google account.

---

## 2026-10-14 — Documentation: add the durable change log

Commit: *docs: add CHANGELOG.md and make the changelog rule explicit*

### Why

The previous session updated `README.md`, `docs/architecture.md`,
`docs/implementation-plan.md`, and `docs/references.md` after building the service,
but never created a dedicated record of **what had changed**. The owner's standing
requirement is durable documentation for future sessions, and a future session
starting cold has to reconstruct that from prose spread across four documents, with
no indication of what was deliberately left undone.

### Added

- `CHANGELOG.md` — an append-only record, one entry per meaningful change. Each entry
  records decisions settled, behaviour changed, bugs found and their impact, commands
  actually run with their results, coverage per package, what was not done,
  verification gaps, the next step in order, and the open decisions still needing the
  owner.
- The first entry covers milestone 1 in full.

### Changed

- `AGENTS.md` now names `CHANGELOG.md` in the read-first list, states the changelog
  requirement explicitly as its own rule, and lists the six things every entry must
  contain: decisions, behaviour, bugs, commands and results, what was not done, and
  the next step.
- `AGENTS.md` no longer describes the repository as planning documents only. That
  sentence became wrong once the service was built, and a future session reading it
  would have assumed there was no working code.
- `README.md` documentation map links `CHANGELOG.md`.
- `docs/implementation-plan.md` section 6 states that appending a changelog entry is
  part of the same change, and that updating the plan or README is not a substitute.

### Verified

```text
make check   # gofmt -l, go vet, go test ./...  -> pass
```

No code changed in this entry, so no provider or database verification is implicated.

---

## 2026-10-14 — Milestone 1: service foundation, routing policy, dual-dialect store, SDKs

Commit: `8fdd875` — *feat: implement Go API, routing policy engine, dual-dialect store, and SDKs*

### Why

The repository contained planning documents only. The owner approved the open
milestone-0 decisions and asked for a working Go implementation with an HTTP API,
client SDKs, JSON configuration, `.env`-held secrets, a relational store, and tests.

### Decisions settled in this milestone

These moved from "proposed" to accepted. Recorded here so a later session does not
reopen them without a reason.

| Decision | Value | Notes |
|---|---|---|
| Go module path | `github.com/shovan04/storvia` | Settled; changing it after SDK consumers exist is costly |
| Metadata store | SQLite for development, PostgreSQL for production | One repository interface, separate SQL per dialect |
| Fallback backend | Adapter-abstracted across AWS S3 and Cloudflare R2 | Common S3 subset only; neither vendor verified |
| Client SDKs | Go and TypeScript | Chosen for v1 |
| Cache | Interface, in-memory default, Redis implementation | Redis only needed for multi-instance |
| HTTP routing | Standard library `http.ServeMux` | Chosen over chi on measured evidence, see below |
| Error format | RFC 9457 `application/problem+json` | One shape for all failures and both SDKs |
| Identifiers | CSPRNG-generated, prefixed, opaque | Nothing guessable or descriptive |
| Token encryption | AES-256-GCM keyring with rotation overlap | Refresh tokens decryptable only inside the Drive adapter |

**Routing choice evidence:** a community benchmark on Go 1.27.1 found chi marginally
*slower* and more allocation-heavy than the standard library's `ServeMux`, with all
`net/http`-based routers within 4% of each other. The author's own caveat is that a
single database roundtrip dwarfs the router's share. The original plan said "chi or
`http.ServeMux`"; the measurement decided it. Source recorded in
`docs/references.md` section 22.

### Added

**Cross-cutting infrastructure**

- `internal/apperr` — one error vocabulary. Every error reaching HTTP is an
  `*apperr.Error`, matched by kind with `errors.Is`, mapped to an HTTP status and an
  RFC 9457 problem type by a single table. An error that is *not* an `*apperr.Error`
  is coerced to a generic 500, so a bare `fmt.Errorf` from a deep package cannot
  become a client-visible string.
- `internal/httpapi` — routes, middleware, and the global error handler. Every failure
  funnels through one renderer that logs the wrapped cause and never serializes it.
- `internal/logger` — `slog` JSON. Redaction is applied **inside the handler**, not
  requested from callers: sensitive attribute names are replaced with a marker before
  any record is written, including nested groups and `With`-bound attributes.
- `internal/config` — JSON configuration with `${VAR}` / `${VAR:-default}` resolution
  from the environment. Strict decoding rejects unknown fields, so a typo in a
  security-relevant key fails at startup rather than silently becoming a zero value.
- `internal/cache` — `Cache` interface with per-entry expiry. In-memory default with
  an injectable clock and a background janitor; Redis implementation for shared state.
- `internal/secrets` — AES-256-GCM keyring. Decrypt tries the referenced key, then
  the active one, so a rotation does not sign every user out. Validation happens at
  startup: a process that cannot decrypt its tokens should not accept uploads.
- `internal/idid` — opaque identifier generation.
- `internal/auth` — HMAC-signed session tokens over an opaque session ID, so no
  identity claim can be edited by the holder. `MemorySessions` development store.

**Storage core**

- `internal/storage/model` — provider-neutral domain types. `LogicalObject.Location()`
  fails loudly on a ready row with zero or two locations rather than returning a guess.
- `internal/storage/policy` — the pure routing engine. No network, no database, no
  clock of its own; every input is passed in. This is what makes the policy table
  testable as plain unit tests.
- `internal/storage/service` — upload coordinator and object read/delete use cases.
- `internal/storage/provider` — `Provider` interface plus availability, mode, and
  lifecycle types.
- `internal/storage/provider/drive` — OAuth consent flow, connection registry, token
  refresh, and the read-only availability preflight.
- `internal/storage/provider/s3compat` — the S3-compatible adapter for AWS S3 and R2.

**Persistence**

- `migrations/sql/sqlite/0001_init.sql`, `migrations/sql/postgres/0001_init.sql`,
  embedded at build time so a compiled binary can migrate without loose files.
- `internal/persistence` — store, migrator, and tenant-scoped repositories.

**SDKs**

- `sdk/go` — Go client sharing the module prefix, with no dependency on server
  internals.
- `sdk/ts` — TypeScript client for browsers and Node.

**Repository files**

- `config/config.development.json`, `config/config.production.json`
- `.env.example` — every variable documented with the command to generate it
- `.gitignore`, `Makefile`

### Changed behaviour worth knowing

- **Unknown provider failures are refused, not guessed.** An error the adapter cannot
  classify becomes `ClassUnknown`, and `ClassUnknown` is never eligible for the
  fallback. Treating an unclassified error as an outage is exactly how a mis-mapped
  quota or auth error reaches the fallback bucket.
- **Missing Drive credentials are a configuration error, not an outage.** At startup
  the service logs an issue and reports `provider_not_configured` through
  `GET /v1/storage/status` instead of failing to start. Classifying this as
  unavailable would route the first upload of every deployment into the fallback.
- **`GET /v1/storage/status` distinguishes blocked states.** "Not configured on this
  deployment" and "this user has not connected Drive" are different problems for
  different people, so the endpoint reports which.
- **Cross-tenant reads return not-found, never forbidden.** A forbidden response would
  confirm the object ID exists, turning the endpoint into an existence oracle.
- **Delete is idempotent**, and a provider `404` is treated as already-absent rather
  than as a failure. Deletion is pinned to the file's recorded location: a file
  uploaded during an outage is deleted from S3, never from Drive.
- **The access log records the path only, never the query string**, because a presigned
  capability can arrive as a query parameter.
- **`WriteTimeout` defaults to 0** so a streamed download is not cut off; the upload
  and metadata budgets are separate per-route deadlines.
- **Session `Verify` splits the token on the last dot.** The payload is
  `<sessionID>.<expiry>` and the signature covers both segments joined; splitting on
  the first dot rejects every genuine token.

### Bugs found by the tests and fixed

Each of these was caught by a test written against the documented contract, and each
would have caused real data problems.

| Bug | Impact |
|---|---|
| `SelectLocation` used the pre-insert `RowVersion` | **Every** upload failed its own optimistic lock. The row version was never read back after insert |
| The routing coordinator ignored a `reject` decision | A revoked grant or an unclassified failure proceeded to write to Drive anyway, violating the policy |
| `CommitReady` compared the stored Drive file ID against the incoming one | The stored value is a *planned* identity; Drive assigns the real ID on completion. Every genuine Drive upload would have been rejected |
| `SelectLocation` set object status to `selected` | `selected` is an attempt state; the object status constraint rejected the write |
| `BackendRepository.Upsert` was missing an argument | Backend configuration could never be recorded, so the fallback gate could never open |
| `OwnsKey` compared against the unprefixed namespace | Orphan sweeps would reject Storvia's own keys whenever a prefix was configured |
| `Circuit.Observe` counted mutation-bearing failures | A single ambiguous write could open the outage circuit and push other users' uploads to S3 |
| A circuit success arriving after cooldown left the circuit half-open | The documented "a successful probe closes it immediately" behaviour did not hold |
| `Issuer.Verify` split the token on the first dot | No session token could ever verify |
| `ParseEnv` did not decode the escapes `FormatEnv` emits | A secret containing a quote or backslash did not round-trip through `.env` |
| `ParseByteRange` / `ContentDisposition` | Malformed ranges and header-injecting filenames were not fully rejected |

### Checks actually run

Executed on 2026-10-14; the results are what is written here.

```text
go build ./...                                       -> success
gofmt -l ./cmd ./internal ./sdk/go ./migrations      -> no output
go vet ./...                                         -> clean
go test ./...                                        -> all packages ok
go test -race ./...                                  -> all packages ok
sdk/ts: npx tsc --project tsconfig.json               -> success
sdk/ts: node --test --experimental-strip-types test/  -> 19 tests, 19 pass, 0 fail
```

Coverage by package, recorded for comparison against the next milestone:

| Package | Coverage |
|---|---|
| `internal/logger` | 98.4% |
| `internal/storage/model` | 96.3% |
| `internal/apperr` | 91.7% |
| `internal/secrets` | 89.2% |
| `internal/config` | 87.8% |
| `internal/auth` | 86.5% |
| `internal/storage/policy` | 79.4% |
| `internal/storage/service` | 71.7% |
| `internal/persistence` | 63.8% |
| `sdk/go` | 62.9% |
| `internal/cache` | 60.5% |
| `internal/storage/provider/s3compat` | 42.1% |
| `internal/httpapi` | 35.3% |
| `internal/storage/provider/drive` | 34.3% |
| `internal/app`, `internal/idid`, `cmd/storvia-api` | 0% |

Low coverage in the two provider packages is expected and honest: it reflects
unexercised provider calls, not missing unit tests. Low coverage in `internal/app` is
wiring code that is verified by starting the binary instead.

### Not done

Recorded so a future session does not assume these exist.

- Drive resumable upload verbs: folder creation, session initiation, chunk proxying,
  completion verification. `PATCH /uploads/{id}/chunks` returns
  `provider_not_configured`.
- Drive media download and Drive delete.
- Upload completion and reconciliation endpoints.
- Job workers and lease acquisition on top of `storage_jobs`. The table exists; no
  worker claims from it.
- Drive changes-feed reconciliation poller.
- Real metrics. `/metrics` is a stub.
- Production session store behind `auth.Lookup`.
- Documentation for users about fallback storage, region, retention, and deletion
  behaviour.

### Verification gaps

The most important thing in this entry. None of the following has been done:

- **No test has run against PostgreSQL.** Every migration ran on SQLite. The dialects
  differ in the job-lease query, identifier handling, and boolean representation,
  which is precisely where a migration passes in development and fails in production.
- No test has run against a live Google account.
- No test has run against a real AWS S3 or R2 bucket.
- No licence or dependency audit.

Per `docs/implementation-plan.md`, M3 and M4 cannot be called complete on mocked
tests alone.

### Next step, in order

1. Apply `migrations/postgres/0001_init.sql` to a real PostgreSQL instance and fix any
   divergence. Nothing in this milestone touched it.
2. Implement the Drive resumable upload verbs.
3. Run the M3 smoke test against a controlled Google account **before** enabling
   fallback in any environment.

### Open decisions still needing the owner

- Application sign-in integration and the production session store.
- Hosting target.
- Fallback bucket ownership, region, cost limits, credential delivery.
- File types, maximum size, retention, deletion and disconnect wording.
- Privacy and legal acceptance that an outage upload may be stored outside the user's
  Drive. `fallback.consent_required` is hard-coded to `true` in the loader as the
  code-level stand-in until this is signed off.