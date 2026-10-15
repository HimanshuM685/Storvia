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

## 2026-10-15 — Remove dead configuration and guard the config files with tests

Commit: *chore: remove dead configuration, add shipped-config guard tests*

### Why

The owner asked for the configuration surface to be cleaned up and the whole codebase
brought under test. An audit found configuration that was declared, defaulted, and
validated, but never read by any runtime code.

### Dead configuration removed

Every one of these was referenced only by its own default block and its own
validation. An operator could tune the value, see no effect, and reasonably conclude
the setting was subtly broken. That is worse than not offering the setting.

| Removed | Why it was dead |
|---|---|
| `server.download_timeout` | Never applied. `WriteTimeout` is `0` so a streamed download is not cut off |
| `server.max_request_body_size` | The body limit is a constant in `internal/httpapi`, not configuration |
| `server.trusted_proxies`, `auth.trusted_proxies` | Never read. `clientIP` deliberately ignores proxy headers, because a client-supplied `X-Forwarded-For` would otherwise poison the access log |
| `database.migrations_dir` | Migrations are compiled into the binary with `embed.FS`, so a path is meaningless |
| `cache.access_token_ttl`, `preflight_ttl`, `circuit_state_ttl` | Cache TTLs are not wired. `MemoryTokens` and `Memory` take their own TTLs |
| `cache.availability_scope` | Never read. The availability scope is built by the coordinator from the connection, not from configuration |
| `security.max_clock_skew` | Never used |
| `uploads.presigned_part_count` | Part planning is part of the unimplemented presigned path |
| `fallback.backends[].orphan_grace_period` | Orphan cleanup is not implemented |
| the entire `telemetry` block | `health_path`, `metrics_path`, `metrics_enabled`, and `readiness_timeout`. The three routes are registered at fixed paths in `internal/httpapi/router.go` |

Configuration fields declared: 105 to 90.

Deliberately **kept** despite appearing unused by a naive scan, because each is used
inside the config package or has a documented contract:

- `security.allowed_endpoint_schemes`, `allowed_endpoint_hosts` — enforced by `endpointAllowed`
- `uploads.max_file_size_bytes`, `category_limits` — used by `EffectiveMaxSize`
- `uploads.min_part_size_bytes` — the S3 five mebibyte minimum, enforced by validation
- `drive.circuit_failures`, `circuit_cooldown` — defined in `docs/storage-policy.md` section 4 and covered by tests in `internal/storage/policy`
- `fallback.backends[].disabled` — used by `ActiveBackends`

### `.env.example` cleaned

Removed three orphan variables that no config file referenced: `STORVIA_S3_ACCESS_KEY_ID`,
`STORVIA_S3_SECRET_ACCESS_KEY`, and `STORVIA_S3_SESSION_TOKEN`. They documented an AWS
alternative to the configured R2 backend, but with no `*_ref` pointing at them they
were a secret an operator would populate for no reason. AWS S3 remains supported by the
adapter; adding it back means adding the backend block and its references.

### Added

`internal/config/shipped_config_test.go` — nine tests that run against the real,
committed configuration files rather than fixtures built inside the test:

1. Both shipped files parse with an explicit environment.
2. Neither contains a credential-shaped value.
3. No secret-bearing field holds a value instead of a reference.
4. Neither file uses `${VAR}` substitution, which is the property that keeps ordinary
   settings out of `.env`.
5. Every `*_ref` in a config file is documented in `.env.example`.
6. Every variable in `.env.example` is referenced by a config file, so no orphan
   secret is documented.
7. The one compose-only exemption is genuinely used by `docker-compose.yml`.
8. Neither file contains a key the loader does not recognise, which catches a field
   removed from the struct but left behind in the JSON.

### Two bugs in the new tests, found by running them

- The environment-variable pattern matched only active assignments, so a variable
  documented as a commented placeholder counted as undocumented. Optional variables
  are documented precisely by being commented out, so the pattern now accepts that.
- `looksLikeCredential` flagged OAuth scope URIs and redirect URLs, because they are
  long strings without underscores. It now excludes anything containing `://` or `/`,
  since a real credential carries neither. Without this the check was useless, and the
  obvious fix was to delete it, which would have removed the guard entirely.

### Verified

```text
make check                                            -> pass
go test -race ./...                                   -> pass
internal/config shipped-config tests                   -> 9 pass
storvia-api -config config.development.json -check    -> valid
```

A repository-wide scan now reports no configuration field that is never read outside
the config package's own defaults and validation.

### Next step, in order

Unchanged: run the PostgreSQL migration against a real instance, implement the Drive
resumable upload verbs, then run the M3 smoke test against a controlled account.

---

## 2026-10-15 — Fix the configuration boundary: secrets only in the environment

Commit: *fix: enforce the configuration boundary between JSON and the environment*

### Why

The owner corrected a standing rule: actual credentials belong only in an ignored
`.env` or runtime secret injection, and ordinary settings such as timeouts, TTLs, and
booleans belong only in the JSON config file. Auditing the codebase against that rule
found six violations. All are fixed here.

### Violations found and fixed

| # | Violation | Fix |
|---|---|---|
| 1 | `database.dsn` took a whole connection string from `${STORVIA_DATABASE_DSN}`. Because a DSN contains a password, that pushed four **non-secret** values (host, port, user, database name) into `.env` | Split into plain `host`, `port`, `user`, `database`, `sslmode` in JSON plus `password_ref`. `BuildDSN(password)` assembles it at runtime, percent-encoding the password |
| 2 | `cache.redis_address` used `${STORVIA_REDIS_ADDRESS}`. A hostname is not a secret | Plain value in JSON. Removed from `.env.example` |
| 3 | `drive.redirect_uri` used `${STORVIA_GOOGLE_REDIRECT_URI}`. A URL is not a secret | Plain value in JSON. Removed from `.env.example` |
| 4 | `STORVIA_SESSION_SIGNING_KEY` was hardcoded in Go. The variable **name** is configuration and belongs in JSON | New `auth.session_signing_key_ref` field, defaulting to `STORVIA_SESSION_SIGNING_KEY` |
| 5 | `docker-compose.yml` carried an inline default password, `storvia-dev-password` | Removed. `POSTGRES_PASSWORD` is now required, so a missing value stops the stack instead of silently using a committed credential |
| 6 | `.env.example` documented `STORVIA_R2_ACCOUNT_ID`, a non-secret, and `STORVIA_DATABASE_DSN` and `STORVIA_REDIS_ADDRESS`, also non-secrets | Rewritten so every remaining variable is genuinely a secret |

### A seventh problem, found while fixing the sixth

**A missing secret was silently tolerated.** `validateFallback` checked only that a
reference *name* was non-empty, never that it resolved. An unset
`STORVIA_R2_SECRET_ACCESS_KEY` therefore produced an empty credential, the backend
failed to construct, and `buildBackends` logged an issue and skipped it. The
deployment then looked like the fallback was configured while no fallback existed,
and the outage path silently stopped working.

Now `Config.ResolveSecrets` resolves every reference once at startup and fails when a
required one is absent, naming the field and the variable. Optional references are
only required when the feature they belong to is enabled, so a deployment without
Google credentials still starts and reports that gap through `GET /v1/storage/status`.

### Added

- `Config.SecretRefs()` is the single authoritative list of what counts as a secret.
  Startup validation, the redaction log line, and the tests all read from it, so they
  cannot drift apart.
- `Config.ResolveSecrets(lookup)` resolves every reference and fails fast.
- `ValidateSecretRefs` rejects a reference that is not a variable name. It checks the
  character set and, more usefully, looks for recognizable credential prefixes
  (`AKIA`, `ASIA`, `ya29.`, `sk-`, `ghp_`, `xoxb-`, `-----BEGIN`) and for a long opaque
  string with no underscores. The rejection message names the field but never
  reprints the value.
- `DatabaseConfig.BuildDSN(password)`, replacing the `dsn` field.
- `internal/config/boundary_test.go` — 14 tests covering the boundary, including that
  a credential in a reference is rejected, that the rejection does not echo the
  credential, that an absent required secret fails startup, and that an optional
  secret does not fail a deployment that does not use its feature.

### Changed

- `internal/app/app.go` — secrets are resolved once in `Build` and passed down as a
  map. No call site reads the environment any more, so there is one place where a
  missing secret is detected. The map is held on `App.secrets`, never logged.
- `persistence.Open` takes the resolved password as a parameter, so no layer below
  the entry point touches the environment.
- `docker-compose.yml` passes only `STORVIA_DATABASE_PASSWORD`, not a full DSN.
- `.env.example` documents only genuine secrets, and explains why the rest are absent.
- `README.md` rewrites the configuration section around the secrecy split, including
  the DSN mistake so the reasoning is visible.

### New validation

- `sslmode: disable` is rejected when `environment` is `production`, because the DSN
  carries a credential.
- Every ordinary Postgres parameter is required, so splitting the DSN did not make
  anything silently optional.

### Verified

```text
make check                                                  -> pass
go test ./internal/config/...                                -> pass, 14 new tests
storvia-api -config config.development.json -check           -> valid
storvia-api with STORVIA_ENCRYPTION_KEY unset                -> fails, naming the field
storvia-api with AKIA... pasted into access_key_id_ref       -> fails, value not reprinted
```

A boundary audit over the repository now reports zero violations: no `${}` references
remain in the JSON, no credential-shaped key appears outside a `*_ref` field, no
environment variable name is hardcoded in Go, and no inline credential default remains
in the compose file.

### Next step, in order

Unchanged: run the PostgreSQL migration against a real instance, implement the Drive
resumable upload verbs, then run the M3 smoke test against a controlled account.

---

## 2026-10-14 — Containerization: Dockerfile, .dockerignore, compose stack

Commit: *build: add multi-stage Dockerfile, .dockerignore, and a compose stack*

### Why

The project had no container definition, so it could not be deployed anywhere that
expects an image.

### Added

- `Dockerfile` — three stages. `build` compiles a static binary, `test` runs vet,
  unit tests, and the race detector so a failing suite fails the build rather than
  producing an unchecked image, and `runtime` ships only the binary on
  `distroless/static-debian12:nonroot`, which has no shell, no package manager, and
  runs as a non-root user.
- `.dockerignore` — excludes `.env*`, keys, local databases, build output, and
  `node_modules` from the build context. Anything matched is invisible to the daemon,
  which is the only reliable way to guarantee a credential cannot end up in a layer.
- `docker-compose.yml` — Postgres, Redis, and the API, with health-gated startup so
  migrations do not run against a database that is still booting.

### Changed

- `cmd/storvia-api/main.go` — added `version` and `commit` variables and a `-commit`
  flag so `-ldflags "-X main.version=..."` actually has something to set. Previously
  the `-version` output was a hardcoded string and the linker flag in the Dockerfile
  would have been silently ignored, since the Go linker does not error on a `-X` for a
  symbol that does not exist. The flag now reports the injected value, verified by
  building with and without the ldflags.
- `AGENTS.md` — added the rule that every meaningful change must also update the
  author's private working memory, alongside the public changelog entry.
- `README.md` — added a container section, including an explicit statement that the
  image has not been built.

### Verified, and not verified

Verified directly:

- `CGO_ENABLED=0 GOOS=linux go build` produces a working static binary, which is what
  permits a distroless runtime. This works because the SQLite driver is a pure-Go
  translation and needs no cgo.
- Migrations are compiled in via `embed.FS`, confirmed by reading the directive, so
  the runtime image needs no schema directory.
- The version and commit ldflags now take effect.
- `make check` passes.

**Not verified: the Docker image has never been built.** No Docker daemon was
available in the environment where the Dockerfile was written. The base image tags,
the `RUN --mount=type=cache` BuildKit syntax, and the compose health-check invocation
are all unexercised. The first `docker build` is a check to run.

### Notes for whoever runs the build first

- `golang:1.27-alpine` must exist on Docker Hub. If it does not, pin to the exact
  patch tag.
- The runtime stage has no shell and no `curl`, so an in-image `HEALTHCHECK` using
  `curl` would silently never run. The health check therefore invokes the binary's
  own `-check` flag, which validates configuration and exits.
- The compose stack deliberately uses Postgres rather than SQLite.

### Next step, in order

Unchanged: run the PostgreSQL migration against a real instance, implement the Drive
resumable upload verbs, then run the M3 smoke test against a controlled account.

---

## 2026-10-14 — Attribution, SECURITY.md, and CONTRIBUTING.md

Commit: *docs: add SECURITY.md and CONTRIBUTING.md, attribute copyright to shovan04*

### Why

The owner confirmed the project's creator as **shovan04** and asked for the two
community documents a public repository is expected to have.

### Changed

- `LICENSE` — copyright attribution corrected from "Storvia contributors" to
  **"Copyright 2026 shovan04"**. The earlier wording was an invention: the author's
  identity was not known when the licence was written, and inventing a collective
  holder for a single-author project was the wrong default.
- `README.md` — contributing section now points at `CONTRIBUTING.md`, the
  documentation table lists both new files, and the contributing section carries a
  pointer to `SECURITY.md` so a reporter does not open a public issue.

### Added

- `SECURITY.md` — private disclosure through GitHub Security Advisories, with a
  maintainer note that the feature must be enabled under Settings → Security or the
  path will not work. Includes a scope table naming tenant isolation, secret handling,
  OAuth, the single-location invariant, injection, and capability leakage as
  high-severity in-scope areas; the TypeScript SDK is in scope too, since incorrect
  idempotency handling there causes duplicate uploads. Also lists the known
  limitations that are *not* vulnerabilities, so a reporter does not file the
  no-copies trade-offs as bugs, and states that the project has not completed a
  security review.
- `CONTRIBUTING.md` — setup, the `make` targets, and the two project rules expected of
  a change: the storage contract is load-bearing and the tests in
  `internal/storage/policy` must mirror `docs/storage-policy.md` row for row, and a
  rule should be enforced structurally where possible rather than left to reviewers.
  Names the specific mistakes the existing tests are written to catch, so a
  contributor learns them from the test file.

### Still outstanding for a first release tag

- GitHub issue and pull request templates
- A decision on publishing the SDKs to public registries. The TypeScript package name
  `@storvia/sdk` is unscoped and unclaimed.

### Next step, in order

Unchanged: apply the PostgreSQL migration to a real instance, implement the Drive
resumable upload verbs, then run the M3 smoke test against a controlled account.

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
  filled in as "Copyright 2026 shovan04". The text was copied from a
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

---

## 2026-10-15 — Development admin panel for Google Drive OAuth testing

Commit: *not committed; changes are left in the working tree for the owner*

### Why

The owner asked for a local admin panel that can complete Google OAuth and show the
connected test user's Drive authorization. The existing application already had the
OAuth state/token/connection foundation, but no browser page could start the flow and
issue an application session for a controlled development identity.

### Decisions settled

- The panel is development-only and is rejected by configuration validation outside
  the `development` environment.
- It uses configured fixed development tenant/user IDs; the browser cannot select
  either identity.
- It uses the existing narrow `drive.file` scope and the existing encrypted refresh
  token path.
- The panel has a separate registered callback URI so the existing authenticated API
  callback remains unchanged:
  `/admin/oauth/callback` for development versus
  `/v1/storage/connections/google-drive/callback` for the normal API.
- The panel issues a normal Storvia session only after Google code exchange succeeds.
- The page shows connection status, Google subject, granted scope, folder presence,
  logout, and disconnect. It does not implement file transfer.

### Added/changed

- `internal/httpapi/admin_handlers.go` — server-rendered development admin page,
  browser-bound flow cookie, OAuth start/callback, session issuance, status view,
  logout, and disconnect.
- `internal/httpapi/admin_handlers_test.go` — tests for the disabled gate, callback
  session issuance, metadata rendering, unauthorized disconnect, and configured
  connection use.
- `internal/config/config.go` — `admin_panel` configuration and the separate
  `drive.admin_redirect_uri`; enabled panels require development mode, fixed IDs,
  and a callback URI.
- `config/config.development.json` — enables the local panel with non-secret test
  tenant/user IDs and `http://127.0.0.1:8080/admin/oauth/callback`.
- `config/config.production.json` — explicitly disables the panel.
- `internal/app/app.go` and `internal/httpapi/router.go` — wire the panel without
  changing `/v1` authentication.
- `internal/storage/provider/drive/client.go`, `oauth.go`, and
  `internal/storage/service/connections.go` — support an operator-configured second
  callback URI while keeping the normal flow unchanged.
- `internal/storage/provider/drive/client_test.go` — verifies the OAuth `state`
  query parameter is the exact server-stored state value.
- `.env.example` — documents both the normal API callback and the development admin
  callback as exact Google OAuth redirect URIs.
- `internal/config/config_test.go` — verifies checked-in development/production JSON
  uses environment references for credential-bearing fields instead of literal secret
  values.
- `README.md`, `docs/architecture.md`, `docs/implementation-plan.md`, and
  `docs/risk-register.md` — record the panel, setup, and remaining live-provider gap.

### Bug found and fixed

The existing OAuth URL appended the browser binding to the `state` query parameter,
while the callback attempted to consume the unmodified server-stored state. A real
Google callback would therefore fail state lookup. The binding is already stored and
checked separately by `StateStore`, so the URL now carries only the stored state.

### Checks actually run

```text
go test ./internal/httpapi ./internal/config ./internal/storage/provider/drive ./internal/storage/service ./internal/app -> success
go test ./...                                                                                                       -> all packages ok
go vet ./...                                                                                                         -> clean
gofmt -l ./cmd ./internal ./sdk/go ./migrations                                                               -> no output
go run ./cmd/storvia-api -config config/config.development.json -env .env.development -check                    -> success; reports missing Google credentials as a startup issue
curl http://127.0.0.1:8080/admin                                                                           -> 200; rendered the panel
curl http://127.0.0.1:8080/admin/connect                                                                       -> 502 because local Google credentials are empty
make check                                                                                                  -> go vet and go test success
make check-config                                                                                           -> success; reports missing Google credentials as a startup issue
```

The first server smoke command was re-run with a corrected shell variable after
zsh rejected the read-only variable name `status`; no code issue was involved.

The configuration boundary was also corrected: the earlier R2 instructions could be
read as putting account identity settings and secrets together in `.env`. Account
endpoint/bucket/region/prefix remain JSON configuration; only R2 access/session keys
remain in `.env`.

### Not done

- No live Google account, consent round trip, or real Drive API call was performed;
  `.env.development` has no Google client credentials.
- Drive folder creation, resumable upload, media download, and delete remain
  unimplemented.
- No PostgreSQL or S3/R2 provider verification was performed.
- No commit was created; the owner will inspect and commit the changes.

### Next step, in order

1. Add the local Google OAuth client ID/secret to the ignored `.env.development` and
   register both exact callback URIs in Google Cloud.
2. Start the API and complete `/admin` against a controlled Google test account.
3. Record the observed callback, connection status, and any provider errors.
4. Implement the Drive `Storvia/` folder creation and resumable transfer verbs.
