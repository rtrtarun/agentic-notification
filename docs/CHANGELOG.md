# Changelog

Every change to this repository is recorded here, newest first. Milestone-level detail lives in `docs/milestone-N.md`; this file is the running index. Update it in the same commit as the change.

## Unreleased

### Added
- Milestone 17 (Overview, Activity and Analytics, see [milestone-17.md](milestone-17.md)): new `pkg/insights`, `GET /api/analytics` and `GET /api/activity`, `inbox.Repository.RecentTrail`, three new screens with accessible CSS charts, and a TypeScript mirror for demo mode. 145 Vitest tests; Go module passes.
- Milestone 16 (All Notifications, see [milestone-16.md](milestone-16.md)): a sortable, filterable table at `#/all` with source, repository and date-range filters, page size and CSV export. New screens are English-first; the translation test exempts their namespaces.
- Milestone 15, commit 2 (Inbox v2 and detail drawer, see [milestone-15.md](milestone-15.md)): new `pkg/inbox` and `/api/notifications` endpoints (list with filters, sort and paging, detail, patch, delete, acknowledge, resolve, snooze, bulk actions, mark all read), tables `notification_state` and `event_trail`, four display priority levels over the three stored classes (critical can never be lowered), read state on the service, combinable filter tabs, bulk bar, confirmation dialog, optimistic updates with rollback, and a detail drawer with why, decision, timeline and related events. 101 Vitest tests; Go module passes.
- Go live under GitHub: [publish-image.yml](../.github/workflows/publish-image.yml) builds the service image and pushes it to GitHub Container Registry; the Pages workflow reads an optional `BACKEND_URL` repository variable (no variable means demo mode); the console treats an empty `VITE_BACKEND_URL` as unset. Step-by-step in [deployment.md](deployment.md).
- Inbox redesign of the console, informed by common notification-centre layouts: a sidebar of views with counts, a top bar with search, time range and a notification bell with unread count and popover, coloured category chips, unread dots with **Mark all as read**, paging, a lead banner, and a three-column layout where the detail sheet docks from 80rem and the sidebar becomes a drawer below 62rem.
- **Languages:** English, Spanish and Hindi through a small typed translation layer (`lib/i18n`), with `Intl` dates and relative times, `<html lang>`, a saved choice and browser-language detection. A test checks every language has the same keys and placeholders. Translations need native review.
- **Colour themes:** Harbour, Violet, Forest, Sunset and Night, applied before first paint, following the system dark setting on first visit; contrast of ink, accent and distress checked at 4.5:1 or better in each.
- 77 Vitest tests (up from 44): i18n, filters and paging, categories, unread storage.
- Docs: [ui-architecture.md](ui-architecture.md) updated (components, layout, accessibility, section 14a).
- Easier first use of the console: a welcome screen on a first visit (what Attention does, how events are sorted, "Try the demo" or "Connect your service"); a **demo mode** with sample data that needs no service (`VITE_DEFAULT_DEMO=true` makes it the default for the hosted build); a plain-language key for the three attention classes above the list; a line telling people to select an event; and feedback choices that each say what they do under a "Was this right?" heading. 44 Vitest tests.
- Hosting: [.github/workflows/pages.yml](../.github/workflows/pages.yml) publishes the console to GitHub Pages (tests, then a build with `VITE_BASE` and demo mode); `vite.config.ts` reads `VITE_BASE`. Guide: [deployment.md](deployment.md).
- [notification-hubs-comparison.md](notification-hubs-comparison.md): how the platform differs from Azure Notification Hubs, with its public request details.
- Mobile and polish pass on the console, from the `mobile-native` and `tarun-design-eng` audit in [ui-architecture.md](ui-architecture.md) section 15: hover styles gated to hover-capable pointers, press feedback, tap highlight and `touch-action` fixes, `overscroll-behavior`, `100dvh`, `viewport-fit=cover` with safe-area padding, `theme-color`, a custom ease-out curve, a reduced-motion fade, and the page behind the full-screen sheet marked `inert` (new `useMediaQuery` hook). No behaviour or API change; 34 tests, `tsc` and build pass. Not yet tested on a physical phone.
- [ui-architecture.md](ui-architecture.md): component reference, state and data flow, design tokens, wireframes, accessibility, and an audit of the console against the `mobile-native`, `tarun-design-eng` and `pick-ui-library` skills with a prioritised fix list. No code changed.
- Milestone 14 (operator console and read API, see [milestone-14.md](milestone-14.md)): `GET /events` (newest first, no raw payloads, `?limit=1..500`) and `Event.CreatedAt`; a new **Attention console** in `apps/web` with its own design language ([frontend.md](frontend.md)): latest critical event and a last-hour sentence, a ledger whose row size and type width follow the attention class, merged events nested on a thread line, a detail sheet with the decision reason and feedback (Dismiss, Always notify me, Never notify me, notes, Undo), specific empty and error states, connection settings kept in `sessionStorage`. 34 Vitest tests and `npm test` in CI.
- [apps/web/README.md](../apps/web/README.md), a runbook section for the console, and sections 9.1, 10.6, 11.4a and 23 (change record) in [architecture.md](architecture.md).
- [architecture.md](architecture.md): complete architecture reference for the Go backend (layers, components, flows, data contracts, ER, UML, state machine, patterns, API, discrepancies, roadmap). All 15 Mermaid diagrams validated.
- Milestone 13 step 6 (review remediation, see [milestone-13.md](milestone-13.md)): `notification.Limits`/`ValidatingService` (SSRF and recipient validation), `delivery.WebhookProvider` (private-address blocking, no redirects) and `delivery.MultiProvider` (concurrent channels, joined errors), `RedisDeduper` (`REDIS_URL`), `event.Repository.Exists`/`event.Purger` and hourly retention (`EVENT_RETENTION`), `db.PoolConfig` and `db.MigrateLocked`, `pkg/metrics` (Prometheus text with latency histograms), Postgres integration tests (`-tags integration`), `.golangci.yml`, `docs/runbook.md`, `k8s/secret.example.yaml`, READMEs for every package. New env vars: `ALLOW_UNAUTHENTICATED_API`, `CORS_ALLOWED_ORIGINS`, `NOTIFY_ALLOWED_WEBHOOK_HOSTS`, `NOTIFY_ALLOWED_EMAIL_DOMAINS`, `REQUEST_TIMEOUT`, `EVENT_RETENTION`, `REDIS_URL`, `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME`.
- New `skills/` folder with the `golang-pro` skill (SKILL.md plus concurrency, interfaces, generics, testing and project-structure references) from Jeffallan/claude-skills (MIT), the `tarun-code-review` skill (principal-engineer review checklist), the `tarun-design-eng`, `mobile-native` and `pick-ui-library` frontend skills (Tarun), and an index README.
- Milestone 13 steps 2-5 (complete): request IDs (`X-Request-ID`), generic client error messages (validation errors still return 400 with detail), structured `log/slog` JSON logging (`LOG_LEVEL`), `context.Context` propagation through all I/O, new `pkg/mail` for cancellable SMTP, graceful shutdown with `ReadHeaderTimeout`, and new tests for `config`, `db`, `notification/impl`, `mail`, server and cancellation. `rows.Err()` is now checked in the Postgres repository.
- Milestone 13 step 1: GitHub webhook signature verification (`X-Hub-Signature-256`, constant-time HMAC over the raw body), 1 MiB body limit, fail-closed when `GITHUB_WEBHOOK_SECRET` is unset. New env vars `GITHUB_WEBHOOK_SECRET`, `ALLOW_UNSIGNED_WEBHOOKS`, `MAX_BODY_BYTES`. **Breaking:** GitHub webhooks without a secret now get 503; set the secret in GitHub or use `ALLOW_UNSIGNED_WEBHOOKS=true` locally.
- `docs/milestone-13.md`: proposed security and production hardening plan (webhook signature first). Plan only; no code.
- `docs/CHANGELOG.md` and the rule that every change is documented in `docs/`.

### Changed
- **Breaking (console):** unread state now comes from the service (`/api/notifications`), so the console needs a service from this version. Read ids kept in the browser are sent once and forgotten. CORS now allows PATCH and DELETE.
- Removed the old list, sheet and filter code (`Ledger`, `EventSheet`, `lib/filters`, `useRead`), replaced by `features/inbox`.
- **Breaking (console):** the dashboard is rebuilt; it needs `GET /events`, so it requires a service from this version. The API key is now entered in Connection settings (per tab) instead of read from `VITE_API_KEY` alone. `docker-compose.yml` allows the origins `http://localhost:3000` and `http://localhost:5173`.
- Web job in CI runs `npm test` before the build. Generated `tsbuildinfo`, `vite.config.js` and `vite.config.d.ts` are no longer tracked.
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
