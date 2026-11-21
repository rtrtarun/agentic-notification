# Milestone 39: Observability and MCP

Status: **slice 39a reviewed and approved by the owner on 2026-11-21** (both decisions accepted); 39b in progress; 39c and 39d not started. Plan, with the owner's four decisions and extra guardrails: [milestone-39-plan.md](milestone-39-plan.md).

## Slice 39a: Java agent events and traces

### Goals (met)
- Java accepts, stores and returns agent steps and traces exactly as Go does: `POST /api/events`, `GET /api/events?traceId=`, `GET /api/traces`, `GET /api/traces/{id}`.
- A shared contract file proves it; it was written and run against today's Go before any Java.
- The console's Traces screen and the Python SDK now work against either service (they use only these endpoints).

### Decisions
- **Ports, not new designs.** `AgentEvents` is Go's `pkg/agentevents` (same checks in the same order, same messages, the same generated id `agent-<20 hex>`), `Traces` is Go's `pkg/traces` (same arithmetic, same JSON). An agent's `severity` is stored in the payload as data and never sets the class.
- **Exact storage (owner guardrail).** Java writes NULL, never `''`, in `trace_id`, `parent_event_id` and `agent_name` for an event outside a run, as Go's `nullString` does, and reads NULL back as empty. Both are tested against PostgreSQL in each service.
- **Exact tie-breaking (owner guardrail).** Go's PostgreSQL list sorted only by `created_at`, so two events stored at the same instant came back in whatever order the database chose. **Go change:** it now sorts `created_at DESC, id DESC` (what Java already did), so the order is fixed. `Traces` reads events oldest first and sorts steps with a stable sort, so steps at one instant keep that order; traces that ended together are ordered by trace id. After review, **both in-memory stores** (Go `InMemoryRepository.List`, Java `InMemoryEventRepository.listRecent`) were changed from insertion order to the same `(created_at, id)` order, so offline runs cannot diverge from PostgreSQL. New tests pin this in Go (`TestTiesKeepTheOrderTheyArriveIn`, `TestPostgresListBreaksTimeTiesByID`, `TestInMemoryListOrdersLikePostgres`) and Java (`TracesTest`, `AgentApiTest`, `PostgresEventRepositoryTest`, `InfrastructureTest`).
- **Go's JSON, byte for byte where it matters.** A whole-number float is written `0`, not `0.0`; Go's `omitempty` fields are left out when zero; `confidence: 0` is kept; the accepted answer always has `traceId`, empty outside a run. Rounding of the cost is Go's `math.Round` (halves away from zero, exact for `0.49999999999999994`).
- **Go's body decoding.** `encoding/json` matches keys without regard to case and the last match wins, a JSON null leaves a text field unset, and a value of the wrong type makes the whole body `invalid request payload`; `AgentApi.decode` does the same. Trimming is Go's `strings.TrimSpace` (it also trims a non-breaking space; Java's `strip()` would not).
- **One known Go quirk kept.** Go checks the three ids in map order, which is random, so an input with two bad ids may name either one. Java checks in a fixed order; the contract only sends one bad id at a time.
- No database change: the columns and index are Go's, from milestone 31.

### APIs
`POST /api/events` (202 `{status, eventId, traceId, attentionClass, decisionState}`; 200 `{status:"duplicate"}`; 400 with Go's messages), `GET /api/events` and `GET /events` (`?traceId=` narrows; the list now shows `traceId`, `parentEventId`, `agentName`), `GET /api/traces?limit=` (1 to 100, default 30), `GET /api/traces/{id}` (404 `trace not found`; a deeper path is 404 `not found`). All need the API key or a session.

### Folder changes
- Java: new `application/AgentEvents.java`, `application/Traces.java`, `interfaces/AgentApi.java`; `domain/Event.java` (three fields), `infrastructure/PostgresEventRepository.java` (three columns), `interfaces/HttpApi.java` (routes, the `traceId` filter and the chain fields in the list).
- Go: `pkg/event/postgres_repository.go` (the tie-break), tests in `pkg/traces` and `pkg/event`.
- Contract: `docs/contract/16-agent-events.json`.

### Risks and how they were handled
- Drift from Go: contract file 16 (37 steps) covers auth, wrong methods, every validation message, a run with a duplicate, severity as data, a rule-made critical step delivered inline, an orphan parent, `confidence: 0`, list order, limits and the 404s. A deliberately wrong expectation was seen to fail before the file was trusted.
- Critical leakage: a new invariant test arms every non-critical mechanism and shows that `severity: critical` alone is not critical, and that a step a rule makes critical never reaches the classifier, is never merged and is delivered inline.
- Data window: like Go, traces read the newest 1000 events (finding F10, milestone 42).

### Tests
- Contract: file 16 passes in Go (`TestContract`) and Java (`ContractTest`, 16 files).
- Java: `AgentEventsTest` (7), `TracesTest` (13, Go's fixtures plus ties, number parsing, rounding and the JSON shape), `AgentApiTest` (7), two PostgreSQL tests, one invariant test. `mvn verify`: **387 tests with PostgreSQL and Redis, none skipped, 94.7 percent of lines**; offline 358 (5 skipped), 89.7 percent; 359 offline after the in-memory order follow-up. The three new classes are fully covered.
- Go: `go test ./...`, `go vet` (with and without `-tags integration`) and `gofmt` clean; the integration suite passes against PostgreSQL 16 and Redis 7. The new tie test was seen to fail when the tie-break was reversed.
- Real stack (PostgreSQL 16, the Go binary and the Java jar on one database): one run published step by step through Java and Go in turn; `GET /api/traces/{id}` and `GET /api/events?traceId=` were identical from both services (timestamps compared as instants); Java answered `duplicate` for a step Go had stored; no row stored `''` in the chain columns.

### Review checklist
- [x] Contract file 16 reads as the behaviour we want (approved 2026-11-21).
- [x] The Go tie-break change (`created_at DESC, id DESC`) is accepted; the in-memory stores now match it.
- [x] Java's fixed order for two bad ids (Go's is random) is accepted.
- [ ] CI green on the pull request.
