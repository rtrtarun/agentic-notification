# attention

Layer 2, Attention Intelligence. The only place judgement about a notification (as opposed to a rule) lives. AI is never used anywhere else.

Promises, enforced in code:

1. **A critical event never reaches it.** `Guarded` refuses critical events before calling the wrapped classifier; the pipeline, the digest generator and the summariser all exclude them.
2. **It can only move an event between standard and background.** `Guarded` rejects any other class and any score or confidence out of range.
3. **The service works without it.** `Heuristic` is deterministic and offline; a model is an optional replacement behind the same interfaces.

## Pieces
- `Classifier`, `Summarizer`, `Explainer` — the interfaces. `Heuristic` implements all three. `ModelHeuristic` (`heuristic-v1`) is recorded on every decision it takes part in.
- `Heuristic.Classify` — scores 0 to 100 from the event type, action, sender and a few payload fields, with a reason for every point. Below 35 is background.
- `Generator` — writes a `Digest` of events whose outcome is `digest` or `delayed` since the previous digest (never critical, never merged repeats), grouped by repository and source, with up to three highlights. Stores it and hands it to a `Notifier` (the delivery outbox).
- `Scheduler` — runs the generator on an interval; a zero interval never runs.
- `DigestStore` — `MemoryDigestStore` and `PostgresDigestStore` (table `digests`).

A model is provided by `pkg/attention/llm` (Ollama or Claude, milestone 26): it implements these interfaces, is wrapped by `Guarded`, and falls back to `Heuristic` on any failure. `SourcedExplainer` and `SourceOf` let an API say whether the heuristic or a model wrote something.

Tests cover classification, determinism, the guard, summaries, explanations, digest selection and windows, bounds and the scheduler.

## Learned score adjustments (milestone 38b)
`Tuning` is an in-memory snapshot of the adjustments an admin approved (see `pkg/learning`). `Heuristic.WithTuning` makes `Classify` add the matching offset (a repository-specific one beats one for the whole type), add the reason `learned adjustment -14: 5 of 5 signals from 2 people were negative`, and name the version on the decision (`heuristic-v1+tuned-N`; `SourceOf` still says "heuristic"). The live path only does a map lookup. With no snapshot, an empty one or a stale one (older than `DefaultTuningMaxAge`) scores are exactly what they were. Offsets are clamped to ±`MaxTuningDelta` (50) whatever a store holds. A critical event never reaches the heuristic, so it is never adjusted.
