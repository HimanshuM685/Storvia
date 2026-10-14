# Storvia project guidance

- Read `README.md`, `docs/storage-policy.md`, and `docs/implementation-plan.md` before working on this project.
- Keep project decisions, changed behavior, actual checks, and next steps updated in Markdown during each meaningful change. The user requires durable documentation for future sessions.
- Distinguish confirmed requirements from proposed implementation choices. Do not silently promote a recommendation into an accepted requirement.
- Confirmed storage direction: Google Drive first; S3-compatible storage only for Drive unavailability; no application-managed duplicate file copies, mirroring, or background copy-back.
- Follow the failure classification and single-location rules in `docs/storage-policy.md`. A timeout is not proof that a write failed.
- Implementation language: Go. This repository currently contains planning documents, not a working application. Do not describe planned Go checks as having passed.
- Preserve narrow Drive OAuth scope, tenant isolation, secret redaction, and existing user changes. Never store credentials, tokens, signed URLs, or raw private inputs in these documents.
