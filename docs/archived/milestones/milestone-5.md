# Milestone 5: Persistence Layer for Events and Policy Decisions

## Architecture

- Keep persistence behind a repository interface so the service layer is not tied to a specific storage engine.
- Use a thin PostgreSQL repository for the current milestone while preserving an in-memory implementation for local and test use.
- Store normalized events and their policy decisions in a simple schema.

## Tasks

- Add a repository abstraction for event persistence.
- Implement a PostgreSQL-backed repository for the current milestone.
- Preserve the in-memory repository for lightweight local usage and tests.

## Implementation

- The event package now exposes a repository interface and a PostgreSQL-backed implementation.
- The repository saves event metadata and serialized payloads into a simple events table contract.
- The server remains decoupled from persistence specifics through the interface.

## Tests

- Verify the PostgreSQL repository issues the expected insert and query operations.
- Keep the existing server and policy tests passing.

## Review checklist

- [x] Repository abstraction exists.
- [x] PostgreSQL-backed persistence is implemented.
- [x] Backend tests pass.
