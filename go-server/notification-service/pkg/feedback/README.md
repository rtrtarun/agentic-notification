# feedback

Layer 4: records what people do with a notification (opened, dismissed, undo, always notify, never notify, replies) as stored, attributed signals, and turns "always" and "never" into overrides. Feedback never changes a critical event.

- `Service.Record` validates the action and scope, looks the event up, copies its source, type and repository onto the row, and stores it. `always` and `never` create an override through `pkg/overrides` (scope `type`, `repository` or `source`), unless the caller may not (`allowOverride=false`), in which case only the signal is kept. `never` on a critical event is refused. `undo` removes the override the same person's latest always or never created.
- `Service.RecordOpened` notes an open once per person and event.
- `Store` — `MemoryStore` (bounded) and `PostgresStore` (table `feedback`). `List` takes a `Filter` (event, person, since, limit) and returns newest first, which is what the learning layer reads.

Tests cover signals with context, scope mapping, critical refusal, signal-only callers, undo rules and store filters.
