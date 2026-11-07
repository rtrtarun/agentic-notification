# db

PostgreSQL connection and schema.

- `Connect(ctx, url, PoolConfig)` — pooled connection verified with a ping.
- `Migrate` — idempotent statements. `MigrateLocked` runs them in one transaction under `pg_advisory_xact_lock`, so replicas starting together migrate one at a time and a failure rolls back.

See also `dbtest` (`pkg/db/dbtest`): opens a migrated database for integration tests.
