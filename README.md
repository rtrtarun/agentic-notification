# Attention Orchestration Platform

This workspace contains the foundation for an attention orchestration platform — a system that decides *when, where, whether, and how* someone should be interrupted.

## Architecture

See [docs/orchestration-platform.md](docs/orchestration-platform.md) for the full architecture, canonical schema, and implementation status.

## Current milestone

**Milestone 12** — Deduplication, grouping and threading. See [docs/milestone-12.md](docs/milestone-12.md).

## Included

- Go API service with GitHub webhook ingestion, YAML policy rules, and critical-event delivery
- Pipeline: ingest → policy → deliver (critical) / queue (standard) → persist
- PostgreSQL persistence with automatic schema migration
- React + TypeScript web shell
- Local PostgreSQL via Docker Compose
- CI wiring for the Go service

## Structure

- [go-server/notification-service](go-server/notification-service) — Go API service
- [apps/web](apps/web) — Vite + React + TypeScript frontend shell
- [docs/](docs/) — architecture and milestone plans

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

