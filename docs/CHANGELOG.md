# Changelog

Every change to this repository is recorded here, newest first. Milestone-level detail lives in `docs/milestone-N.md`; this file is the running index. Update it in the same commit as the change.

## Unreleased

### Added
- Milestone 13 step 1: GitHub webhook signature verification (`X-Hub-Signature-256`, constant-time HMAC over the raw body), 1 MiB body limit, fail-closed when `GITHUB_WEBHOOK_SECRET` is unset. New env vars `GITHUB_WEBHOOK_SECRET`, `ALLOW_UNSIGNED_WEBHOOKS`, `MAX_BODY_BYTES`. **Breaking:** GitHub webhooks without a secret now get 503; set the secret in GitHub or use `ALLOW_UNSIGNED_WEBHOOKS=true` locally.
- `docs/milestone-13.md`: proposed security and production hardening plan (webhook signature first). Plan only; no code.
- `docs/CHANGELOG.md` and the rule that every change is documented in `docs/`.

### Changed
- Commit messages no longer carry a Co-Authored-By trailer; earlier local commits were rewritten to match.

## Milestone 12 — Deduplication, grouping and threading
- New `pkg/dedup` (`Deduper`, `MemoryDeduper`); duplicates within `DEDUP_WINDOW` (default 10m) are saved as `merged` with `merged_into`.
- Every event gets a `thread_id`; critical events are never merged.
- New `events` columns: `dedup_key`, `thread_id`, `merged_into` (idempotent migration).
- Details: [milestone-12.md](milestone-12.md).

## Repository hardening
- Added [coding-standards.md](coding-standards.md), `.gitattributes` (LF), `.editorconfig`.
- CI: gofmt check, `go vet`, `go test -race`; fixed Java path and Go version; added web build job.
- Ran `gofmt` over the Go service; ignored `target/` and `*.tsbuildinfo`.
- Git remote `origin` now points at `rtrtarun/agentic-notification`.

## Milestones 1–11
See `milestone-1.md` through `milestone-11.md`.
