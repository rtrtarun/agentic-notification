# Milestone 39: Observability and MCP

Status: **slices 39a and 39b merged to `master` on 2026-11-21** (PR #20 and PR #21; CI green on `master`, runs 44879506261 and 44879871735). **Slice 39c (OpenTelemetry span export) built; awaiting the owner's review.** 39d (real-client verification) is not started. Plan, with the owner's four decisions and extra guardrails: [milestone-39-plan.md](milestone-39-plan.md).

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
- [x] CI green on the pull request (PR #20: go-service, java-service, python-sdk, web all passed).

## Slice 39b: Java MCP

### Goals (met)
- Java answers `POST /api/mcp` exactly as Go does: `initialize`, `ping`, `tools/list`, `tools/call` with the four tools (`publish_event`, `get_recent_events`, `notify_user`, `explain_decision`), notifications, and every JSON-RPC error, held to a new shared contract file written and run against Go first.
- Go's stdio `cmd/mcp-server` works against the Java service unchanged (`AGENTPULSE_URL` pointed at Java), so Java has a stdio MCP server without a second binary.

### Owner guardrails
- **Notification status locked.** A message without an id (such as `notifications/initialized`) gets **HTTP 202 with no body** in both services; contract file 17 checks the status and that the body is empty (new `bodyEmpty` expectation).
- **Protocol negotiation.** A client proposing a newer protocol (`2025-06-18`) or an older one (`2024-10-07`) is answered with `2024-11-05`; Go already did this (it never rejected a proposed version), so **no Go fix was needed**. Locked in the contract.

### Decisions
- **A port of `pkg/mcp`**: the same methods, the same four tool descriptions and schemas (contract file 17 holds Go's whole `tools/list` answer, so a changed word in either service fails), the same limits (arguments at most 64 KiB, titles 200 and messages 4000 **bytes**, 10 recipients per channel, `limit` 1 to 50, default 20), and the same answers for duplicates, refusals and backend failures.
- **Go's JSON-RPC decoding, exactly.** The request id is echoed as Go writes a `json.RawMessage` (the written bytes, compacted, `< > &` escaped), so `1.50` stays `1.50`; keys match without regard to case with the last match winning; a `jsonrpc` or `method` of the wrong type is a parse error (-32700), as Go's decoder reports it; `id: null` is a request, not a notification. New `RawJson` reads raw byte spans with Jackson so none of this depends on a parsed tree.
- **Kept for parity (documented):** a request without an id for a method outside `notifications/` still runs (a `tools/call` sent as a notification publishes its event) but is never answered. The contract locks it.
- **Go change: plain argument errors.** Go's `decodeStrict` returned `encoding/json`'s text, which names Go types (`Go struct field In.limit of type int`, `agentevents.Input`) and changes between Go versions. It now says `unknown argument "x"`, `argument limit must be a whole number`, `argument emailRecipients must be a list of text`, `arguments must be an object`. Java writes the same words; the first problem in document order wins in both. Tested in Go (`TestArgumentErrorsArePlainAndStable`) and Java (`McpTest`).
- **Tool results** are written with sorted map keys, as Go writes a map, so the same answer is the same text where Go uses maps.
- **In process, like Go.** `McpApi.InProcess` uses the same pipeline, the validating `Notify.Service`, the event list's summary and the heuristic explainer as the REST API; the in-process server reports version `service`, as Go's does. Java explains with the heuristic only (Java has no language model, as before).
- No database change.

### APIs
`POST /api/mcp` (API key or session): 200 with the JSON-RPC answer, 202 with no body for a notification, 405 for other methods, 413 for a body over `MAX_BODY_BYTES`.

### Folder changes
- Java: new `application/Mcp.java` (server, tools, strict arguments), `application/RawJson.java`, `interfaces/McpApi.java` (HTTP and the in-process backend); `interfaces/HttpApi.java` (route; the list summary is shared with MCP).
- Go: `pkg/mcp/mcp.go` (argument messages) and its tests; the contract runner (`bodyEmpty`, `textJson`).
- Contract: `docs/contract/17-mcp.json` (39 steps); `README.md` documents the two new expectations, also implemented in the Java runner.

### Risks and how they were handled
- Drift from Go: file 17 covers auth, methods, both negotiations, three kinds of notification, text and null ids, the full tool list, batches, parse and invalid-request errors, unknown methods and tools, every tool's success, duplicate and refusal paths, argument type errors, and a rule-made critical step through MCP. The new `bodyEmpty` and `textJson` checks were each seen to fail on a wrong expectation before the file was trusted.
- Critical leakage: `severity: critical` through MCP stays standard without a rule (contract and `McpApiTest`); with a rule it is critical and delivered inline before the answer.
- Known differences left: Go accepts invalid UTF-8 (replacing it) where Jackson refuses the message as invalid JSON; Go escapes `< > &` inside tool-result text, Java does not (the JSON means the same).

### Tests
- Contract: file 17 passes in Go (`TestContract`) and Java (`ContractTest`, 17 files).
- Java: `McpTest` (12: raw id echo, key matching, malformed messages, notifications, every argument error, Go decoding of repeated and null values, byte limits, sorted result keys), `McpApiTest` (5: critical path, storage, provider and explainer failures, HTTP rules, recent events). Offline `mvn verify`: **376 tests (5 skipped), 90.4 percent of lines**; the MCP classes 94 to 100 percent.
- Go: `go test ./...`, `go vet` (with and without `-tags integration`) and `gofmt` clean.
- Real stack: Go's stdio `mcp-server` against the Java jar: `initialize` (proposing `2025-06-18`), `notifications/initialized` (no answer), `publish_event`, `get_recent_events`, `explain_decision` and a refused `notify_user` all answered as against Go. A real MCP client (Claude Code, MCP Inspector) is slice 39d.

### Review checklist
- [x] Contract file 17 and the Go change to plain argument errors: merged by the owner as PR #21. Whether a tool call sent as a notification should keep running is still open question 4 in [implementation-sync.md](../implementation-sync.md#5-open-questions-for-the-owner).
- [x] CI green on the pull request (PR #21) and on `master` after the merge (run 44879871735).

## Slice 39c: OpenTelemetry span export (Go, then Java)

### Goals (met)
- The spans both services record are sent to any OpenTelemetry collector over OTLP/HTTP JSON when `OTEL_EXPORTER_OTLP_ENDPOINT` is set; with it unset nothing changes.
- Java gains Go's `traceparent` handling and the same spans (names, kinds, parents, attributes), so one caller's trace through either service looks the same.

### Owner guardrails (all met)
- **Explicit span context in Java.** `Telemetry.SpanContext` is passed as a parameter: request wrapper, then webhook, `/api/events` and MCP handlers, then `Pipeline.handle(event, parent)`, then each step. No `ThreadLocal`, and no `HttpExchange` attribute (the JDK server can keep those on the shared context).
- **Retries only on a network error, 429 or 5xx**, once; any other 4xx is never retried and counts the batch as failed. A test matrix in each service (503, 429, 500/502, 400, 401, refused connection).
- **OTLP JSON rules**: lower-case hex ids, nanosecond times written as strings, kinds and status codes as numbers, integer attributes as strings, `Content-Type: application/json`, path `/v1/traces`. Go and Java tests check the same expected strings.
- **No secrets**: an allow-list of nine attribute keys (anything else is ignored, values cut to 200 characters), and a failed span's `status.message` is always the fixed word `failed`, never the error text (which stays in the local debug log). Tests export spans for events with secrets in the payload and errors with passwords in the message and find none of it in the export.
- **Sampled flag identical in Go and Java**: only bit 0 of the flags means sampled (`00`, `02` not sampled; `01`, `03`, `ff` sampled); a new trace is sampled; a child keeps its parent's flag; only sampled spans are exported. Contract file 18 checks it in both.

### Decisions
- **Hand-written exporter** (owner decision 1): Go `pkg/telemetry/otlp.go`, Java `infrastructure/OtlpExporter.java`; no OpenTelemetry SDK. `Export` never blocks: a bounded buffer (`OTEL_EXPORT_BUFFER`, default 2048) drops and counts spans when full; a background loop sends batches (`OTEL_EXPORT_BATCH`, default 100) every two seconds or when one fills; each request has a five-second timeout; shutdown flushes within five seconds and logs `sent`, `dropped`, `failed`.
- **Server spans** are named `<method> <route>` with Go's route labels (Java ports the same list in `Routes`, including routes only Go serves, so labels match), and carry method, route and status code; a 5xx marks the span failed. **`/health` is not traced** (as the plan says): no span and no `traceparent` header. This changes Go, which used to trace it; the Go test that used `/health` now uses `/events`.
- **The pipeline span** names the decision: `event.source`, `event.type`, `agent.trace_id`, `attention.class`, `decision.status`, `rule.name`; a redelivery is not failed and adds no decision.
- **Settings** (both services, same names): `OTEL_EXPORTER_OTLP_ENDPOINT` (http or https, no user, password, query or fragment), `OTEL_EXPORTER_OTLP_HEADERS` (URL-encoded values, never logged, a bad entry named by position only), `OTEL_SERVICE_NAME` (defaults `notification-service` and `notification-service-java`), `OTEL_EXPORT_BATCH`, `OTEL_EXPORT_BUFFER`. Invalid values stop the start. `service.version` is `APP_VERSION`.
- Java's exporter never interrupts its worker: shutdown sets a flag the worker checks every 50 ms, so an interrupt can never cut a send short (found by a test, fixed before commit).
- No database change. Exporting counters as metrics is milestone 41.

### APIs
No new endpoints. Every response except `/health` carries `traceparent`.

### Folder changes
- Go: `pkg/telemetry/telemetry.go` (kinds, attributes, exporter hook, server span with status), new `pkg/telemetry/otlp.go`, `pkg/config` (settings), `pkg/server` (wiring, flush on shutdown, `traceRoute`), `pkg/pipeline` (decision attributes).
- Java: new `application/Telemetry.java`, `infrastructure/OtlpExporter.java`, `interfaces/Routes.java`; `Pipeline` (spans, `handle(event, parent)`), `HttpApi` (`serve`: request span, header, explicit context), `AgentApi`, `McpApi` (per-request backend), `Main`.
- Contract: new `docs/contract/18-traceparent.json` (16 steps); both runners gain the `traceparent` expectation.

### Risks and how they were handled
- **A collector hurting requests:** a new invariant test in each service sends 20 critical events through a pipeline whose exporter points at a collector that never answers; all are delivered inline within two seconds and the full buffer drops spans. The exporters' own tests show `Export` returns at once and shutdown gives up at its deadline.
- **Leaking data:** the allow-list, the fixed status message, and tests with secrets in payloads and error text.
- **Hand-written OTLP drifting from the spec:** shared expected strings in both services; a real collector and trace viewer are slice 39d.

### Tests
- Contract: 18 files pass in Go and Java; the `traceparent` checks were each seen to fail on a wrong expectation.
- Go: `otlp_test.go` (JSON rules, batching and headers, retry matrix, non-blocking and drop count, hanging collector, endpoint rules, sampling and the allow-list, server span), two pipeline tests (decision attributes without payload; the hanging-collector invariant), config tests (defaults, decoding, refusals, no header value in errors), `TestHealthIsNotTraced`. `go test ./...`, `go vet` and `gofmt` clean; the integration suite passes against PostgreSQL 16 and Redis 7; `pkg/telemetry` 97.6 percent, `pkg/pipeline` 96.2 percent.
- Java: `TelemetryTest` (8), `OtlpExporterTest` (6), `HttpTracingTest` (4), one more `SacredInvariantTest` case (17 now). Offline `mvn verify`: **395 tests, 5 skipped, 90.7 percent**; with PostgreSQL and Redis: **424 tests, none skipped, 95.0 percent**. (A 39a test now looks only at its own rows, because the Go integration suite can share its database.)
- Real stack: the Go binary and the Java jar on one PostgreSQL, both exporting to one fake OTLP collector; the same caller trace sent to `POST /api/events` on each gave the same span tree (`POST /api/events` then `pipeline.Handle` then `policy.Evaluate`, `dedup.Check`, `attention.Classify`) with identical attributes, under `notification-service` and `notification-service-java`, version `0.39.0`; the URL-encoded header arrived decoded; the payload secret was not exported; an unsampled request was not exported.

### Review checklist
- [ ] Contract file 18 reads as the behaviour we want (especially: `/health` is not traced, and only bit 0 of the flags means sampled).
- [ ] The Go change that stops tracing `/health` is acceptable.
- [ ] CI green on the pull request.
