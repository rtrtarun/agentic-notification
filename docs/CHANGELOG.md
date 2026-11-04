# Changelog

Every change to this repository is recorded here, newest first. Milestone-level detail lives in `docs/milestone-N.md`; this file is the running index. Update it in the same commit as the change.

## Unreleased

### Added
- Milestone 13 step 6 (review remediation, see [milestone-13.md](milestone-13.md)): `notification.Limits`/`ValidatingService` (SSRF and recipient validation), `delivery.WebhookProvider` (private-address blocking, no redirects) and `delivery.MultiProvider` (concurrent channels, joined errors), `RedisDeduper` (`REDIS_URL`), `event.Repository.Exists`/`event.Purger` and hourly retention (`EVENT_RETENTION`), `db.PoolConfig` and `db.MigrateLocked`, `pkg/metrics` (Prometheus text with latency histograms), Postgres integration tests (`-tags integration`), `.golangci.yml`, `docs/runbook.md`, `k8s/secret.example.yaml`, READMEs for every package. New env vars: `ALLOW_UNAUTHENTICATED_API`, `CORS_ALLOWED_ORIGINS`, `NOTIFY_ALLOWED_WEBHOOK_HOSTS`, `NOTIFY_ALLOWED_EMAIL_DOMAINS`, `REQUEST_TIMEOUT`, `EVENT_RETENTION`, `REDIS_URL`, `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME`.
- New `skills/` folder with the `golang-pro` skill (SKILL.md plus concurrency, interfaces, generics, testing and project-structure references) from Jeffallan/claude-skills (MIT), the `tarun-code-review` skill (principal-engineer review checklist), and an index README.
- Milestone 13 steps 2-5 (complete): request IDs (`X-Request-ID`), generic client error messages (validation errors still return 400 with detail), structured `log/slog` JSON logging (`LOG_LEVEL`), `context.Context` propagation through all I/O, new `pkg/mail` for cancellable SMTP, graceful shutdown with `ReadHeaderTimeout`, and new tests for `config`, `db`, `notification/impl`, `mail`, server and cancellation. `rows.Err()` is now checked in the Postgres repository.
- Milestone 13 step 1: GitHub webhook signature verification (`X-Hub-Signature-256`, constant-time HMAC over the raw body), 1 MiB body limit, fail-closed when `GITHUB_WEBHOOK_SECRET` is unset. New env vars `GITHUB_WEBHOOK_SECRET`, `ALLOW_UNSIGNED_WEBHOOKS`, `MAX_BODY_BYTES`. **Breaking:** GitHub webhooks without a secret now get 503; set the secret in GitHub or use `ALLOW_UNSIGNED_WEBHOOKS=true` locally.
- `docs/milestone-13.md`: proposed security and production hardening plan (webhook signature first). Plan only; no code.
- `docs/CHANGELOG.md` and the rule that every change is documented in `docs/`.

### Changed
- **Breaking (security):** protected routes now fail closed. With no `API_KEY` they return 503 unless `ALLOW_UNAUTHENTICATED_API=true`. `/health` no longer needs a key. CORS is closed unless `CORS_ALLOWED_ORIGINS` lists the origin. `/notify` rejects non-https or non-allow-listed webhook URLs and invalid recipients (400).
- **Breaking (API):** `GET /metrics` now returns Prometheus text instead of JSON. Webhook event IDs are `github-<event>-<delivery id>`; a redelivery returns `200 {"status":"duplicate"}`. Logs use `http_status` instead of `status`.
- **Breaking (internal API):** `event.Repository` gains `Exists`; `dedup.Deduper.Check` takes a context and returns an error and gains `Forget`; `db.Connect` takes a `PoolConfig`; `pipeline.Pipeline.Handle` can return `ErrDuplicateDelivery`.
- Critical events now also reach Slack and Teams (previously only email was sent from the pipeline) and every channel is attempted even if another fails. `/notify` emails no longer carry the `[n8n agent]` subject prefix.
- Dedup key for `push` events includes the head commit, so different pushes are no longer merged.
- Go toolchain pinned to 1.26.10 (clears standard library vulnerabilities); Go Dockerfile rewritten (multi-stage, non-root, health check); CI adds lint, `govulncheck`, PostgreSQL integration tests, an 80% coverage gate, and `mvn verify`; k8s manifests read secrets from a Secret and define probes.
- **Breaking (internal API):** `event.Repository`, `delivery.Provider`, `delivery.Service`, `pipeline.Handle`, `notification.NotificationService`, `learning.Summarize`, `db.Connect` and `db.Migrate` take a leading `context.Context`; `server.NewServer(ctx, cfg, logger)` and `Server.Run(ctx)` changed to match.
- `notify` handler: 500 responses no longer include internal error text; `notification.ErrInvalidRequest` maps to 400.
- Commit messages no longer carry a Co-Authored-By trailer; earlier local commits were rewritten to match.

### Fixed
- Request logs and metrics always reported status 200.
- `PostgresRepository.List` failed on NULL `repository`.
- Policy rules with an unknown `attentionClass` (for example `critcal`) were silently treated as non-critical; they are now rejected at load.
- Webhook event IDs could collide (nanosecond timestamp); a failed save made GitHub's retry look like a duplicate and be dropped.

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
