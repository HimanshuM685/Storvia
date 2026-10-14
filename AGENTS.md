# Storvia project guidance

- Read `README.md`, `CHANGELOG.md`, `docs/storage-policy.md`, and `docs/implementation-plan.md` before working on this project. `CHANGELOG.md` is the record of what has already been done and what is still missing.
- **After every meaningful change, append an entry to `CHANGELOG.md`.** The user requires durable documentation for future sessions, and updating only the pre-existing documents is not sufficient: a future session needs to know what changed, what was verified, and what was deliberately left undone. Every entry must record the decisions settled, the behaviour changed, the bugs found, the commands actually run with their results, what was not done, and the next step in order.
- Keep project decisions, changed behavior, actual checks, and next steps updated in Markdown during each meaningful change.
- Distinguish confirmed requirements from proposed implementation choices. Do not silently promote a recommendation into an accepted requirement.
- Confirmed storage direction: Google Drive first; S3-compatible storage only for Drive unavailability; no application-managed duplicate file copies, mirroring, or background copy-back.
- Follow the failure classification and single-location rules in `docs/storage-policy.md`. A timeout is not proof that a write failed.
- Implementation language: Go. The repository now contains a working application alongside its planning documents. Do not describe planned Go checks as having passed, and do not describe a mocked or unit-tested path as verified against a live provider.
- Preserve narrow Drive OAuth scope, tenant isolation, secret redaction, and existing user changes. Never store credentials, tokens, signed URLs, or raw private inputs in these documents.
