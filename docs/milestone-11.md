# Milestone 11: End-to-End Critical Event Pipeline

## Goals

- Wire ingested events through a deterministic pipeline: policy evaluation → routing → persistence.
- Deliver **critical** events immediately, bypassing the Attention Layer entirely.
- Promote `attention_class` and `decision_state` to first-class fields on every event.
- Load policy rules from YAML instead of hardcoding them in the server.
- Use PostgreSQL when `DATABASE_URL` is configured; fall back to in-memory for tests and local dev.

## Architecture Decisions

- Add a `pipeline` package that orchestrates policy, delivery, and persistence. Handlers stay thin.
- Critical events set `decision_state.status = sent` and invoke the delivery layer synchronously.
- Standard and background events set `decision_state.status = queued` and are persisted only — the Attention Layer will process them in a later milestone.
- Rules are data-driven YAML loaded at startup via `RULES_PATH`. A built-in default applies when no file is configured.
- PostgreSQL schema stores attention metadata as columns, not buried inside the JSON payload.
- Schema migrations run automatically on startup when a database is connected.

## Database Changes

```sql
CREATE TABLE IF NOT EXISTS events (
    id                    TEXT PRIMARY KEY,
    source                TEXT NOT NULL,
    event_type            TEXT NOT NULL,
    repository            TEXT,
    attention_class       TEXT NOT NULL DEFAULT 'standard',
    decision_status       TEXT,
    decision_reason       TEXT,
    decision_model_version TEXT,
    decision_confidence   DOUBLE PRECISION,
    decision_matched_rule TEXT,
    payload               JSONB NOT NULL DEFAULT '{}',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

## APIs

No new HTTP endpoints. Existing routes change behavior internally:

| Route | Change |
|---|---|
| `POST /webhooks/github` | Runs full pipeline: normalize → policy → deliver if critical → persist |
| `GET /health` | Unchanged |
| `POST /feedback` | Unchanged |

## Folder Changes

```
go-server/notification-service/
  config/rules.yaml              # default policy rules
  pkg/db/migrate.go              # schema migration on startup
  pkg/event/model.go             # AttentionClass, DecisionState fields
  pkg/event/postgres_repository.go  # updated columns
  pkg/policy/loader.go           # YAML rule loader
  pkg/pipeline/pipeline.go       # orchestration
  pkg/config/config.go           # DATABASE_URL, RULES_PATH, DEFAULT_NOTIFY_EMAIL
  pkg/server/server.go           # wire pipeline + postgres
```

## Risks

- Critical delivery requires configured recipients (`DEFAULT_NOTIFY_EMAIL`). Without them, events are marked sent but delivery is a no-op via the stub provider.
- YAML rule syntax errors prevent startup — fail fast with a clear error message.
- PostgreSQL migration is idempotent (`CREATE TABLE IF NOT EXISTS`) but does not handle column renames in future milestones.

## Tests

- Pipeline delivers critical events and sets `decision_state.status = sent`.
- Pipeline queues standard events with `decision_state.status = queued`.
- YAML loader parses rules and rejects invalid files.
- PostgreSQL repository persists attention metadata columns.
- GitHub webhook handler runs through the pipeline end-to-end.
- All existing tests continue to pass.

## Acceptance Criteria

- [x] Ingested GitHub events receive an `attention_class` from YAML rules.
- [x] Critical events trigger immediate delivery and are persisted with `decision_state.status = sent`.
- [x] Standard/background events are persisted with `decision_state.status = queued` and no delivery.
- [x] Events persist to PostgreSQL when `DATABASE_URL` is set.
- [x] Rules load from `RULES_PATH` YAML file.
- [x] Architecture doc reflects canonical schema and pipeline flow.
- [x] All Go tests pass.

## Review Checklist

- [x] Pipeline is deterministic — no AI in the critical path.
- [x] Decision metadata is first-class, not nested in payload.
- [x] Server starts with or without PostgreSQL.
- [x] Backend test suite passes.
