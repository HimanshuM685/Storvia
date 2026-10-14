# Security Policy

## Reporting a vulnerability

**Please do not open a public issue for a security problem.** A public issue gives
everyone a copy of the exploit before a fix exists.

Use GitHub's private reporting instead:

1. Go to the **Security** tab of this repository.
2. Select **Report a vulnerability**.
3. Fill in the advisory.

That channel is private between you and the maintainer until an advisory is
published.

> **Maintainer note:** private vulnerability reporting must be enabled under
> **Settings → Security → Advanced Security** before this path works. If the
> Security tab has no "Report a vulnerability" button, that is why.

If you cannot reach the advisory channel, open a public issue that says only
*"security report available on request"* with no technical detail, and wait for a
response.

## What to include

The more of these you can supply, the faster it can be confirmed:

- What the vulnerability lets an attacker do, in plain terms
- The affected version, commit, or file path
- Steps to reproduce, or a proof of concept
- Any request and response captures, with secrets and personal data removed
- Whether the issue is already public anywhere

## Scope

This project handles Google OAuth refresh tokens and enforces tenant isolation, so
the following are in scope and treated as high severity:

| Area | Example |
|---|---|
| Tenant isolation | Reading or mutating another tenant's object by changing an identifier |
| Secret handling | A refresh token, credential, or session URI reaching a log, a response, or a database column in the clear |
| OAuth | A bypassed state check, a replayed consent flow, an unbound callback |
| Single-location invariant | A path that writes the same logical object to two providers |
| Injection | Client-controlled data steering an endpoint, bucket, key, or provider |
| Capability leakage | A presigned URL or session URI escaping its intended scope or lifetime |

Also in scope: the TypeScript SDK, which embeds the same routing assumptions and can
cause duplicate uploads through incorrect idempotency handling.

**Out of scope:** running Storvia against a Google account you do not own, missing
configuration hardening you were warned about, and social engineering.

## Known limitations, reported by design

These are not vulnerabilities. They are the accepted trade-offs of the no-copies
design, documented in [`docs/storage-policy.md`](docs/storage-policy.md) and
[`docs/risk-register.md`](docs/risk-register.md):

- **Storvia is not a backup.** A Drive-only file is unrecoverable if the user deletes
  it or loses the account.
- **A file stored on the fallback is not readable during a Drive-independent outage.**
  There is no second copy.
- **A file written to S3 during an outage stays there.** There is no move-back.
- **A Drive write timeout produces `outcome_unknown`, not a retry.** The upload is
  reconciled on the same provider. A user may see a pending upload resolve late.
- **Client-declared MIME type is trusted as metadata**, not as content validation.

## Pre-release status

This project has **not completed its security review**. It has never been exercised
against a live Google account, a real S3 or R2 bucket, or PostgreSQL. Treat a
production deployment as unvalidated, and do not use it for data you cannot lose.

Known verification gaps are listed in [`CHANGELOG.md`](CHANGELOG.md).

## Secrets

If you believe a credential has been exposed:

1. Revoke it first. Do not wait for a response.
2. For a Google OAuth grant, revoke it at
   [myaccount.google.com/connections](https://myaccount.google.com/connections) and
   delete the project credential.
3. For S3 or R2, deactivate the access key and issue a new one.
4. Then report it, noting which credential was revoked and when.

Never paste a live credential into an issue, a pull request, or a log. Storvia's
logger redacts tokens, secrets, session URIs, and signatures, but a hand-written
`fmt.Println` will not be protected by that.