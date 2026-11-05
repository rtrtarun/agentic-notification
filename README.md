# Attention Orchestration Platform

This workspace contains the foundation for an attention orchestration platform — a system that decides *when, where, whether, and how* someone should be interrupted.

## Architecture

See [docs/orchestration-platform.md](docs/orchestration-platform.md) for the full architecture, canonical schema, and implementation status.

## Current milestone

**Milestone 21** — Help and Support, closing the console build-out (milestones 15 to 21: inbox, tables, analytics, rules, channels, preferences, live updates). See [docs/milestone-21.md](docs/milestone-21.md). Earlier: [milestone 13](docs/milestone-13.md), security and production hardening.

## Included

- Go API service with GitHub webhook ingestion, YAML policy rules, and critical-event delivery
- Pipeline: ingest → policy → deliver (critical) / queue (standard) → persist
- PostgreSQL persistence with automatic schema migration
- React + TypeScript operator console: inbox, all notifications, overview, activity, analytics, rules and policies, channels, integrations, preferences, live updates, help
- Local PostgreSQL via Docker Compose
- CI wiring for the Go service

## Structure

- [go-server/notification-service](go-server/notification-service) — Go API service
- [apps/web](apps/web) — Vite + React + TypeScript frontend shell
- [docs/](docs/) — architecture and milestone plans
- [docs/coding-standards.md](docs/coding-standards.md) — production code standards
- [docs/CHANGELOG.md](docs/CHANGELOG.md) — running record of every change

## Run locally

```bash
docker compose up --build
```

Then open:

- API health: http://localhost:8080/health
- UI: http://localhost:3000

Send a test GitHub webhook:

```bash
curl -X POST http://localhost:8080/webhooks/github \
  -H "X-GitHub-Event: security_advisory" \
  -H "Content-Type: application/json" \
  -d '{"repository":{"full_name":"octo/repo"}}'
```

