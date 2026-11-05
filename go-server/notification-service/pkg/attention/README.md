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

Adding a model means implementing `Classifier` (and optionally `Summarizer` and `Explainer`) and passing it through `Guarded`. No other code changes.

Tests cover classification, determinism, the guard, summaries, explanations, digest selection and windows, bounds and the scheduler.
