# Storvia

Drive-first file storage. Files go to the user's own Google Drive; when Drive is
unavailable, eligible new uploads go to one configured S3-compatible bucket instead.

**Storvia keeps exactly one copy of your file.** There is no mirroring, no dual
write, and no background copy-back. That constraint shapes everything below.

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![TypeScript](https://img.shields.io/badge/TypeScript-5.9%2B-3178C6?logo=typescript&logoColor=white)](sdk/ts)

---

## What it does

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

1. The user signs in and separately connects Drive with the narrow `drive.file` scope.
2. Storvia checks permissions, limits, consent, and Drive availability before any write.
3. The upload goes to one provider, and the location is verified before the file is
   marked ready.
4. Downloads and deletes always use that file's recorded location.

### The one rule that shapes the design

**Drive being unavailable is not the same as Drive failing.**

Only a bounded, read-only availability check that fails with a transport error or a
5xx — and only *before* any bytes have left the process — may send a new upload to
the fallback bucket. A full Drive, a revoked grant, a Workspace admin block, a rate
limit, a deleted folder, or an error Storvia cannot classify all mean "do not upload
here", and none of them mean "upload somewhere else".

### What this costs you

The no-copies rule has real, deliberate consequences:

| Situation | What happens | Why |
|---|---|---|
| Drive is down, a user downloads a Drive-stored file | The download fails | There is no second copy to serve from |
| An upload is written to S3 during an outage | It stays on S3 permanently, even after Drive recovers | Moving it back would mean a copy-verify-delete move, which temporarily creates two locations |
| A Drive write times out | The upload becomes `outcome_unknown` and is reconciled on Drive | A timeout can mean "Drive stored it and the reply was lost". Writing to S3 as well would create two files |
| The user loses their Drive account | The file is gone | Storvia is not a backup product |

Storvia is an availability strategy, not a backup strategy. It does not provide
independent backup, seamless cross-provider read failover, immutable retention, or a
recovery guarantee after the loss of the only file.

---

## Status

Early. The foundations are built and tested; the file transfer paths are not finished.

| Area | State |
|---|---|
| Routing policy engine | Complete, table-driven tests |
| Error handling, logging, caching, configuration | Complete, tested |
| Metadata store with enforced single-location invariant | Complete, tested on SQLite |
| Google OAuth (consent, token refresh, revocation) | Complete; **verified against a live Google account** |
| Development admin panel | Complete; live OAuth round trip verified |
| S3-compatible adapter | Complete, **verified against a live Cloudflare R2 bucket** |
| HTTP API surface | Complete |
| Go and TypeScript SDKs | Complete, tested |
| Drive resumable upload / download / delete | Complete, **verified against a real Drive account** |
| Reconciliation | Implemented; Drive's outcome is honestly `unknown` by design |
| Job workers, leases, orphan sweep | Implemented; a worker was observed claiming and completing a real job |
| Metrics (`/metrics`, Prometheus) | Implemented; 12 families served by the running binary |
| Drive change-log poller | Implemented; cursor advancing against a live account |
| Testing console at `/console` | Implemented; backend verified against real R2, **the page has not been clicked in a browser** |
| Forced-fallback operator switch | Implemented; verified with a real R2 upload while Drive was healthy |
| Redis-backed session store | Implemented, **never run against a real Redis** |

### What has actually been verified

This distinction matters more than the feature list, so it is stated plainly:

- **Verified live against a real provider:** the R2 fallback round trip (upload planned,
  bytes transferred, verified with `HeadObject`, read back, deleted), Google OAuth
  consent and token refresh, Drive folder creation, the Drive change-log cursor, and the
  background worker's claim-and-complete cycle.
- **Verified against a real Drive account:** an upload lands in the `Storvia/` folder, the
  bytes read back identical, and the delete works — and with Drive forced unavailable the
  same upload routes to R2 instead. Both directions are covered by `make test-live-drive`.
- **Verified against real R2 through the console:** forcing the fallback uploads to R2
  even while Drive is healthy, the bytes come back identical, and the routing decision is
  visible in the reported state.
- **Never run at all:** the PostgreSQL migrations (all testing is SQLite), and the Redis
  session store (only a failing stub).

See [`CHANGELOG.md`](CHANGELOG.md) for exactly what is missing and
[`docs/implementation-plan.md`](docs/implementation-plan.md) for milestone status.

---

## The testing console

`/console` is the operator surface. It replaces an earlier `/admin` panel that could show
a connection status and nothing else — it could not perform an upload, so it could not
answer the question it existed to answer.

```bash
./scripts/dev.sh start          # starts the API and opens http://127.0.0.1:8080/console
```

Sign in with the Google account whose Drive you want to use. You stay signed in until you
press **Sign out**; the console's session lasts 30 days and is not tied to the browser
process.

What it does:

- **Upload a file** and see where it actually landed — the provider, the provider-side
  reference, the bytes transferred, and the elapsed time. This runs the whole client
  sequence (create, transfer, verify, commit), including the resumable `PUT` to Google
  that an SDK performs. With Drive connected, files go to Drive.
- **Download and delete** any stored object, so an upload can be verified rather than
  assumed.
- **See the Drive connection**, create the `Storvia/` folder, or disconnect.
- **Force the fallback provider** — three states: force it, prefer Drive, or follow
  `fallback.force` from `config.json`.
- **Simulate a Drive outage.** Off by default, so uploads use the real Drive path.

Google Drive is the upload default. Uploads only go elsewhere when Drive is unavailable
*and* you have accepted the outage-fallback disclosure — an operator switch cannot write
a file outside Drive without that consent, and cannot move bytes that may already have
reached Drive.

The console has no build step and no external assets.

---

## Requirements

- Go 1.27 or newer
- Node.js 20 or newer, for the TypeScript SDK
- A Google Cloud project with the Drive API enabled
- Optionally: PostgreSQL for production, Redis for multi-instance deployments
- Optionally: an AWS S3 or Cloudflare R2 bucket for outage fallback

## Quick start

```sh
git clone https://github.com/shovan04/storvia.git
cd storvia

cp .env.example .env.development
# fill in the values; .env.example documents how to generate each one

make check-config   # validate without serving
make run            # serve on 127.0.0.1:8080
```

```sh
curl http://127.0.0.1:8080/healthz
# {"status":"ok"}
```

The service starts and serves health checks even when Google credentials are absent,
reporting the gap through `GET /v1/storage/status` rather than refusing to boot. A
container that cannot start cannot explain why it is unready.

### Container

```sh
docker build --target runtime --build-arg VERSION=1.0.0 -t storvia-api .

docker run --rm --env-file .env.production -p 8080:8080 storvia-api
```

The image is three stages: build, test, runtime. Only the runtime stage ships, and it
is `distroless/static` — no shell, no package manager, running as a non-root user.
Migrations are compiled into the binary, so there is no schema directory to copy and
no chance of shipping a stale one.

Secrets are supplied at run time only. Never pass one as a build argument or `COPY`
it into the image: both leave a readable layer behind.

A full local stack with Postgres and Redis:

```sh
docker compose up --build
```

This deliberately runs Postgres rather than SQLite. The two dialects differ in the
job-lease query, identifier handling, and boolean representation, so the cheapest way
to catch a migration that works locally and fails in production is to develop against
the real thing.

> The `Dockerfile` has **not been built**. No Docker daemon was available in the
> environment where it was written. It is written against Go 1.27.1 and the module's
> verified properties — the static build and the embedded migrations were both
> confirmed directly — but treat the first `docker build` as a check to run, not a
> step already done.

### Running it locally

```sh
scripts/dev.sh start      # build, start with logging, open the admin panel
scripts/dev.sh stop       # stop it
scripts/dev.sh status     # is it up, where are the logs
scripts/dev.sh logs       # follow the log; Ctrl-C stops following, not the server
```

`make dev`, `make dev-stop`, `make dev-status`, and `make dev-logs` do the same.

It exists because the manual sequence is easy to get subtly wrong. In particular a
stale process holding port 8080 makes the *new* binary look broken, which has cost
real debugging time more than once in this project. So the script:

- refuses to start when something it did not launch holds the port, and names that
  process rather than killing a stranger
- validates the configuration before starting, so a broken config cannot leave a
  half-running process behind
- waits for `/readyz` and tails the log if the process dies during startup
- restores anything it changed when you stop it

Options: `--fresh` for a new local database, `--no-open` to skip the browser, and
`--faults-off` to turn off Drive outage simulation so uploads use the real Drive path
instead of the fallback. The last one rewrites the config while running and **restores it
on `stop`**, so the flag cannot silently persist.

Secrets come from the gitignored `.env.development` and are never echoed.

### Configuration

Configuration is split by secrecy, and the split is absolute:

- **JSON** (`config/*.json`) holds every ordinary setting and **nothing else**:
  timeouts, TTLs, booleans, size limits, bucket names, regions, prefixes, and redirect
  URIs. There is no secret in it — not a value, and not a field that names one. It also
  holds no account-specific endpoint, so the same file works for every deployment of a
  given version.
- **`.env`** holds every actual credential — API keys, OAuth client credentials, access
  keys, secret keys, passwords, the refresh-token encryption key, and the session-signing
  key — plus the account-specific S3 endpoint (`STORVIA_R2_ENDPOINT`), which is not a
  secret but does change per deployment. `.env` files are never committed.

The endpoint variables are chosen by vendor (`STORVIA_R2_ENDPOINT`,
`STORVIA_AWS_ENDPOINT`) for the same reason the credentials are. A backend may still
name an explicit `endpoint` in its own JSON entry when two backends of one vendor point
at different endpoints.

A checked-in config file is read, reviewed, and committed by people who should never
have to think about secrets. Leaving a `client_secret_ref` in it means the file is no
longer unambiguously clean, and a reviewer has to decide what it means. So it is not
there. The variable names the code reads are a convention, declared in
`internal/config/secrets_names.go`.

This is why a secret-shaped field must be split rather than moved. An earlier version
read a whole Postgres connection string from the environment; because the string
contained a password, four ordinary settings were quietly pushed into `.env` too. The
current shape keeps the ordinary parts in the file:

```json
{
  "database": {
    "driver": "postgres",
    "host": "postgres",
    "port": 5432,
    "user": "storvia",
    "database": "storvia",
    "sslmode": "require"
  }
}
```

Host, port, user, and database name are not secrets, so they are plain values here.
`STORVIA_DATABASE_PASSWORD` is read from the environment and the DSN is assembled at
runtime. Nothing about where the password lives needs to appear in this file.

The rule is enforced by tests against the real committed files, not by code review:

- **A missing required secret stops the service.** It does not become an empty
  credential. The error names the field and the variable.
- **A secret-bearing key in a JSON file fails the build.** A test walks both shipped
  files and rejects any `_ref` field, any secret-bearing key, and any value shaped
  like a credential.
- **A credential-shaped value is rejected anywhere it appears**, and the error never
  reprints the value it rejected.

The loader also refuses configuration that would be unsafe: the broad `drive` scope, a
non-allow-listed endpoint, a retry budget that cannot fit its own attempts, and
enabling fallback while the outage disclosure is unaccepted.

### Outage fallback consent

When Drive is unavailable, a new upload may be routed to the fallback provider instead.
That means a file can be stored outside the user's own Drive, so it only happens when the
**tenant has accepted the outage-fallback disclosure**. It is a per-tenant record, not a
deployment switch: consent is not transferable between tenants and a deployment cannot
imply it on someone's behalf.

```sh
GET    /v1/storage/fallback/consent   # current state
POST   /v1/storage/fallback/consent   # accept the current disclosure
DELETE /v1/storage/fallback/consent   # withdraw; closes the gate immediately
```

A tenant that has never been asked and a tenant that declined both read as *not granted*,
because both mean the same thing to the router. The record carries the disclosure version
the tenant agreed to, so when the wording changes they are asked again rather than being
held to terms they never saw.

Withdrawal is the direction that has to work instantly — it is what stops a tenant's files
being routed somewhere new.

Enabling `fallback.enabled` in the configuration does **not** grant consent. It only says
where files would go if the tenant agreed.

### Google OAuth setup

1. Create a Google Cloud project and enable the Drive API.
2. Create an OAuth client of type **Web application**.
3. Add both exact redirect URIs:
   `https://your-host/v1/storage/connections/google-drive/callback`
   and, for local development, `http://127.0.0.1:8080/admin/oauth/callback`
   (`http://127.0.0.1:8080/console/oauth/callback` also works and is the current path;
   both are accepted so the console can be moved without breaking sign-in for a project
   that still has the older URI registered — Google refuses a `redirect_uri` that is not
   registered, so the registered one must keep working)
   It must match character for character, including any trailing slash.
4. Set `STORVIA_GOOGLE_CLIENT_ID` and `STORVIA_GOOGLE_CLIENT_SECRET`.

Storvia requests only `https://www.googleapis.com/auth/drive.file`, a non-sensitive
per-file scope covering files the app creates. That scope is enforced at
construction; a config naming a broader scope fails to load.

### Development admin panel

The development configuration enables a local admin panel at
`http://127.0.0.1:8080/admin`. It is disabled by configuration validation outside the
`development` environment. The panel uses a browser-bound OAuth state, exchanges the
Google authorization code server-side, and then issues a normal Storvia session for
the configured development tenant/user. It displays only connection metadata and
never displays access tokens, refresh tokens, authorization codes, or Drive session
URLs.

To use it:

```sh
# Put these two values in the ignored .env.development file.
STORVIA_GOOGLE_CLIENT_ID=...
STORVIA_GOOGLE_CLIENT_SECRET=...
make run
open http://127.0.0.1:8080/admin
```

The panel currently tests Google Drive authorization and connection state only.
Drive folder creation, file uploads, downloads, and deletes remain unfinished.

## Using the SDKs

### Go

```go
client, _ := storvia.New(storvia.Config{BaseURL: "https://api.your-host"})

// The idempotency key must be stable across retries. A fresh key per attempt
// defeats the protection and can create a duplicate file.
result, err := client.CreateUpload(ctx, uploadKey, storvia.CreateUploadRequest{
    Name:      "report.pdf",
    MIMEType:  "application/pdf",
    SizeBytes: 10 << 20,
    Category:  "report",
})

switch result.Mode {
case storvia.ModeDriveResumableSession:
    // The session URL in result.Session is a write capability for this one file.
    // upload.UploadAll streams to it, then upload.CompleteDrive sends the file id.
case storvia.ModeS3PresignedParts:
    // Upload parts directly with result.Parts. Those URLs are bearer tokens.
}
```

### TypeScript

```ts
const client = new StorviaClient({ baseUrl: 'https://api.your-host' });

const { object } = await client.upload(
  { name: 'report.pdf', mime_type: 'application/pdf', size_bytes: file.size, category: 'report' },
  file,
  { idempotencyKey },
);
```

Both SDKs surface one machine-readable error vocabulary. `uncertain` is a distinct
kind on purpose: the write may already have committed, so the correct action is to
reconcile, never to start a new upload elsewhere.

See [`sdk/go/README.md`](sdk/go/README.md) and [`sdk/ts/README.md`](sdk/ts/README.md).

## HTTP API

Every failure returns RFC 9457 `application/problem+json`, including 4xx responses,
so a client needs exactly one error parser.

```text
GET    /healthz
GET    /readyz
GET    /metrics            Prometheus exposition

GET    /console                        Testing console (Google sign-in required)
GET    /console/api/state              Everything the console shows
POST   /console/api/upload             Upload a file end to end
POST   /console/api/routing            Force or unforce the fallback provider
POST   /console/api/faults             Simulate a Drive outage
POST   /console/api/consent            Accept or withdraw the fallback disclosure
GET    /console/api/objects/{id}/content
DELETE /console/api/objects/{id}

GET    /v1/storage/status
GET    /v1/storage/connections/google-drive
POST   /v1/storage/connections/google-drive/start
GET    /v1/storage/connections/google-drive/callback
DELETE /v1/storage/connections/google-drive

POST   /v1/storage/uploads
GET    /v1/storage/uploads/{upload_id}
POST   /v1/storage/uploads/{upload_id}/complete
POST   /v1/storage/uploads/{upload_id}/reconcile
DELETE /v1/storage/uploads/{upload_id}

GET    /v1/storage/objects/{object_id}
GET    /v1/storage/objects/{object_id}/content
DELETE /v1/storage/objects/{object_id}

GET    /v1/storage/fallback/consent
POST   /v1/storage/fallback/consent
DELETE /v1/storage/fallback/consent
```

### Upload modes

`POST /v1/storage/uploads` answers with a `mode`, and the two are deliberately
different shapes because they are genuinely different mechanisms.

**`drive_resumable_session`** — Storvia asks Google for a resumable session and returns
its URI in `session.url`. The client `PUT`s each chunk straight to Google, declaring a
`Content-Range` for every one, and then reports the file id Google returned in the final
chunk response:

```json
{ "drive_file_id": "1AbC..." }
```

**`s3_presigned_parts`** — `parts[].url` are short-lived, operation-scoped URLs for the
fallback bucket. No file bytes pass through Storvia in either mode.

The session URL is a **write capability for that one file**. Storvia returns it once,
never stores it, and never replays it on an idempotent repeat. The file id reported at
completion is **untrusted input**: it is re-read from Drive and adopted only if it is
inside the Storvia folder, is not trashed, and has the declared size.

There is no proxy mode. File bytes never traverse the API, so there is nothing for the
server to forward and no chunk endpoint exists.

Object and upload identifiers are opaque and generated by the server. A client can
never supply a tenant, a provider, a Drive file ID, a bucket, or an object key as an
authority claim.

## Security model

- **Tenant isolation is structural, not advisory.** Repositories require a verified
  tenant scope, so a query without one does not compile. A cross-tenant read returns
  not-found rather than forbidden, which prevents the endpoint from being used to
  discover which IDs exist.
- **The single-location invariant is enforced by the database.** `CHECK` constraints
  reject a ready object holding both a Drive file ID and an S3 key, or whose verified
  size disagrees with the declared one.
- **Secrets are encrypted before they touch the database.** Refresh tokens are sealed
  with AES-256-GCM and decrypted only inside the Drive adapter, so no other layer
  holds a usable token. Rotation overlaps, so a rotation does not sign every user out.
- **Logging redaction is enforced in the handler,** not requested from callers.
  Tokens, secrets, session URIs, and signatures are replaced before any record is
  written, including nested inside groups.
- **Capabilities stay short-lived.** Presigned part URLs are scoped to one part,
  returned once, never logged, never persisted.
- **The access log records paths only,** because a presigned capability can arrive in
  a query string.

Before deploying, read [`docs/risk-register.md`](docs/risk-register.md). It documents
the failure modes, the privacy checklist, and an explicit go/no-go list — including
the conditions under which this design is the wrong choice.

## Development

```sh
make help            # list every task
make check           # gofmt, go vet, go test
make check-all       # adds the race detector
make test-cover      # coverage summary
make sdk-ts-test     # TypeScript SDK tests
make deps            # tidy and verify modules
```

Layout:

```text
cmd/storvia-api/     API entry point
internal/
  apperr/             shared error vocabulary
  auth/               sessions and tenant identity
  cache/              TTL interface, in-memory and Redis
  config/             JSON config and .env loading
  httpapi/            routes, middleware, global error handler
  logger/             structured logging with redaction
  persistence/        repositories, migrations, dialects
  secrets/            token encryption
  storage/
    model/            domain types
    policy/           routing engine: classification, budgets, circuit
    service/          use cases
    provider/drive/   OAuth, connection registry, availability
    provider/s3compat/  S3-compatible adapter
migrations/           per-dialect SQL
sdk/go/  sdk/ts/      client SDKs
docs/                 policy, architecture, plan, risks, references
```

The routing policy engine in `internal/storage/policy` is pure: no network, no
database, no clock of its own. Every input is passed in, which is what lets the whole
error-classification table be tested as plain unit tests. If you change routing
behaviour, that package and its table-driven tests are the place to change it.

## Contributing

Issues and pull requests are welcome. Start with
[`CONTRIBUTING.md`](CONTRIBUTING.md) for setup and the checks, and
[`AGENTS.md`](AGENTS.md) for the working rules this project is held to.

Two things to know before you change the routing behaviour:

- The storage contract in [`docs/storage-policy.md`](docs/storage-policy.md) is
  load-bearing. Changes should come with table-driven tests in
  `internal/storage/policy` that mirror the document row for row, so the code and
  the document cannot drift apart.
- Every meaningful change appends an entry to [`CHANGELOG.md`](CHANGELOG.md), and no
  unrun check is ever described as passed.

**Found a security problem?** Do not open a public issue. See
[`SECURITY.md`](SECURITY.md).

## Documentation

| Document | Purpose |
|---|---|
| [`CHANGELOG.md`](CHANGELOG.md) | What changed, what broke, what was verified, what is missing |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Setup, checks, and the two project rules to respect |
| [`SECURITY.md`](SECURITY.md) | Reporting a vulnerability privately; known limitations |
| [`docs/storage-policy.md`](docs/storage-policy.md) | The confirmed storage contract and error classification |
| [`docs/architecture.md`](docs/architecture.md) | Components, schema, API, lifecycle |
| [`docs/implementation-plan.md`](docs/implementation-plan.md) | Milestones, acceptance status, checks run |
| [`docs/risk-register.md`](docs/risk-register.md) | Failure modes, security controls, go/no-go |
| [`docs/references.md`](docs/references.md) | Google and Cloudflare API references, bounded community evidence |

## Licence

Apache License 2.0. See [`LICENSE`](LICENSE).

Apache-2.0 rather than MIT because this project integrates with third-party storage
APIs, and its explicit patent grant covers an implementation someone might deploy
commercially.

The dependency licences are worth reviewing before you deploy: the AWS SDK for Go v2
is Apache-2.0, `pgx` is MIT, `modernc.org/sqlite` and `go-redis` are BSD-3-Clause.
Run `make deps` and review your own dependency tree for your situation.
