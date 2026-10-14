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
| Google OAuth (consent, token refresh, revocation) | Complete, tested against stubs |
| S3-compatible adapter | Complete, **never run against a real bucket** |
| HTTP API surface | Complete; transfer endpoints return `provider_not_configured` |
| Go and TypeScript SDKs | Complete, tested |
| Drive resumable upload / download / delete | **Not implemented** |
| Reconciliation and job workers | **Not implemented** |

No code path has been exercised against a live Google account, a real S3 or R2
bucket, or PostgreSQL. See [`CHANGELOG.md`](CHANGELOG.md) for exactly what is missing
and [`docs/implementation-plan.md`](docs/implementation-plan.md) for milestone status.

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

### Configuration

Configuration is split deliberately:

- **JSON** (`config/*.json`) holds structure and non-secret values. Safe to review
  and commit.
- **`.env`** holds every API key, password, and signing material. Never committed.

A JSON string of `${VAR}` or `${VAR:-fallback}` resolves from the environment at
load time. A reference with no value and no default is a startup error, so a missing
key fails loudly instead of becoming an empty credential.

```json
{
  "drive": {
    "client_id_ref": "STORVIA_GOOGLE_CLIENT_ID",
    "client_secret_ref": "STORVIA_GOOGLE_CLIENT_SECRET"
  }
}
```

The loader refuses configuration that would be unsafe: the broad `drive` scope, a
non-allow-listed endpoint, a retry budget that cannot fit its own attempts, and
enabling fallback while the outage disclosure is unaccepted.

### Google OAuth setup

1. Create a Google Cloud project and enable the Drive API.
2. Create an OAuth client of type **Web application**.
3. Add your exact redirect URI:
   `https://your-host/v1/storage/connections/google-drive/callback`
   It must match character for character, including any trailing slash.
4. Set `STORVIA_GOOGLE_CLIENT_ID` and `STORVIA_GOOGLE_CLIENT_SECRET`.

Storvia requests only `https://www.googleapis.com/auth/drive.file`, a non-sensitive
per-file scope covering files the app creates. That scope is enforced at
construction; a config naming a broader scope fails to load.

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
case storvia.ModeDriveProxy:
    // Stream chunks through the API; the Drive session URI stays server-side.
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
GET    /metrics

GET    /v1/storage/status
GET    /v1/storage/connections/google-drive
POST   /v1/storage/connections/google-drive/start
GET    /v1/storage/connections/google-drive/callback
DELETE /v1/storage/connections/google-drive

POST   /v1/storage/uploads
GET    /v1/storage/uploads/{upload_id}
PATCH  /v1/storage/uploads/{upload_id}/chunks
POST   /v1/storage/uploads/{upload_id}/complete
POST   /v1/storage/uploads/{upload_id}/reconcile
DELETE /v1/storage/uploads/{upload_id}

GET    /v1/storage/objects/{object_id}
GET    /v1/storage/objects/{object_id}/content
DELETE /v1/storage/objects/{object_id}
```

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

Issues and pull requests are welcome. Please read [`AGENTS.md`](AGENTS.md) first: it
records the working rules this project is held to, including the requirement that
every meaningful change appends an entry to [`CHANGELOG.md`](CHANGELOG.md) and never
describes an unrun check as passed.

The storage contract in [`docs/storage-policy.md`](docs/storage-policy.md) is
load-bearing. Changes to it should come with table-driven tests in
`internal/storage/policy` that mirror the document row for row, so the code and the
document cannot drift apart.

## Documentation

| Document | Purpose |
|---|---|
| [`CHANGELOG.md`](CHANGELOG.md) | What changed, what broke, what was verified, what is missing |
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