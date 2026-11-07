# event

Event model and persistence.

- `Repository` — `Save` (idempotent on `ID`), `Exists`, `List` (newest `DefaultListLimit` rows, oldest first).
- `Purger` — `DeleteOlderThan`, used by the retention loop (`EVENT_RETENTION`).
- `InMemoryRepository` for tests and local runs; `PostgresRepository` for production (parameterised SQL only).

PostgreSQL integration tests: `go test -tags integration` with `TEST_DATABASE_URL` set.

`WorkQueue` (milestone 29, optional): `ClaimPending` (FOR UPDATE SKIP LOCKED with leases, never a critical event), `ListDelayed`, `UpdateDecision` (refuses to change a critical event). Implemented by the in-memory and PostgreSQL repositories.
