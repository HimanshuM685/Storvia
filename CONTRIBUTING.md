# Contributing to Storvia

Thanks for considering it. This document covers the setup, the checks, and the two
project-specific rules that a contribution is expected to respect.

By contributing you agree that your work is licensed under the
[Apache License 2.0](LICENSE).

## Before you start

Read [`AGENTS.md`](AGENTS.md). It records the working rules this project is held to,
and they are short. The two that trip people up:

- **Every meaningful change appends an entry to [`CHANGELOG.md`](CHANGELOG.md).**
- **Never describe an unrun check as passed**, and never present a mocked or
  unit-tested path as verified against a live provider.

## Setup

```sh
git clone https://github.com/shovan04/storvia.git
cd storvia

cp .env.example .env.development
# fill in the values you need; see .env.example for how to generate each one

make check-config   # validate configuration without serving
make run            # serve on http://127.0.0.1:8080
```

Requirements: Go 1.27+, Node.js 20+ for the TypeScript SDK.

You do not need Google credentials or an S3 bucket to run the test suite. The
provider adapters are exercised against stubs.

## Running the checks

```sh
make check           # gofmt -l, go vet, go test ./...
make check-all       # the above plus the race detector
make test-cover      # coverage summary
make sdk-ts-check    # type-check the TypeScript SDK
make sdk-ts-test     # run the TypeScript SDK tests
make deps            # tidy and verify modules
```

A pull request should not reduce coverage in a package it touches, and should not
introduce a data race.

## Two rules that are not negotiable

### 1. The storage contract is load-bearing

[`docs/storage-policy.md`](docs/storage-policy.md) defines the confirmed behaviour:
Drive first, the S3-compatible fallback only for Drive unavailability, one
application-managed location per object, no mirroring and no copy-back.

That contract is enforced by `internal/storage/policy`, which is deliberately a pure
package with no network, database, or clock dependency. Every input is passed in,
which is what lets the whole error-classification table be tested as plain unit
tests.

If you change routing behaviour, change it there and update the table-driven tests
in `internal/storage/policy/policy_test.go` so they still mirror the document row for
row. **The tests are written to catch the tempting mistakes specifically:**

- A class other than `ClassUnavailable` must never select the fallback
- Once a mutation may have started, no combination of arguments may reopen the door
- A failure carrying `MutationMayHaveStarted` must never open the outage circuit
- An unclassified error must be refused rather than guessed at, because guessing
  "outage" on a mis-mapped quota error is how a quota error reaches the fallback bucket

### 2. Structural beats advisory

Where a rule can be enforced by the system rather than by everyone remembering it,
enforce it structurally. Existing examples:

- The single-location invariant is a database `CHECK` constraint, not just application
  code
- Repositories require a verified tenant scope, so a query without one does not compile
- Logging redaction runs in the handler, so a careless caller cannot leak by forgetting
- Unknown configuration fields are rejected, so a typo fails at startup instead of
  becoming a zero value

A review comment asking "why not just check this in the handler?" is usually the
right question. Sometimes the handler is the right place. Prefer the constraint that
cannot be bypassed by a future change.

## Style

- `gofmt` is enforced by `make fmt-check`.
- Keep the provider adapters behind `provider.Provider`. Adapters own HTTP, credential
  use, signing, status parsing, and normalization into `policy.FailureClass`. They must
  never write an application row.
- Keep `internal/storage/policy` free of provider-specific code. If policy needs to
  know something about Drive or S3, the answer is a normalized failure class, not a
  provider branch.
- Normalize provider errors at the adapter boundary. Routing decisions are made on
  `FailureClass`, never on a raw HTTP status.
- Never log or return a token, refresh token, session URI, presigned URL, signature,
  credential, or file body. The logger redacts by attribute name; a raw print will not
  be protected.
- Prefer an explicit comment explaining *why* over one restating *what*.

## Commit and pull request guidance

- One logical change per commit. A commit that fixes three unrelated bugs is three
  commits.
- The subject line states what changed and why: `classify a post-write timeout as
  uncertain, not unavailable` rather than `fix timeout handling`.
- Describe what you verified and what you did not. If the change touches a provider
  path, say plainly whether it was run against a real provider.
- If you fix a bug, the pull request should include the test that failed before.

## Reporting a security problem

Do not open a public issue. See [`SECURITY.md`](SECURITY.md).

## Licence

Apache License 2.0. See [`LICENSE`](LICENSE).