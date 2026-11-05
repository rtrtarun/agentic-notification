# preferences

What a person asked for: quiet hours and muted sources and repositories. Deterministic Layer 1 policy; no AI.

- `Decide` applies them to an event the rules already classified. **Critical events are never adjusted** (pinned by tests in this package and in `pkg/pipeline`).
- Muted source or repository: the event becomes `background` with status `digest` ("muted in your preferences").
- Quiet hours (standard events only): status `delayed` with the time the window ends. Windows may cross midnight and use any IANA zone; `time/tzdata` is embedded so this works in minimal containers.
- `Service` caches the current value for the pipeline and replaces it atomically on `Update`. `Normalise` validates zone, clock times, patterns and limits (50 entries, 100 characters).
- Stores: `MemoryStore`, `PostgresStore` (single row in `preferences`).
