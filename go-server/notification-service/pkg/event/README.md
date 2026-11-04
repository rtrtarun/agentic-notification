# event

Event model and persistence.

- `Repository` — `Save` (idempotent on `ID`), `Exists`, `List` (newest `DefaultListLimit` rows, oldest first).
- `Purger` — `DeleteOlderThan`, used by the retention loop (`EVENT_RETENTION`).
- `InMemoryRepository` for tests and local runs; `PostgresRepository` for production (parameterised SQL only).

PostgreSQL integration tests: `go test -tags integration` with `TEST_DATABASE_URL` set.
