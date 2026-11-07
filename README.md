# Attention Orchestration Platform

This workspace contains the foundation for an attention orchestration platform — a system that decides *when, where, whether, and how* someone should be interrupted.

## Architecture

See [docs/orchestration-platform.md](docs/orchestration-platform.md) for the full architecture, canonical schema, and implementation status.

## Current milestone

**Milestone 33** — documentation reconciled with the code, and a Python SDK for AI agents. The platform is built through milestone 33: ingestion, rules and policy, attention intelligence (heuristic with optional Ollama or Claude), delivery (SMTP, Novu, Slack, Teams, SMS), feedback and learning, accounts, agent traces and an MCP server, and the console. See [docs/CHANGELOG.md](docs/CHANGELOG.md), [docs/architecture.md](docs/architecture.md) for the checked reference, and [docs/implementation-sync.md](docs/implementation-sync.md) for what has and has not been proven against real services.

## Included

- Go API service with GitHub webhook ingestion, YAML policy rules, and critical-event delivery
- Pipeline: ingest → policy → deliver (critical) / queue (standard) → persist
- PostgreSQL persistence with automatic schema migration
- React + TypeScript operator console, 15 screens: overview, inbox, all notifications, activity, digests, agent traces, rules, policies, channels, integrations, users, analytics, preferences, settings, help; a demo mode; five themes; nine languages
- Attention intelligence: heuristic classifier, digests, explanations, optional Ollama or Claude with fallback, learning suggestions, an attention budget and an asynchronous worker
- Delivery: a durable retry queue; SMTP, Novu, Slack, Teams and SMS
- Accounts, roles and single sign-on; stored feedback and always and never overrides
- AI agents: `POST /api/events`, traces, an MCP server (stdio and HTTP), a Python SDK (`sdk/python`)
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

