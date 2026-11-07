# pkg/inbox

What a person does with events: read, archive, snooze, acknowledge, resolve, delete, change priority.

- **Never changes an event.** It reads through `event.Repository` and writes only `notification_state` and `event_trail`. The pipeline stays the only author of decisions.
- **Four display levels** (`LevelOf`): critical stays critical and cannot be lowered or raised by anyone; standard shows as High when the pipeline sent it, otherwise Normal; background shows as Low. A person's override applies below critical only.
- **Outcomes** (`OutcomeOf`): delivered, held, suppressed, merged, recorded, derived from the stored decision.
- **Trail.** User actions are recorded. Earlier steps (received, classified, rules, dedup, outcome) are *reconstructed* from stored fields and flagged `reconstructed`, because the store keeps one timestamp per event.
- **Delete** hides the notification; the event stays for audit and retention.
- **Window.** Lists, counts and filters run over the newest `event.DefaultListLimit` (1000) events, in Go, so memory and Postgres behave the same.

HTTP routes are in `pkg/server/inbox_handlers.go`; see `docs/milestones/milestone-15.md` for the API.

Repositories: `MemoryRepository` (no database, tests) and `PostgresRepository`.
