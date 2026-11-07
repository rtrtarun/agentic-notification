# Milestone 12: Deduplication, Grouping and Threading

## Goals

- Collapse repeated standard/background events before they reach the Attention Layer.
- Assign every event a `thread_id` so related events (one PR, one issue) group together.
- Never merge critical events.

## Architecture Decisions

- New `pkg/dedup` package with a `Deduper` interface and a sliding-window `MemoryDeduper`. Deterministic, no AI (Layer 1).
- `dedup_key` = `source:repository[:number]|eventType|action`; `thread_id` = the same without event type and action.
- The pipeline takes the deduper through `WithDeduper`, so it stays optional and testable.
- A duplicate is still persisted, with `decision_state.status = merged` and `merged_into` pointing at the first event.
- Window is configurable with `DEDUP_WINDOW` (Go duration, default `10m`).

## Database Changes

```sql
ALTER TABLE events ADD COLUMN IF NOT EXISTS dedup_key TEXT;
ALTER TABLE events ADD COLUMN IF NOT EXISTS thread_id TEXT;
ALTER TABLE events ADD COLUMN IF NOT EXISTS merged_into TEXT;
CREATE INDEX IF NOT EXISTS events_thread_id_idx ON events (thread_id);
```

## APIs

No new routes. Events now carry `dedupKey`, `threadId` and `mergedInto`.

## Folder Changes

```
pkg/dedup/            # Deduper, MemoryDeduper, key/thread derivation
pkg/pipeline/         # WithDeduper, applyDedup
pkg/event/            # new fields + Postgres columns
pkg/db/migrate.go     # idempotent column migration
pkg/config/config.go  # DEDUP_WINDOW
```

## Risks

- Keys that are too broad could hide distinct events. Mitigation: keys include action and PR/issue number; critical events are never merged; window is configurable.
- The in-memory deduper resets on restart and is per-instance. A Redis-backed `Deduper` can replace it without touching the pipeline.

## Tests

- Duplicate inside the window is detected; outside it is not.
- Different actions share a thread but not a key; different PRs get different threads.
- Pipeline merges duplicate standard events and persists both.
- Pipeline never merges critical events.

## Acceptance Criteria

- [x] Repeated standard events get `decision_state.status = merged` and `merged_into`.
- [x] Critical events are never merged.
- [x] Every event gets a `thread_id`.
- [x] All Go tests pass.

## Review Checklist

- [x] No AI in the dedup path.
- [x] Dedup is behind an interface.
- [x] Migration is idempotent.
