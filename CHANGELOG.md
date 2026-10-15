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

## 2026-10-15 — The app-managed Drive folder is created, and the connect is complete

### Why

With OAuth finally working, the admin panel reported the connection as `connected`
with subject `13735161528780792640` and scope `drive.file`, but then:

```
Storvia folder    not created yet
```

Every Drive upload was refused by design: `usableConnection` requires a known folder
and reports `ClassConfiguration` without one. Folder creation had never been
implemented.

### Added

**`internal/storage/provider/drive/folders.go`** — `EnsureRootFolder` (create or find)
and `FindRootFolder` (find only). The folder is created **at connection time** rather
than lazily on the first upload, because failing the first upload for a reason the user
cannot act on is the worst moment to discover a missing folder.

Folder creation failure is logged but does not fail the callback: the grant is stored
and the account is connected, so discarding a working authorization would force the user
to consent again.

**Recovery policy, from `docs/architecture.md`, implemented as written:**

- If more than one folder carries the Storvia name, the ambiguity is **reported, not
  resolved**. Picking one would silently split the user's files across two places;
  creating a third would make the ambiguity permanent. The candidate ids are returned
  so a person can be asked.
- Trashed folders are excluded from discovery. Reusing one would write into the user's
  trash.
- `FindRootFolder` never creates. If the user deleted the folder, the honest answer is
  that it is gone and the fix is an explicit action.

**`POST /admin/ensure-folder`** plus a panel button shown when the folder is missing.
Connections made before this existed have no folder, and re-running it must be something
a person decides.

`driveAboutURL` is a separate constant for the same reason `driveUploadURL` is: `about` is
not under `/files`.

### Verified against a real Google account

```
POST /admin/ensure-folder -> 303
root_folder_id: (empty) -> 1R5CuK1CDTr2XKFcyy2xsXmpNVsot6W1i
```

Run a second time, the id is unchanged — discovery found the existing folder rather than
creating a duplicate. This is the **first write this project has ever made to Google
Drive**.

### Tests

Five new tests: reuse-existing, create-when-absent, refuse-to-choose-between-duplicates,
find-never-creates, and folder-work-tolerates-a-missing-folder-id. That last one guards a
deadlock — folder work must work on a connection whose folder id is empty, since that is
how the id gets set.

`internal/storage/provider/drive` coverage: 46.8% → 61.5%.

### Checks run

```
make check / check-all, sdk-ts-test (22), test-live (both live R2 tests)
```

## 2026-10-15 — The token response was parsed as form data, not JSON

### Why

Still failing after the identity fix, and this time the newly added logging named the
cause: `drive: the token response contained no access token`.

### Cause

Google's token endpoint answers with:

```
content-type: application/json; charset=utf-8
```

The response was parsed with `url.ParseQuery`, which is form encoding. A successful
response therefore produced an empty value set, and the exchange failed while the access
token sat in the body in plain sight.

This broke **both** paths that shared the parser: the authorization-code exchange *and*
every subsequent refresh. Nothing had ever got far enough to exercise either.

`parseFormURL` is now `parseTokenResponse`. JSON is tried first because that is what
Google sends; the form encoding RFC 6749 permits is kept as a fallback, since it costs
three lines and removes a class of surprise.

Numbers are rendered as strings because the downstream duration parser expects
`expires_in` as text. That is deliberate, and tested: losing `refresh_token` would make
a connection appear to work once and then fail forever.

### The first failure in the same session

An earlier attempt logged `the authorization request could not be verified`. That is the
CSRF state check rejecting a stale value — correct behaviour, caused by the server having
been restarted between `/admin/connect` and the callback, which invalidates the in-memory
state store. It was the previous binary's browser tab, not a new defect.

### Tests

`TestParseTokenResponseReadsGoogleJSON` and
`TestParseTokenResponseStillAcceptsFormEncoding`.

### Not verified

Still no observed successful connect. Two consecutive real defects have now been found by
the logging added after the first, which is the argument for keeping it.

## 2026-10-15 — Connecting a Google account failed after consent

### Why

The admin panel sent the user to Google's consent screen, the user approved, and the
callback answered `502 Google Drive authorization could not be completed.` for every
account. The failure was after consent, which is the worst moment for it.

### Fixed: the account was identified through the wrong Google API

After the code was exchanged, Storvia fetched the user's identity from **OpenID
Connect's userinfo endpoint**:

```go
GET https://openidconnect.googleapis.com/v1/userinfo
```

That endpoint requires an `openid` grant. Storvia requests only `drive.file`, by
design, so the access token was always rejected there with a 401 — for every user, on
every attempt.

Identification now uses **Drive's own `about` endpoint**, which is inside the
`drive.file` scope and so costs no additional permission:

```go
GET https://www.googleapis.com/drive/v3/about?fields=user
```

`driveAboutURL` is a separate constant because `about` is **not** under `/files`;
appending it to `driveFilesURL` would produce `/drive/v3/files/about`, which is not a
route — the same class of mistake as the `/files/files` upload URL fixed earlier.

The subject is Google's `user.permissionId`, a stable per-account identifier. The email
address is only a fallback; it is personal data Storvia has no reason to store.

### Fixed: the callback discarded the reason entirely

This is why the bug was hard to find. `AdminHandler.Callback` rendered a generic message
and **logged nothing**, so a scope mismatch looked identical to a bad code, a replayed
state, and a provider outage.

The handler now records the cause and the failing stage. The user-facing message is still
generic, because the cause can carry provider detail — but that is only safe if it is
recorded somewhere. The page now also points at the log.

### How it was found

The token endpoint was probed directly with the real client credentials and a deliberately
invalid code:

```
admin redirect URI -> invalid_grant: Malformed auth code.
api   redirect URI -> invalid_grant: Malformed auth code.
```

`invalid_grant` rather than `invalid_client` or `redirect_uri_mismatch` proves the client
ID, the client secret, **and both registered redirect URIs are correct** — which ruled
out the whole configuration and pointed at the code after the exchange.

### Tests

- `TestFetchSubjectUsesDriveAboutNotOpenIDConnect` asserts the exact path, that it is not
  under `/files`, and that the subject is the permission id.
- `TestFetchSubjectReportsWhyItFailed` requires the provider's reason to survive.
- `TestAdminCallbackLogsTheRealFailureReason` and
  `TestAdminCallbackLogsAMissingFlowCookie` require the handler to log the cause and
  stage while keeping it off the error page.

### Not verified

The fix has **not** been confirmed against a real access token — that needs a browser
consent round trip. The diagnosis is certain (an `openid` grant is required for userinfo
and Storvia does not request one), and Drive's `about` endpoint was confirmed to exist,
but a successful connect has not yet been observed.

## 2026-10-15 — Development launcher

### Why

Starting the service by hand — build, run, watch the log, open a browser, stop it — is
easy to get subtly wrong, and one failure mode in particular has cost real debugging
time more than once: **a stale process holding port 8080 makes the newly built binary
look broken.** That was misdiagnosed as a routing fault before it was identified as an
old process.

### Added

`scripts/dev.sh`, with `make dev`, `make dev-stop`, `make dev-status`, and `make dev-logs`.

```
scripts/dev.sh start      build, start with logging, open the admin panel
scripts/dev.sh stop       stop it and restore anything that was changed
scripts/dev.sh restart    stop then start
scripts/dev.sh status     report what is running and where the logs are
scripts/dev.sh logs       follow the log (Ctrl-C stops following, not the server)
```

Options: `--fresh` (new local database), `--no-open`, `--faults-off`.

### What it refuses to do

- **Start over a process it did not launch.** It names the holder of port 8080 and asks,
  rather than killing a stranger.
- **Start with a broken configuration.** The loader runs first with `-check`, so an
  invalid config fails before any process exists to clean up.
- **Claim success when the API died.** It waits for `/readyz` and tails the log if the
  process exits during startup.

`--faults-off` rewrites `simulate_drive_outage` so uploads take the real Drive path, and
**restores the file on `stop`**. An override that silently persisted would make a later
run quietly test the wrong thing.

Output goes to `logs/`, which is gitignored along with the binary and the pid file.

### Checks run

Every command was exercised rather than assumed: start from a clean database, start
again while running (correctly refused), stop, stop twice (idempotent), restart, status
before and after, a missing secrets file, and a deliberately invalid configuration —
which the validation gate rejected before starting. A stale port holder was simulated
and correctly named.

## 2026-10-15 — System-wide bug sweep, and the last stub endpoints removed

### Why

An instruction to fix everything found while testing the whole system. This entry
records what was actually broken, including two bugs in code written earlier the same
day.

### Fixed: the Drive upload URL was wrong

```go
driveUploadURL = "https://www.googleapis.com/upload/drive/v3/files"  // already ends in /files
endpoint       = driveUploadURL + "/files?uploadType=resumable"      // -> /files/files
```

Every resumable upload would have been rejected by Google with a 404. It was written
and compiled an hour before it was caught, and only the new test suite found it, because
nothing had yet called the real endpoint.

**Lesson:** a constant ending in a resource name, concatenated with that same resource
name, is a mistake waiting for someone to notice. The test now asserts the exact path.

### Fixed: the admin connect flow could never complete

`oauth2.googleapis.com/auth` is not a route. Google serves a 404 page, so the browser
landed on it. Google splits its OAuth endpoints across two hosts:

```
accounts.google.com/o/oauth2/v2/auth   consent page   (was wrong)
oauth2.googleapis.com/token            code exchange  (correct)
oauth2.googleapis.com/revoke           revoke         (correct)
```

A test already existed, `TestAuthorizeURLCarriesTheRequiredParameters`, asserting six
query parameters. All six were right. **It never asserted the host**, which is exactly how
a wrong destination passes. A test name implying coverage is not coverage.

### Fixed: a tenant with no Drive connection got an internal error

`drive.Client.ConnectionID` documented that it returns `""` when no connection exists but
propagated `ErrNoConnection` as an error, making the coordinator's `connectionID == ""`
branch unreachable. A tenant who had never connected Drive received a wrapped internal
error from `CreateUpload` instead of a routing decision.

### Fixed: the development tenant was never created

`POST /admin/fallback-consent` returned 502. Consent, connections, objects, and uploads
all reference the tenant, and nothing inserted the row the admin panel names in
configuration. The panel rendered perfectly and then failed on the first write.
`TenantRepository.EnsureTenant` creates it during `Build`.

### Removed: the proxy chunk endpoints

`ModeDriveProxy`, `PATCH`/`POST .../chunks`, and `UploadHandler.Chunks` are gone. A
resumable session makes proxying impossible: the server holds no copy of the bytes, so
there is nothing to forward. An endpoint that could only ever answer "not implemented"
advertises a capability that does not exist.

### Added: reconciliation

`POST /v1/storage/uploads/{id}/reconcile` previously answered "Reconciliation not
implemented". It now asks the provider what actually landed and reports one of
`already_ready`, `completed`, `nothing_stored`, `size_mismatch`, or `unknown`. It never
retries and never re-uploads.

The distinction between `nothing_stored` and `unknown` is the point. Only the first tells
a client it may start over. For a Drive upload the answer is always `unknown`, because
Storvia deliberately does not hold the resumable session and therefore cannot ask Google
— and reporting `nothing_stored` would push a client into creating a duplicate file.

### Added: Drive download and delete

`contentSource` returned "not implemented" for both despite the verbs existing. Wired
to `DownloadFile` and `DeleteFile`, with the same rule as everywhere else: the recorded
provider only, never "try the other one".

### SDKs rewritten for the session flow

Both SDKs implemented the proxy flow, which is now impossible, and the TypeScript client
decoded the complete response — `{object, already_complete}` — as a bare object.

Go SDK: `UploadAll` streams to the Drive session in 256 KiB chunks, each declaring its
own `Content-Range`; `CompleteDrive` sends the reported file id; `ReconcileUpload`
returns a typed outcome.

TypeScript SDK: the same flow via `fetch`, plus `DriveSession`, `ReconcileResult`, and a
corrected `completeUpload`.

### Tests

| Area | Before | After |
|---|---|---|
| `internal/storage/provider/drive` | 25.9% | 46.8% |
| `internal/httpapi` | 36.8% | 46.0% |

The drive tests use a `RoundTripper` that intercepts every request, because the provider's
endpoints are absolute constants pointing at googleapis.com and a `httptest.Server` alone
would have let the suite talk to the real Google.

New: 13 drive file-verb tests, 7 reconcile tests, 6 SDK chunking tests, 10 HTTP
integration tests driving the real handlers over a real database.

The HTTP tests caught two more things. The `storage_objects.drive_connection_id` foreign
key rejected a create whose connection did not exist — the constraint doing its job — and
handlers read `PathValue`, which only a `ServeMux` populates.

### Checks run

```
make check / check-all   # fmt, vet, test, -race — passed
make sdk-ts-test         # 22 pass
make test-live           # both live tests pass against real R2
```

Server started against a fresh database and every route probed: `/admin` 200, `/healthz`
and `/readyz` 200, the API 401 without a session, consent POST 303 from the panel, and the
panel reporting the R2 backend reachable.

### Still not verified

No live Google Drive request has ever run. The Drive upload path, the consent round trip,
and the folder creation are exercised only against stubs. PostgreSQL still has never been
executed.

## 2026-10-15 — Drive resumable uploads, and the Google authorization endpoint was wrong

### Why

The admin panel could not connect an account: the authorization redirect 302'd to
`https://oauth2.googleapis.com/auth`, which is not a route. Google serves a 404 error
page, so the browser landed on it and the flow simply failed.

### Fixed: the authorization endpoint did not exist

```go
authBase     = "https://oauth2.googleapis.com"
authorizeURL = authBase + "/auth"   // 404 - not a route
```

Google splits its OAuth web endpoints across two hosts, and the split is not obvious:

```
accounts.google.com/o/oauth2/v2/auth   - the consent page       (was wrong)
oauth2.googleapis.com/token            - exchanging the code   (was correct)
oauth2.googleapis.com/revoke           - revoking a token      (was correct)
```

Verified by probe: `accounts.google.com/o/oauth2/v2/auth` answers 302, while
`oauth2.googleapis.com/auth` answers 404. The token and revoke endpoints were checked
the way they are actually used and are fine.

**Why no test caught it.** `TestAuthorizeURLCarriesTheRequiredParameters` already existed
and checked six query parameters. All six were correct. It never asserted the **host**,
which is why a wrong destination passed. A test that checks the query but not where the
request goes is the exact shape that lets this through. `endpoints_test.go` now asserts
the host and path on the constant and through the real builder.

**This means the admin connect flow could never have completed**, not merely that it was
untested.

### Added: Drive file transfer

Drive had OAuth and connection management and no way to move a byte.

- **`internal/storage/provider/drive/files.go`** — `InitiateUpload` (creates a resumable
  session against the app-managed folder only), `FetchFile`, `DownloadFile`,
  `DeleteFile`, and `UploadState`, which reads back how many bytes Drive accepted so a
  partial upload is detectable rather than assumed.
- **New mode `drive_resumable_session`.** Drive has no S3-style presigned URLs; its
  equivalent is a resumable session that Drive mints and Storvia relays. The client
  PUTs chunks straight to Google and no file content traverses Storvia.

Drive requires every chunk except the last to be a 256 KiB multiple. `DriveResumableChunkMultiple`
states that and `ChunkSizeIsValid` checks it, because a granularity Drive rejects
partway through an upload is the worst place to discover the mistake.

### A decision worth stating: the session URL is not persisted

The session URI is a bearer capability — anyone holding it can write to that one file.
It is returned once, never stored, never logged, and never replayed: a repeated
idempotent request gets the existing upload back without the capability. The test
`TestDriveSessionIsNotStoredDurably` exists to keep it that way.

That has a consequence. Storvia cannot learn the resulting file ID on its own, so the
client reports it at completion. **That ID is untrusted input** and is treated as a
lookup key: `CompleteDriveUpload` re-reads the file from Drive and adopts it only if it
is inside the app-managed folder, is not trashed, and has the declared size. Without the
folder check a client could point an upload at any other file in its account that
happened to be the right size and Storvia would adopt it as its own.

Completion refuses a size mismatch as `uncertain` rather than committing, because a
truncated upload that became "ready" would only be discovered when someone tried to open
the file.

### Added: a development testing dashboard

The old panel showed connection state and said "file transfer is not implemented yet".
It now reports what the deployment can **actually do**, which required measuring rather
than reading configuration — the distinction that matters, and the one that hid three
inert dependencies in this session.

It shows: the fault-injection banner, fallback enabled/vendors, **tenant consent** with
an inline accept button, one row per backup provider with a **real reachability probe**
against the provider, and stored-object counts.

When consent is missing it explains why: configuration alone does not grant it, because
that moves files outside the user's own Drive.

### Fixed: the development tenant was never created

`POST /admin/fallback-consent` returned 502. Every scoped row — consent, Drive
connections, objects — references the tenant, and nothing ever inserted the row the
admin panel names in configuration. `TenantRepository.EnsureTenant` now creates it during
`Build` when the panel is enabled. The panel looked fine right up until the first write,
which is the least useful moment to find out.

### Checks run

```
make check / check-all   # fmt, vet, test, -race — passed
make test-live           # both live tests pass against real R2
make sdk-ts-test         # 19 pass
```

The panel was verified against a running server: consent went from "never asked" to
"granted", and the R2 probe reported the provider reachable.

## 2026-10-15 — Fallback endpoints moved out of the checked-in configuration

### Why

The owner asked for the account-specific endpoint to live in `.env` rather than in the
committed JSON, alongside the credentials.

This is not a secret — an endpoint is a hostname — but the reasoning holds: it is a
property of the account you deploy into, so every line of it in a checked-in file is a
line someone has to edit again when they move it. With it in the environment, the
committed configuration is identical for every deployment of a given version.

### Changed

- **`endpoint` removed from both JSON files.** Every fallback backend entry is now
  `id`, `vendor`, `region`, `bucket`, `prefix`, `use_path_style`, `disabled`, and
  `multipart_abort_ttl`.
- **`STORVIA_R2_ENDPOINT` and `STORVIA_AWS_ENDPOINT`**, declared in
  `internal/config/endpoint_names.go`. Kept deliberately in a separate file from
  `secrets_names.go`: the names are not secrets, nothing redacts them, and
  `AllSecretEnvVars` does not include them. A test asserts that separation.
- **`EndpointEnvVar(vendor)`** picks the variable by vendor, matching how the
  credentials are chosen, so an R2 backend cannot be pointed at the AWS variable.
- **`resolveEndpoints`** runs during `Parse`, before validation, because the
  allow-list check has nothing to inspect until there is a value.

### Two decisions worth stating

**A missing endpoint is an error, not an empty string.** An empty endpoint would fall
back to the AWS SDK's own default, which for an R2 backend is not an R2 request at all.
The error names the variable to set.

**The JSON `endpoint` field still exists as an optional per-backend override.** Two
backends of the same vendor against different endpoints cannot both be served by one
vendor-level variable, and silently breaking that seemed worse than keeping the escape
hatch. A value in the JSON wins over the environment. Both shipped files omit it.

### Security: wildcard host allow-listing

With the account identifier gone from the configuration, the allow-list could no longer
name it literally, and `YOUR_ACCOUNT_ID.r2.cloudflarestorage.com` in a committed file is
worse than useless. Both files now carry `*.r2.cloudflarestorage.com`, `s3.amazonaws.com`,
and `*.amazonaws.com`.

A leading `*.` matches exactly one label, and `TestHostAllowedWildcardIsOneLabelOnly`
pins that down. `*.r2.cloudflarestorage.com` matches
`abc123.r2.cloudflarestorage.com` and nothing deeper, so it cannot be widened into
matching `a.b.r2.cloudflarestorage.com` or `r2.cloudflarestorage.com.attacker.test`.

### Tests

Eight new tests in `internal/config/endpoint_test.go`: endpoint read from the
environment, missing endpoint rejected by name, explicit JSON override, disabled backend
needing no endpoint, vendor-selected variable, the one-label wildcard rule including the
attacker-suffix cases, the shipped allow-list against real vendor host shapes, and
`.env.example` documenting both variables.

`TestEnvExampleHasNoOrphanVariables` now audits the endpoint variables alongside the
secrets without treating them as credentials.

### Checks run

```
make check      # fmt-check vet test — passed
make test-live  # both live tests still pass; the endpoint now comes from .env.development
```

## 2026-10-15 — Outage fallback verified against real Cloudflare R2, and three wiring bugs fixed

### Why

The goal was always to prove the outage mechanism end to end: force Drive to fail, watch
an upload land in R2. Getting there turned up three defects that made the fallback
**unreachable in any real deployment** while the entire test suite passed.

### Fixed: the fallback could never fire

Three dependencies were written, unit-tested in isolation, and then never handed to the
service that uses them. Each defaulted to a safe-looking value that quietly disabled the
feature.

| Dependency | Symptom when unwired |
|---|---|
| `FallbackConsent` | Defaulted to `return false, nil`. Every fallback was refused with `fallback_consent_missing`. |
| `FallbackUploader` | Nil. A completed fallback upload answered `provider_not_configured`. |
| `newFallbackUploader(...)` | The adapter existed and was tested; nothing ever called it. |

Every existing test passed, because the tests inject their own stubs. The configuration
said `fallback.enabled: true`, uploads went to Drive, and nothing reported a problem. This
is the worst class of bug: a feature that is configured, believed working, and inert.

`TestBuildWiresTheOutageFallbackDependencies` now asserts the real dependencies are
present after `Build` and that consent opens and closes against real storage.

### Added: tenant fallback consent

The consent gate existed as a policy condition with no way to satisfy it. There was no
consent table in either migration, so nothing could ever grant it.

- **Migration `0002_tenant_fallback_consent.sql`** for both dialects. Per tenant, not
  per deployment: consent to have files held outside the user's own Drive is not
  transferable. Carries a `disclosure_version` so a tenant is never held to wording they
  did not see, and `withdrawn_at` so a withdrawal is explicit rather than an absence.
- **`FallbackConsentRepository`** with `Resolve`, `Granted`, `Accept`, `Withdraw`.
  `Granted()` requires the flag, a matching disclosure version, and no withdrawal.
  A missing record and a refusal are distinguishable, because a caller has to tell a new
  tenant from a declining one. Both mean "not granted".
- **`ConsentService`** and **`ConsentGate`**, which adapts the store to the coordinator's
  routing dependency. The gate states once, in one place, that a lookup failure must
  never open the gate: a database problem closing the fallback shut is the safe direction.
- **`ConsentHandler`** serving `GET`, `POST`, and `DELETE /v1/storage/fallback/consent`.
- **Wired in `buildServices`**, which is the actual fix.

Four repository tests cover the default (not granted), the full lifecycle, the disclosure
version check, and tenant isolation.

### Fixed: a tenant with no Drive connection got an internal error

`drive.Client.ConnectionID` documented that it returns `""` when no connection exists, but
it propagated `persistence.ErrNoConnection` as an error. The coordinator's
`connectionID == ""` branch was therefore unreachable, and a tenant who had never
connected Drive received a wrapped internal error out of `CreateUpload` instead of a
routing decision. Both `ConnectionID` and `ConnectionUsable` now treat "no connection" as
an ordinary state.

### Verified end to end against Cloudflare R2

`make test-live`, opt-in via `STORVIA_LIVE_FALLBACK_TEST=1`:

```
routed to s3_compatible because "drive_unavailable_preflight" (1 part, mode s3_presigned_parts)
VERIFIED: Drive was forced unavailable, and 39 bytes are in R2 bucket "storvia-testing"
          at key "storvia/tenants/tenant_dev_admin/objects/upl_..."
demonstration object deleted; the bucket is clean
```

Drive was forced down with fault injection, the upload was planned against the fallback,
the bytes were transferred to the presigned URL the service issued, the upload was
completed and verified with `HeadObject`, and the object was read back out of R2 through
the provider client rather than from the database. The single-location invariant was
asserted: no Drive file ID and no Drive connection on the completed object.

### Credential resolution

The bucket was named `storvia-testing`, not the `storvia-fallback` that had been guessed.
That wrong name produced `AccessDenied`, which is returned *before* the signature is
evaluated — so the secret access key was untestable until the bucket name was right, at
which point it returned `SignatureDoesNotMatch` and pointed straight at the real problem.
Four pasted non-`STORVIA_*` lines were removed from `.env.development`; nothing read them.

### Checks run

```
make check      # fmt-check vet test — passed
make test-live  # both live tests pass against real R2
```

## 2026-10-15 — Secret references removed from the checked-in configuration

### Why

The owner has stated repeatedly that secrets belong strictly in `.env`, and that the
JSON configuration files must carry no secret keys at all. The previous boundary was
`client_secret_ref`, `encryption_key_ref`, `access_key_id_ref` and friends: field
names in a committed file whose values were environment variable *names*. The owner
considered that still too much — a checked-in config should describe ordinary
settings and nothing else.

### Changed

**The checked-in JSON now has no secret-bearing field of any kind.** Every
`*_ref` field was removed from both `config/config.development.json` and
`config/config.production.json`, and from the Go structs that decoded them
(`DatabaseConfig.PasswordRef`, `CacheConfig.RedisPasswordRef`,
`AuthConfig.SessionSigningKeyRef`, `SecurityConfig.EncryptionKeyRef`,
`SecurityConfig.EncryptionKeyPrevious`, `DriveConfig.ClientIDRef`,
`DriveConfig.ClientSecretRef`, `S3BackendConfig.AccessKeyIDRef`,
`S3BackendConfig.SecretAccessKeyRef`, `S3BackendConfig.SessionTokenRef`).

Because the loader uses `DisallowUnknownFields`, leaving any of them in a JSON file
would now be a startup error rather than a silent no-op.

**The variable names became a code convention** in `internal/config/secrets_names.go`
(`EnvEncryptionKey`, `EnvGoogleClientSecret`, `EnvR2SecretAccessKey`, and the rest),
with `AllSecretEnvVars()` so tooling and tests read one list. `SecretRefs()` now
derives every reference from those constants instead of from configuration.

**Credentials are chosen by vendor, not by backend id.** `SecretRefs()` and
`credentialNames` in `internal/app/app.go` select the R2 variables for a
`cloudflare_r2` backend and the AWS variables for an `aws_s3` backend. Previously the
backend's own `id` chose the variable names, which meant a copy-pasted configuration
could point an R2 bucket at the AWS keys with no error.

### Validation that was removed with it

`ValidateSecretRefs` previously guarded against a developer pasting a real key into a
`*_ref` field. That failure mode no longer exists — there is no field to paste into —
but the helper is kept and still tested, because it now guards the constants: a
credential-shaped string reaching `SecretRefs` (a ref that resolves to itself, say)
still fails startup rather than becoming an empty credential.

### Tests

- `TestCheckedInJSONHasNoSecretFields` (new, replaces
  `TestCheckedInJSONUsesEnvironmentReferencesForCredentials`) fails if any checked-in
  config file gains a `_ref` field, a secret-bearing key, or a credential-shaped
  value. The replaced test asserted the **opposite** — that the refs are present — so
  it was inverted rather than merely deleted.
- `TestShippedConfigContainsNoCredentials` inverted its secret-field rule: a
  secret-bearing field no longer has a legitimate form, so it is now an error
  outright.
- `TestEnvExampleHasNoOrphanVariables` and `TestEverySecretRefIsDocumentedInEnvExample`
  now compare `.env.example` against `AllSecretEnvVars()` rather than against refs
  scraped out of the JSON.
- `TestSecretEnvVarNamesAreCanonical` (new) checks every name uses the `STORVIA_`
  prefix and is a valid environment variable name.
- `TestFallbackCredentialsAreChosenByVendor` (new) resolves an R2 configuration and
  an AWS configuration against one environment and asserts neither pulls in the
  other's credentials.

### `.env.example`

Documented `STORVIA_AWS_ACCESS_KEY_ID` and `STORVIA_AWS_SECRET_ACCESS_KEY` as
commented-out optionals. They were previously invisible, so an operator running an
`aws_s3` backend had no template for them.

### Checks run

```
make check      # fmt-check vet test — passed
go vet ./...    # clean
```

`STORVIA_LIVE_FALLBACK_TEST=1 go test ./internal/app/ -run LiveFallback` fails, as
described in the next entry. That is a credential problem in the Cloudflare account,
not a code failure.

---

## 2026-10-15 — Live R2 verification attempt, and an opt-in test for it

### Why

Everything about the fallback provider had been verified against a mock. A signing
bug, a wrong endpoint, or an under-scoped token would pass every test in the suite
and only surface in production, so it needed a check that talks to a real R2 bucket.

### Added

`internal/app/live_fallback_test.go` — `TestLiveFallbackBackendIsUsable`. It writes one
object under the Storvia prefix through the real `s3compat` client, reads the metadata,
reads the bytes back, and deletes the object.

It is **opt-in**: it skips unless `STORVIA_LIVE_FALLBACK_TEST=1`. A unit suite that
reaches the network implicitly becomes slow, becomes flaky, and stops telling you
which layer broke. `make test-live` is the only supported way to run it.

It deliberately does not exercise the Drive-failure path. The routing decision is
covered deterministically by `TestInjectedDriveOutageRoutesToTheFallback`; mixing a
real provider into it would replace a precise assertion with a flaky one.

### Result: the keys reach Cloudflare and are refused

The owner populated `.env.development` with real credentials and supplied the account ID.
The 403 persisted, so this entry records what was actually established rather than a
list of things to try.

**Confirmed working:** the endpoint in `config/config.development.json` and the account ID
in the environment agree; the access key ID is 32 lowercase hex and the secret is 64 hex,
both free of whitespace and quoting; Storvia's own loader resolves them (`-check` passes).

**Confirmed not the cause:** the signing implementation. The same result was reproduced
with `boto3`, an independent SigV4 implementation, against both path-style and
virtual-hosted addressing, with and without a session token, and across five candidate
bucket names. Every combination returned `403 AccessDenied`.

**A correction to an earlier note.** An earlier entry in this file treated successful DNS
resolution and a matching TLS certificate CN as evidence the account was real. That was
wrong: `r2.cloudflarestorage.com` is wildcard-resolved, so a fabricated account ID
resolves and is served a valid wildcard certificate too. Neither check carries any
information about whether the account exists. They were listed among the things confirmed
working and should not have been.

**The discriminating probe.** Four credential sets, one identical signed request:

```
invented key (all zeros)             -> HTTP 401  Unauthorized
invented key (all f's)               -> HTTP 401  Unauthorized
the real key + the real secret       -> HTTP 403  AccessDenied
the real key + one wrong secret char -> HTTP 403  AccessDenied
```

401 means Cloudflare does not recognise the key; 403 means it does and is declining to
authorise. The access key ID is therefore real and live in that account — not a typo, not
stale, not from the wrong account. That is the hypothesis a 403 usually invites, and it is
now excluded.

Cloudflare collapses "wrong secret" and "valid secret without permission" into the same
403 and does not return `SignatureDoesNotMatch`, so the signature cannot be verified from
outside. Remaining causes, in order of likelihood:

1. The bucket `storvia-fallback` does not exist under account `f537476a76bae45a4f65fd8618326090`,
   or is spelled differently. **R2 answers `AccessDenied` rather than `NoSuchBucket` for a
   bucket the token cannot see** — verified by requesting a deliberately nonexistent bucket
   and receiving an identical 403 — so a wrong bucket name is permanently indistinguishable
   from missing permission.
2. The token is valid but scoped to a different bucket.
3. The token carries `Object Read` rather than `Object Read & Write`.

A `cfat_`-prefixed Cloudflare API token was also present in the environment under a
non-standard name. It returns `401 Invalid API Token` against the Cloudflare API, and
`9109 Unauthorized` against the account endpoint, so it is invalid at the source. It is an
API token, not an S3 session token; signing it as `x-amz-security-token` does not change the
outcome, which was also verified.

### Root cause found: the bucket name was wrong

The owner supplied the S3 API endpoint from the Cloudflare R2 dashboard:

```
https://f537476a76bae45a4f65fd8618326090.r2.cloudflarestorage.com/storvia-testing
```

The path segment is the bucket name: **`storvia-testing`**, not `storvia-fallback`. The
configuration had been written with a guessed name, and the guessed name was the entire
problem.

Correcting the bucket in `config/config.development.json` changed the provider's response
from `AccessDenied` to **`SignatureDoesNotMatch`** — a different failure with a completely
different meaning:

| Error | Meaning |
|---|---|
| `AccessDenied` | Authorisation refused before the signature was evaluated. The earlier error masked the real one. |
| `SignatureDoesNotMatch` | The bucket was found, the request was authorised, and the signature was computed and **rejected**. |

Reproduced identically through `boto3`, so this is the provider's verdict on the
credentials rather than a difference between signers.

`config/config.development.json` now points at `storvia-testing`, and `make test-live`
reaches the bucket and returns the provider's explicit instruction: *"Check your secret
access key and signing method."*

### What the owner needs to do

Re-copy the **Secret Access Key** from the `storvia-testing` token's detail view into
`.env.development` as `STORVIA_R2_SECRET_ACCESS_KEY`. The current value is 64 hex
characters with no whitespace or quoting, so it is well-formed but wrong — almost
certainly a partial or stale copy. The access key ID is confirmed good: an invented key
returns `401` while this one returns `403`, which means Cloudflare recognises it.

### Not done

- The end-to-end "Drive fails, upload lands in R2" demonstration is still **not**
  achieved. It is blocked on the above, and the fault-injection wiring is verified
  only against a mock backend.
- The `Dockerfile` still has never been built; no Docker daemon was available.

---

## 2026-10-15 — Fault injection and the S3 fallback upload path

Commit: *feat: add development-only fault injection and implement fallback upload completion*

### Why

The owner asked to be able to test the outage mechanism: force Drive to fail and see
an upload land in R2. Two things made that impossible.

### What was blocking it

1. **The upload transfer path did not exist.** `POST /uploads/{id}/complete` returned
   `provider_not_configured`, and `createUpload` never populated `Parts`, so a
   fallback upload had no way to move bytes or to finish.
2. **There was no way to make Drive fail on demand.** A real outage is rare and
   unreproducible, so the mechanism could only be reasoned about, never exercised.

### Added

- **Development-only fault injection.** `admin_panel.simulate_drive_outage` forces the
  read-only Drive availability preflight to report a transport failure. It is gated on
  **two independent conditions**, both required: the configuration flag, and
  `environment == "development"` passed from the wiring rather than read from
  configuration. A config file can be copied from development into production by
  accident; an environment cannot. `admin_panel.simulate_drive_outage` is refused by
  the loader outside development, and the check runs before the `enabled` flag so a
  configuration cannot arm a fault with the panel switched off.
- `FallbackUploader`, a narrow port in the service layer for planning and verifying
  fallback uploads. The service never sees an SDK type.
- `UploadService.CompleteUpload` — verifies the object at its recorded key with
  `HeadObject`, compares the provider's confirmed size against the declared size, and
  commits exactly one location. It refuses a size mismatch rather than marking a
  truncated upload ready, and a provider `404` is surfaced as missing, never as a
  reason to write elsewhere.
- `createUpload` now returns short-lived presigned capabilities once the policy has
  selected the fallback and the attempt row is durable, so a client cannot reach the
  path for a Drive upload. It rejects any capability whose URL does not reference the
  planned object key.
- `internal/app/fallback_uploader.go` adapts the S3 clients to the port, choosing a
  part count from the declared size and the S3 ten-thousand-part ceiling.
- `App.DriveFaultsEnabled`, surfaced so the admin panel can show that a simulated
  outage is armed. A service that believes Drive is down when it is not is otherwise
  very confusing to debug.

### Fixed

- **`buildDrive` still read the environment directly** with `os.Getenv` instead of the
  resolved secrets map. It worked only because the entry point exports the variables
  first, and it would have broken the moment secrets came from a runtime secret file
  rather than the environment. The earlier audit missed it because the grep looked for
  hardcoded literal names, not `os.Getenv(cfg.Drive.ClientIDRef)`.
- **Fault injection was not reaching the client.** Three separate patch attempts
  silently failed to match the gofmt-aligned source, so `deps.Faults` was never stored
  on the client and the injector was inert. Found because a test asserted on
  `FaultsEnabled` instead of trusting that the wiring compiled.
- **A completed upload could not be completed twice.** The idempotency check ran after
  the active-attempt lookup, but a finished attempt is no longer active, so the repeat
  failed instead of returning the existing object.

### Tests

Nine new tests in `internal/storage/service/fallback_test.go` and four in
`internal/app/wiring_test.go`:

- an injected Drive outage selects the fallback and returns capabilities scoped to the
  object's own tenant
- a healthy Drive selects Drive even with a fallback configured and plans nothing
- completion commits exactly one location, with no Drive pointer present
- completion is idempotent and plans no second upload
- a size mismatch is refused and the object is not marked ready
- a missing object is reported as not-found and triggers no re-plan
- a capability for the wrong key is rejected
- fault injection is impossible when the environment gate is closed
- a production configuration that injects an outage is refused
- the real development configuration builds with the fallback wired and the fault armed

### Verified

```text
make check                                      -> pass
go test -race ./...                             -> pass
internal/app wiring tests                        -> 4 pass
internal/storage/service fallback tests          -> 9 pass
storvia-api with the fault armed                -> logs "simulation is ACTIVE"
```

### Still blocked, and why

The R2 endpoint in `config/config.development.json` is still the placeholder
`https://YOUR_ACCOUNT_ID.r2.cloudflarestorage.com`. The account ID is not a secret, so
it does not belong in `.env`, and it is the one value needed to complete a real
end-to-end run against the bucket. Until it is filled in, the fallback path is proven
against a stub adapter and by the policy tests, but no byte has been written to R2.

Drive chunk upload is still unimplemented, so a Drive-selected upload still cannot be
completed, and the Drive happy path remains unverified against a live Google account.

### Next step, in order

1. Set the real R2 account endpoint in the configuration.
2. Run one real fallback upload against R2 with the fault armed, and confirm the object
   and its size in the bucket.
3. Implement the Drive resumable upload verbs.

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

---

## 2026-10-15 — Implement the remaining unimplemented features

The owner asked for everything still unimplemented to be built. There were five. Each is
below with what it does, why it is shaped that way, what was actually verified, and what
was not.

### Metrics

`/metrics` returned two hardcoded lines and no counters. It now serves a real
Prometheus-compatible exposition.

- `internal/metrics/` — a small registry: labelled counters, gauges, histograms, and a
  build-info constant. Prometheus text format 0.0.4, so an existing scraper reads it
  with no configuration.
- Instrumented at the decisions that matter: upload planned (`provider`), upload finished
  (`provider`, `outcome`), outcome unknown, bytes committed, fallback routing and why,
  provider calls by failure class, job runs, orphans found, consent state, upload
  duration histogram.
- `HealthHandler.Metrics` returns 503 when no registry is wired, rather than a 200 with an
  empty body. A green response with nothing in it reads as "healthy, nothing to say",
  which is worse than an honest failure.

Design decisions worth stating:

- **Label sets are rendered from sorted keys.** Go map iteration order is randomised, so
  building a series key by ranging over the map would mint a new series on roughly half of
  all calls and a counter would look like it reset at random. `TestLabelKeyIsOrderIndependent`
  pins this.
- **`Gather` is deterministic** — families in registration order, label sets sorted. A
  scrape that reorders between calls makes diffs useless.
- **Histogram buckets are cumulative**, which is what a scraper requires.
- **Every recorder tolerates a nil receiver**, and `*Counter`/`*Gauge`/`*Histogram` do
  too. Instrumentation that can panic takes down the request it measures. This was not
  hypothetical: wiring metrics broke two existing tests, because nil-safety had been
  written on `*Counter` and not on `*Storvia`.

### Job workers and leases

The `storage_jobs` table had existed since the first migration and nothing claimed from
it. A failed delete left its object stuck in `deleting` forever, and every client retry
read as a permanent conflict.

- `internal/persistence/jobs.go` — `Enqueue`, `Claim`, `Renew`, `Finish`, `Fail`, `Pending`.
- `internal/app/worker.go` — the worker loop, lease renewal on a timer, and dispatch for
  `reconcile`, `delete`, and `cleanup`.
- Claiming is a **lease, not a delete**. A crashed worker's jobs become claimable again
  when the lease expires rather than being lost. The `UPDATE` is conditional on the row
  still being claimable, so two workers racing for the same row cannot both win.
- `Finish`/`Renew` require ownership. A worker that lost its lease must stop rather than
  act on an object another worker may already be acting on — that is the double-delete the
  lease exists to prevent. `ErrJobLeaseLost`.
- `Fail` retries with backoff and parks the job as `failed` after `max_attempts`. A job
  that always fails is a problem for a person to look at, not something to spin on.
- `Enqueue` is idempotent per `(attempt_id, kind)` via the existing unique index, so a
  retrying client cannot produce two jobs that both delete the same object.
- Config added: `worker.{enabled,owner,lease,poll,max_attempts}` in JSON. Ordinary
  settings, no secret — a lease owner is an identity string, not a credential.
- `validateWorker` rejects `poll >= lease`: that combination means a worker can hold a job,
  go away for a poll cycle, and have its work taken while it is still running.
- A failed delete now enqueues a retry (`ObjectService.scheduleRetry`). The object stays
  in `deleting` on purpose: the job is pinned to that object and that provider, so a retry
  cannot move the deletion somewhere else.
- Worker and poller are stopped and joined on shutdown, including the failure path. A
  worker left running would keep claiming jobs from a process on its way out.

### Orphan sweep

`ListStorviaPrefix` and `OwnsKey` existed in the S3-compatible client and nothing called
them.

- `internal/app/orphans.go` walks each backend's Storvia prefix, paginated, and reports
  keys with no `storage_objects` row.
- **It only reports. It never deletes.** Listing is cleanup *evidence*, never permission to
  delete: Storvia keeps no second copy, and a key it does not recognise may be a file whose
  database commit is still in flight. Acting on that would be the worst thing this service
  could do. `orphanGrace` (one hour) is documented for when that decision is made.
- Keys outside the Storvia prefix are skipped without being counted or touched.
- One unreachable bucket does not stop the sweep of the others.
- The page cap is a safety bound, not a correctness requirement; a truncated sweep is
  reported so it is never mistaken for a clean one.
- Added `ObjectRepository.ExistsByProviderKey`.

### Drive change-log poller

The `changes_cursor` column existed and nothing advanced it.

- `internal/storage/provider/drive/changes.go` — `ListChanges`, `StartPageToken`,
  `ChangeCursor`, `AdvanceChangesCursor`; `persistence.ConnectionByID` and
  `ConnectedConnectionIDs`.
- `internal/app/changes_poller.go` — the poller loop and `PollOnce`.
- It is a poller rather than a webhook subscriber on purpose. Push notifications are only
  a hint that something changed: they can be delivered more than once, missed while a
  deployment is down, and arrive for accounts Storvia no longer cares about. The change
  log is the source of truth.
- **The cursor is advanced after the page is processed, never before.** Advancing first
  and then failing skips changes permanently; advancing second re-reads a page on a crash,
  and every change here is idempotent. Re-reading is the safe direction.
- Only `connected` connections are polled — a revoked grant has nothing to poll, and
  calling Drive with a revoked token just produces errors that look like an outage.
- One account failing never stops the others.

### Redis-backed session store

Production used `NewMemorySessions`, which is correct for one process and silently wrong
for two: a token issued by one instance is invisible to the other, so every second request
from a user fails, intermittently.

- `internal/auth/sessions_redis.go` — selected whenever the deployment's cache is already
  Redis, so no new configuration and no second connection pool.
- **It does not fall back to memory when Redis is unreachable.** Falling back would start
  working and quietly log users out at random as instances disagreed. It refuses to start.
- `ErrSessionBackendUnavailable` is distinct from "session not found". Conflating them logs
  everybody out the moment Redis blinks, which looks like an attack and is not.
- TTL is session lifetime plus an hour. A key that expires before its token does logs the
  user out while their cookie is still valid, which presents as random auth failures.
- `RevokeSession` so a logout takes effect at once.
- Readiness now checks the session backend. Without it a Redis outage leaves `/readyz`
  green while every authenticated request fails and traffic keeps arriving.

### Bugs found and fixed while implementing

1. **`isExpiredPageToken` drained the response body and never restored it**, so
   `normalizeStatus` classified the following error against an empty body. This is how a
   real Google error message got replaced with `google returned 400` and no reason.
   Fixed, and `TestExpiredPageTokenErrorKeepsItsBody` guards it.
2. **`extractReason` never returned Google's `error.message`** — only `reason` or
   `status`. The message is the actionable part ("Invalid value at 'changes'"). It now
   prefers message over status.
3. **`normalizeStatus` produced an unusable detail.** It now includes the parsed reason,
   and falls back to a capped raw body when there is none. This runs unattended; an error
   nobody can act on is worse than a slightly longer message.
4. **`tenantIDPtr` was used for job fields**, which is not a tenant. Renamed to
   `nullableString`, which is what it always was.
5. **Three of my own new tests were wrong** — claiming into the past cannot expire a lease
   that way, a retry was reset after the claim instead of before, and an `attempt_id` needs
   a real parent row because of the foreign key. All three fixed in the test, not in the
   production code; where the production behaviour was right, the test changed.
6. **A `Redis.UniversalClient` stub implementing the whole interface** was replaced by
   embedding it. Only three commands are overridden; anything else panics loudly rather
   than silently returning a plausible zero.

### The real bug the change poller found

The first live poll failed with a 400 and an empty body. Chasing it:

- My whitespace hypothesis was **wrong** — `first_whitespace_at: 6` is the space in
  `"Bearer "`, which is required. Recorded because it cost time.
- The empty body was my own `isExpiredPageToken` bug (fix 1 above), not Google's.
- The actual cause: **`changes.list` requires `pageToken` on every call, including the
  first.** With no stored cursor there was nothing to send, so the first poll would have
  failed forever and the change log would never have advanced.
  Fixed with `StartPageToken` (`changes/startPageToken`), so a first poll begins from
  *now* rather than walking the entire history of an account that may have been using
  Drive for years. `TestStartPageTokenIsFetchedWhenNoCursorExists` pins it.

Verified against the real connected Google account: cursor stored, second and third poll
cycles quiet, zero errors.

### Commands actually run

```
make check          # fmt-check, vet, test                     — pass
make check-all      # adds -race                               — pass
make sdk-ts-test    # 22 pass, 0 fail
make test-live      # real R2: TestLiveFallbackBackendIsUsable,
                    #         TestLiveFallbackDriveOutageRoutesUploadToR2 — both PASS
```

Live system (`scripts/dev.sh restart --no-open`):

```
GET /admin   200
GET /readyz  200
GET /metrics content-type: text/plain; version=0.0.4; charset=utf-8
```

11 metric families exposed: `storvia_uploads_started_total`,
`storvia_uploads_finished_total`, `storvia_uploads_outcome_unknown_total`,
`storvia_bytes_stored_total`, `storvia_fallback_routings_total`,
`storvia_provider_calls_total`, `storvia_job_runs_total`, `storvia_orphans_found_total`,
`storvia_fallback_consent`, `storvia_objects_stored`,
`storvia_upload_duration_seconds`, `storvia_build_info`.

Worker, against a real enqueued job:

```
inserted job_live_test (cleanup, pending) directly into storage_jobs
after one poll cycle: state=done, attempts=1, last_error=''
log:  "job completed" kind=cleanup
metrics: storvia_job_runs_total{kind="cleanup",outcome="ok"} 1
```

The cleanup job ran the orphan sweep against the real R2 bucket and found no orphans,
which is the correct result — the live tests clean up after themselves.

### Not done

- **Drive upload/download/delete have still never run against Google.** The resumable path
  is unblocked but unverified. Four consecutive defects lived in that code because no live
  request had ever run; treat it the same way.
- **Fault injection is still armed** (`simulate_drive_outage: true`), so uploads route to
  R2 rather than Drive until the server is restarted with `--faults-off`.
- **PostgreSQL migrations 0001 and 0002 have never been executed.** All testing is SQLite.
- **The orphan sweep reports only.** No deletion, no grace-period enforcement, no operator
  surface for acting on what it finds.
- **The Redis session store has never run against a real Redis.** It is exercised only by
  a stub that fails every command, which covers the error paths and not the round trip.
- The change poller advances its cursor and counts changes; it does not yet act on them.
  Nothing reconciles a drive-side deletion into a Storvia object.
- `isExpiredPageToken` classifies an expired cursor as `ClassConfiguration`, which stops
  the poll. There is no recovery path that resets the cursor and continues; a deployment
  running long enough will eventually hit an expired token and stop polling that account
  until someone intervenes.
- No commit was created.

### Next step, in order

1. `scripts/dev.sh stop && scripts/dev.sh start --faults-off`, then drive a real upload,
   download, and delete against the connected account. This is the same gap that hid four
   defects before.
2. Run the PostgreSQL migrations against a real instance.
3. Stand up Redis and verify a session issued by one process resolves in another.
4. Decide what the orphan sweep should *do* about what it finds.

---

## 2026-10-15 — The testing console, persistent sign-in, and forced fallback

Requested by the owner as one change. Six parts: unblock the real Drive path, build a
full testing console, make sign-in last across browser restarts, keep Drive as the
upload default, remove the old admin panel, and add a forceful fallback switch.

### The blocking issue is fixed

`admin_panel.simulate_drive_outage` was `true` in `config/config.development.json`, so
every upload was being routed to R2 and the Drive transfer path stayed cold. It is now
`false`, and the fault toggle is a runtime control rather than something you edit a
checked-in file to change.

### The console replaces the admin panel

The old `/admin` panel was a set of server-rendered form posts. It could show a
connection status and nothing else — it could not perform an upload, so it could not
answer the question it existed to answer. It is **deleted**: `admin_handlers.go`,
`admin_dashboard.go`, and their tests. `GET /admin` now returns 404.

The new console is at **`/console`**. It is a single document with no build step and no
external assets, because a testing panel that needs a bundler stops working exactly when
a toolchain breaks and someone needs it.

- **Sign-in is the gate.** The document is served signed-out so there is something to
  click; every value on the page comes from a session-gated API call, and every API
  route independently rejects an unauthenticated request. A cookie for another tenant is
  rejected with 403 even though its signature verifies.
- **Upload, download, delete.** The panel runs the whole client sequence in one request
  — create, transfer to the provider, verify, commit — and reports the provider, the
  provider-side reference, the byte count, and the elapsed time. The Drive branch
  performs the resumable `PUT` itself and parses the file id out of Google's response,
  exactly as an SDK does; it is the same write, only the caller differs.
- **Object listing, download, and delete**, so an upload can be verified rather than
  assumed. Added `ObjectRepository.ListObjects` — the tenant scope is applied in the
  query, not filtered afterwards, because a listing that read other tenants' rows and
  discarded some has still read them.
- **Connection state, folder creation, and disconnect** on one screen.

### Sign-in now survives closing the browser

The session cookie gets `MaxAge` alongside `Expires`, and the console uses its own
lifetime (`auth.admin_session_ttl`, 720h) rather than the API's 12 hours. A cookie with
only `Expires` is treated as a session cookie by some browsers, so the two are set
together. `MaxAge` truncates to whole seconds — a sub-second remainder truncating to
zero would produce a cookie that expires immediately.

### Drive remains the upload default

It already was, and it still is. What changed is that the console states it explicitly
("Google Drive is the default…") and shows a blocked reason when uploads would be
refused. Force is off by default in both config files.

### Forced fallback

`fallback.force` in config, plus a live switch in the console with three states: force
the fallback, prefer Drive, or follow the configured default. The tri-state matters —
"stop overriding me" cannot be expressed by a plain boolean, and it is the option an
operator reaches for most.

Two deliberate limits, both tested:

- **Consent still applies.** An operator switching providers does not manufacture the
  tenant's agreement to have files stored elsewhere. `TestLiveConsoleForcedFallbackStillNeedsConsent`
  proves a forced upload without consent is refused.
- **An in-flight mutation stays pinned.** The override chooses a provider for a *new*
  upload; it cannot reach back and relocate bytes that may already be in Drive.

Forcing also **skips the Drive preflight** rather than merely ignoring it. During an
incident, a wasted round trip to a struggling provider is not free.

Ordering inside `Evaluate` was wrong on the first attempt — the override was inserted
before the `Authorized` and `MutationMayHaveStarted` checks, so it overrode both. Two of
my own tests caught it.

### Bugs found and fixed while building this

1. **`Dependencies.Metrics` was never set.** Every upload counter had been recording into
   a nil pointer since it was introduced, so the families stayed permanently empty on a
   healthy `/metrics`. This is the third time a dependency has been declared and never
   constructed, and the same lesson applies: unit tests cannot see wiring.
2. **The S3 part capabilities carried no byte ranges.** No client — including both SDKs
   — could know where to split a multipart upload, because the server chose the part size
   and never said so. `PresignedPartInfo` now carries `start_byte`/`end_byte`. Without
   this the multipart contract was unusable by anyone.
3. **`StopBackground` could deadlock.** It waited for the worker and poller without
   cancelling anything, so it only worked because the signal handler had cancelled the
   context first. A clean stop from a test, a library caller, or an in-process restart
   waited forever. Background work now gets its own cancellable context and `Stop`
   cancels before it waits. This was found by a live test timing out in cleanup.
4. **The console rejected every upload on validation.** It hardcoded the category `other`
   and defaulted an absent part `Content-Type` to `application/octet-stream`, neither of
   which the development allow-list permits — and Go's own form writer sends no part
   content type. The panel now infers from the extension and the configured allow-list,
   and picks a real category. The substitution is reported rather than silent.
5. **The final multipart part went out unbounded and chunked.** `want < size-sent` left
   the last part reading the raw reader, so it carried no `Content-Length`. A presigned
   S3 URL is signed over the headers it was issued with, so that request stalls or fails
   the signature — and a stall looks like an outage. The part-length guard also missed a
   single-byte part, because it compared `EndByte > 0` instead of the two bounds.
6. **`FaultInjection` held a static bool**, so no runtime toggle could reach it. It now
   carries an `*FaultFlag`, and `SetFaultsEnabled` refuses when injection was never gated
   on — a provider that can be told to lie about its health must not be reachable in
   production.

Three of my own new tests were wrong again (asserting on `EligibleForFallback` where the
production decision was correct; expecting `evil drop` from `sanitizeFilename`; a nil
issuer in the test helper). In each the production code was right and the expectation was
not.

### Verified

```
make check, make check-all, make sdk-ts-test (22 pass, 0 fail)   — pass
make test-live                                                    — 4 pass, 0 fail
```

Live against real Cloudflare R2:

- `TestLiveConsoleForcedFallbackUpload` — force the fallback, upload 4138 bytes,
  assert the provider is `s3_compatible` **even though Drive was healthy**, read the
  bytes back byte-identical, confirm the routing decision is visible in the state, and
  delete. **PASS**
- `TestLiveConsoleForcedFallbackStillNeedsConsent` — forced upload without consent is
  refused. **PASS**
- `TestLiveFallbackDriveOutageRoutesUploadToR2` — now switches routing rather than
  injecting a fault, so it does not silently stop running. **PASS**

Running system: `/console` 200, `/admin` 404, `/console/api/state` 401 signed out, zero
injected faults.

### Not done

- **Drive upload/download/delete still have never run against Google.** The path is no
  longer blocked by fault injection, but nothing has been uploaded through it. The
  console is now the tool for doing that: sign in, pick a file, upload.
- **The console has not been driven through a browser.** The backend is verified; the
  page itself has only been fetched, not clicked.
- The forced switch and the fault switch are in-memory. Neither survives a restart. That
  is deliberate for fault injection; for the forced switch it means an operator who
  forces the fallback during an incident has to re-apply it after a deploy. `fallback.force`
  in config is the durable form.
- **The fallback's forced path bypasses the Drive preflight entirely**, so there is no
  per-request evidence that Drive was healthy when the switch was flipped. The console
  shows the switch state, which is the intended reading.
- PostgreSQL migrations and the Redis session store remain unproven.
- No commit was created.

### Next step, in order

1. Open `/console`, sign in with Google, and upload a real file. This is the gap that has
   hidden four consecutive defects, and the console now makes it a two-click exercise.
2. Run the PostgreSQL migrations against a real instance.
3. Stand up Redis and verify a session crosses a process boundary.

---

## 2026-10-15 — The Drive upload path, verified against real Google

Requested as "fix the default upload to the user's gdrive, and only fall back when
gdrive is unable". The routing already preferred Drive, so the honest way to answer was
to prove it rather than argue it — and the first live run of the Drive path found a real
defect that had been shipping the whole time.

### The routing was already correct; the upload was not

`TestLiveDefaultUploadGoesToDrive` connects the real account, uploads through the console,
and asserts the provider is `google_drive`. It failed:

```
transfer failed: drive refused the upload with 403:
  "The resource body includes fields which are not directly writable."   (reason: fieldNotWritable)
```

`InitiateUpload` was putting `size` into the create metadata. The comment above it said
*"Drive requires the declared size so it can reject an over- or under-sized upload"*.
That is wrong: **`size` is a computed field on a Drive file resource.** It is returned by
the API and cannot be set. Every resumable initiation failed.

The declared length was already being sent correctly, in `X-Upload-Content-Length` —
three lines below. The metadata field was not merely redundant, it was fatal.

Removed, with a comment explaining why it must not come back. Two regression tests:

- `TestInitiateSendsOnlyWritableFields` — enumerates the fields Drive refuses
  (`size`, `id`, `createdTime`, `md5Checksum`, `owners`, `version`, …) so the next person
  to add a convenient field trips a test rather than a live account.
- `TestInitiateDeclaresLengthInTheHeader` — pins *where* the length went, not only that
  it left, so a later "fix" cannot quietly drop it.

### Both directions verified against the real providers

| Scenario | Result |
|---|---|
| Drive connected and healthy | `google_drive`, file created in `Storvia/`, bytes read back identical, deleted |
| Drive unavailable (preflight forced to fail) | `s3_compatible`, stored at the R2 key, deleted |

- `TestLiveDefaultUploadGoesToDrive` — file `1FjmhI6aCbris2eY2HRMwRjx2_-FKUhSW` created in
  the real `Storvia/` folder, read back byte-identical, deleted. **PASS**
- `TestLiveUnavailableDriveFallsBackToTheProvider` — **PASS**, routed to
  `storvia/tenants/…/objects/upl_000038a0egnpy0000008etdkfyza07aatex9`.

The second test deliberately uses a **real connected account** and makes the preflight
fail the way an outage would, rather than switching routing. With no connection at all,
routing refuses the upload instead of falling back — a different and much easier thing to
get right, and one that would have passed a weaker test.

### Two things about the test harness worth recording

**The relative database path nearly made this a silent no-op.** `sqlite_path` is
`file:./storvia.dev.db`, relative to the *process* working directory, and `go test` runs
from the package directory. The first run reported "no google drive connection" and
skipped — a green run proving nothing, because it had opened a different, empty database.
`findRepoRoot` now resolves the path against the module root, and the test **fails** rather
than skips when there is no connection, so a broken harness can never look like a pass.

**The 403 was truncated twice before it was readable.** The console capped provider
detail at 300 characters, which cut the message off mid-JSON, and the probe that followed
printed only its first 90 characters. Both caps were raised; a diagnosis you cannot read
is a diagnosis you have to guess at.

### How the bug survived

Every existing test asserted on what Storvia *sent*. A test that checks its own outgoing
request cannot detect that the recipient rejects it. The whole suite was green, the
`/console` backend was green, and the feature was broken against the only provider that
matters.

This is the fifth defect in the Drive path and the first found by exercising it rather
than by reading it. The four before it (`/files/files`, `/drive/v3/files/about`, the
token-endpoint content type, the missing `pageToken`) were all found the same way, and
all four would have shipped.

Added `make test-live-drive`, which runs the Drive tests. It is separate from
`make test-live` on purpose: these create, read, and delete files in a real account,
which is a different kind of risk from writing to a test bucket and should not be turned
on by surprise.

### Verified

```
make check, make check-all            — pass
make sdk-ts-test                     — 22 pass, 0 fail
make test-live                       — 2 pass
make test-live-drive                 — 6 pass, 0 fail (real Drive + real R2)
```

`/console` 200, `/admin` 404, `/healthz` and `/readyz` 200, Drive connection `connected`.

### Not done

- The console's frontend is still unclicked. Every API route is driven by tests, but
  nobody has looked at the page in a browser.
- The two Drive tests require a signed-in console session in the development database and
  **skip if there is none.** They are honest about that — they `t.Skip` rather than
  pretend — but a skip is not a pass.
- Only the small-file path is exercised: 3 KB and 4 KB. Chunked resumable uploads above
  `drive_chunk_size_bytes` (8 MB) have still never run.
- PostgreSQL migrations and the Redis session store remain unproven.
- No commit was created.

### Next step, in order

1. Upload a file larger than 8 MB through `/console`, so the chunked resumable path runs.
2. Open `/console` in a browser and confirm the page renders and signs in.
3. Run the PostgreSQL migrations against a real instance.

---

## 2026-10-15 — Fix: sign-in 404s on `/admin/oauth/callback`

Reported immediately after the console replaced the admin panel:

```
404 no route matches this request   instance=/admin/oauth/callback
```

**The console move broke Google sign-in.** The panel served its OAuth callback at
`/admin/oauth/callback`; the console serves it at `/console/oauth/callback`. Deleting the
old panel deleted the callback, and Google redirects the browser there after consent —
so anybody whose Google Cloud project still had the old URI registered landed on a 404
instead of a session.

### Why just changing the config would not have worked

The obvious fix — repoint `drive.admin_redirect_uri` at the console path — trades this
bug for the mirror image. **Google rejects a `redirect_uri` that is not registered**, so
until the project in Google Cloud is edited, changing the config produces a consent
screen that bounces back with `redirect_uri_mismatch`. The callback URI is registered
outside this repository, which makes it the one setting here that cannot be changed
locally.

### The fix: accept the callback at both paths

- `GET /console/oauth/callback` and `GET /admin/oauth/callback` both reach the console's
  callback handler.
- `GET /console/sign-in` and `GET /admin/connect` both start the flow.
- `GET /admin` and `GET /admin/` now **301 to `/console`**, so a stale bookmark lands on
  the console instead of a 404.

`drive.admin_redirect_uri` is left at the registered `/admin/oauth/callback` value, which
is what Google Cloud has, so sign-in works before anyone re-registers anything. The old
path is an alias, not a second panel — nothing is served there except the callback and the
redirect.

Verified against the running server:

```
/admin/oauth/callback?code=…&state=…   302 -> /console?error=…
/console/oauth/callback?code=…&state=…  302 -> /console?error=…
/admin                                301 -> /console
/admin/oauth/callback?error=access_denied
                                       302 -> /console?error=Google did not grant access: access_denied
/admin/connect  -> https://accounts.google.com/…&redirect_uri=http%3A%2F%2F127.0.0.1%3A8080%2Fadmin%2Foauth%2Fcallback
flow cookie: storvia_admin_flow=…; Path=/; Max-Age=600; HttpOnly; SameSite=Lax
```

The redirect URI sent to Google is unchanged, so a project with the old registration keeps
working. The flow cookie is `Path=/`, so it survives the round trip.

### Tests

Four tests against the **production route table** — `Router(Config{...})`, not a
hand-built mux. A test that re-declared the paths would have passed after the real router
lost them, which is precisely the failure being guarded.

- `TestOAuthCallbackSurvivesThePanelMove` — both callback paths reach the handler.
- `TestOldPanelRootRedirectsToTheConsole` — 301 to `/console`.
- `TestDeclinedConsentIsNotAnError` — declining is reported as a normal outcome.
- `TestCallbackWithNoCodeDoesNot404`.

Confirmed the first test fails when the alias is deleted, so it is not decorative.

### The lesson

I deleted a route without asking what depended on it. The dependency was in Google
Cloud, invisible from here, and it only manifests when a human tries to sign in — which
is the one thing nobody does during a test run.

The general form: **a path is not owned by the process that serves it.** Anything a third
party holds a permanent reference to — an OAuth redirect, a bookmark, a webhook, a docs
link — keeps pointing there after you move, and breaking it fails silently until someone
uses the feature. Leave the old path answering until the external registration has
actually changed.
