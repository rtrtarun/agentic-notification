# dedup

Deterministic deduplication and threading (Layer 1, no AI).

- `Deduper` — interface the pipeline depends on: `Check` records an event and reports duplicates, `Forget` releases an entry after a failed save.
- `MemoryDeduper` — in-process sliding window, bounded to 100k keys (stops recording, never suppresses, when full). Fine for one replica.
- `RedisDeduper` — same semantics shared across replicas (enabled by `REDIS_URL`). Fails open on a Redis error.
- `DedupKey` / `ThreadID` — derived from source, repository, PR/issue number, event type, action and, for pushes, the head commit (`after`), so different pushes are never merged.

Critical events are tagged with a thread but never merged (enforced in `pkg/pipeline`).
