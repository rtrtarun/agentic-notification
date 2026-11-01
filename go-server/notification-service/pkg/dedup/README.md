# dedup

Deterministic deduplication and threading (Layer 1, no AI).

- `Deduper` — interface the pipeline depends on.
- `MemoryDeduper` — in-memory sliding window; configure with `DEDUP_WINDOW`.
- `DedupKey` / `ThreadID` — derive keys from source, repository, PR/issue number, event type and action.

Critical events are tagged with a thread but never merged (enforced in `pkg/pipeline`).
