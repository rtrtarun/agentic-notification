# Milestone 39 plan: Observability and MCP

Status: **approved by the owner on 2026-11-21** (decisions and extra guardrails recorded at the end). Build order: 39a, 39b, 39c, 39d, stopping after each slice for review. Follows [milestone 38](milestone-38.md). Roadmap entry: [roadmap.md](../roadmap.md#milestone-39-observability-and-mcp).

## What exists today
- **Go** (milestones 31 and 32): `pkg/agentevents` normalises an agent step (`POST /api/events`; `GET /api/events?traceId=`), `pkg/traces` groups stored events by `traceId` into runs (`GET /api/traces`, `GET /api/traces/{id}`), `pkg/mcp` is a JSON-RPC 2.0 MCP server (protocol `2024-11-05`; tools `publish_event`, `get_recent_events`, `notify_user`, `explain_decision`) served in process at `POST /api/mcp` and over stdio by `cmd/mcp-server`, which calls the REST API with an API key. `pkg/telemetry` reads and writes the W3C `traceparent` header and records spans (`pipeline.Handle`, `policy.Evaluate`, `dedup.Check`, `attention.Classify`, `delivery.Send`) as debug log records. **Spans are not exported anywhere.** The `events.trace_id` and `events.parent_event_id` columns and their index exist.
- **Java:** none of it. `Event` has no `traceId` or `parentEventId`, there is no `/api/events`, `/api/traces` or `/api/mcp`, and no `traceparent` handling.
- **Not verified:** the MCP server has only been driven by tests, never by a real MCP client (finding F9).

## Guardrails
1. **The sacred invariant holds.** An agent's `severity` (including `critical`) is payload data for rules and never sets the class. MCP tools go through the same pipeline and the same validating `/notify` as REST. A critical event is still delivered inline; recording or exporting a span never delays, blocks or fails it.
2. **Exporting is optional and off by default.** With no endpoint set, behaviour is exactly today's. A slow, failing or absent collector never affects a request: spans go into a bounded in-memory buffer, and when it is full new spans are dropped and counted.
3. **No payloads leave the service in spans.** Span attributes are names, ids, attention class, decision status, rule name and durations only, never payload, titles, messages, recipients or keys.

## 1. Goals
- **39a, Java agent events and traces:** `POST /api/events`, `GET /api/events?traceId=`, `GET /api/traces`, `GET /api/traces/{id}` in Java with Go's validation, messages and output, held to a new shared contract file. The existing console Traces screen and the Python SDK then work against either service.
- **39b, Java MCP:** `POST /api/mcp` in Java with the same four tools, schemas, limits and error messages as Go, held to a shared contract file. Go's stdio `cmd/mcp-server` already talks only REST, so pointing its `--url` at the Java service gives Java a stdio MCP server too; no second stdio binary.
- **39c, OpenTelemetry export, Go first, then Java:** the spans Go already records are exported over OTLP/HTTP to any collector. Java gains the same `traceparent` handling, the same span names and the same exporter, so one request crossing both services is one trace.
- **39d, real-client verification:** the MCP server driven by a real MCP client, and spans from both services seen in a real trace viewer; results recorded, defects fixed.

## 2. Architecture decisions
- **Same layers as the rest.** Java: `AgentEvents` (normaliser) and `Traces` (pure grouping) in `application`; `Mcp` (dispatcher) in `application` behind a `Backend` interface with an in-process implementation; `Telemetry` (span context, tracer) in `application` with the exporter in `infrastructure`; `AgentApi` and `McpApi` in `interfaces`. Handlers decode, call one method, encode.
- **Contract first.** Each Java slice starts by writing its contract file and running it against today's Go; Go is the reference. A Go behaviour found to be wrong is fixed in Go in the same slice and said so.
- **OTLP without an SDK (recommended; owner decision 1).** A small exporter in each service: batches finished spans and `POST`s them as OTLP/HTTP **JSON** to `{OTEL_EXPORTER_OTLP_ENDPOINT}/v1/traces` on a timer or when a batch fills, with an explicit timeout, no retries beyond one, and a bounded buffer (drop and count when full). It keeps the existing "no tracing SDK" decision in Go and "plain JDK" in Java, adds no dependencies, and the OTLP JSON format is stable. The alternative, the official OpenTelemetry SDKs, brings auto-instrumentation and other exporters but adds a large dependency tree to both services; it can replace this later behind the same `Tracer` without touching callers.
- **Standard setting names.** `OTEL_EXPORTER_OTLP_ENDPOINT` (unset = off), `OTEL_SERVICE_NAME` (defaults `notification-service` and `notification-service-java`), `OTEL_EXPORTER_OTLP_HEADERS` (for hosted collectors; never logged), plus `OTEL_EXPORT_BATCH` and `OTEL_EXPORT_BUFFER` with safe defaults. Invalid values fail at start, as other settings do.
- **One trace across services.** Both services accept and forward `traceparent`; span names and attributes are identical in Go and Java, so a dashboard needs no per-language cases. The agent `traceId` (a run) and the W3C trace id (a request) stay separate ideas; a span carries the agent `traceId` as an attribute when there is one, so the two can be joined.
- **MCP stays as built.** Protocol `2024-11-05` and the current HTTP shape (`POST /api/mcp`, one JSON-RPC message per request, no batches) are kept for parity. Moving to a newer protocol version or the streamable HTTP transport is out of scope; 39d records what a real client needs.
- **Not in this milestone:** metrics export (milestone 41), log export over OTLP, the SDKs for other languages (43), rate limiting on these endpoints (44, F1), the 1000-event read window behind `/api/traces` (42, F10; Java reads the same window as Go).

## 3. Database changes
None. `events.trace_id`, `events.parent_event_id` and `events_trace_id_idx` exist (Go-owned, milestone 31). Java's PostgreSQL and memory event stores start writing and reading these two columns; a row written by either service reads the same in the other (tested).

## 4. APIs
- **39a (Java, matching Go):** `POST /api/events` (202 `{status, eventId, traceId, attentionClass, decisionState}`; 200 `{status:"duplicate"}` for a repeated id; 400 with Go's messages), `GET /api/events?traceId=&limit=`, `GET /api/traces?limit=` (1 to 100, default 30), `GET /api/traces/{id}` (404 `trace not found`). All behind the existing auth.
- **39b (Java, matching Go):** `POST /api/mcp`: `initialize`, `tools/list`, `tools/call`, `ping`, notifications; JSON-RPC errors with Go's codes and texts.
- **39c (both):** no new endpoints. Both services read `traceparent` on every request and return it on the response; `/health` is not traced. The exporter's sent, dropped and failed counts are logged at shutdown; exposing them as metrics is milestone 41.
- No change to `/webhooks/github`, `/notify` or any decision.

## 5. Folder changes
- Java: new `application/AgentEvents.java`, `Traces.java`, `Mcp.java`, `Telemetry.java`; `infrastructure/OtlpExporter.java`; `interfaces/AgentApi.java`, `McpApi.java`; changes to `domain/Event.java`, the event stores, `Pipeline` (spans), `Config`, `HttpApi`, `Main`.
- Go: new `pkg/telemetry/otlp.go` (exporter) and its tests; changes to `pkg/telemetry` (an exporter hook on `Span.End`), `pkg/config`, `cmd/notification-service`.
- Contract: `docs/contract/16-agent-events.json` (events and traces), `17-mcp.json`; the contract runner gains a check that a sent `traceparent` comes back with the same trace id.
- Deploy: an optional `observability` profile in `docker-compose.yml` with one trace viewer (Jaeger all-in-one, which accepts OTLP directly), off unless asked for.
- Docs: this plan, `milestone-39.md`, `java-service.md`, `implementation-sync.md`, `roadmap.md`, `deployment.md` (settings, the MCP client set-up), `runbook.md` (no spans arriving), `CHANGELOG.md`, package READMEs.

## 6. Risks
- **Leaking data through spans.** Mitigation: an allow-list of attribute keys, and a test in each service that exports a span for an event with secrets in its payload and finds none of it in the request body.
- **Collector trouble hurting requests.** Mitigation: span end only appends to a bounded buffer; the export runs on its own thread or goroutine with a timeout; a test with a collector that hangs shows request latency unchanged and spans dropped and counted. Shutdown flushes once with a deadline.
- **Hand-written OTLP drifting from the spec.** Mitigation: tests against the OTLP JSON shape (hex ids, nanosecond times as strings, `resourceSpans` and `scopeSpans`), and 39d sends to a real collector, which rejects malformed data.
- **Contract mismatch in the agent-event rules.** Go's normaliser has many caps (ids, `step_kind`, telemetry non-negative, `confidence` 0 to 1, sizes). Mitigation: the contract file covers each refusal with its exact message.
- **Old MCP protocol version.** A current client may want a newer version. Mitigation: 39d tests real clients and records the result; an upgrade becomes its own small change if needed.
- **Size.** Four slices; each is committed and reviewed separately, and I stop and ask if one runs long.

## 7. Tests
- **Unit (Java):** `AgentEventsTest` (every validation, severity never sets the class), `TracesTest` (grouping, depth from parents, totals, status from the last step, the same fixtures as Go's `traces_test.go`), `McpTest` (each method and tool, strict arguments, size limits, unknown tool and method, notification gets no reply), `TelemetryTest` (`traceparent` parsing with Go's accepted and rejected cases, child spans, a malformed header starts a new trace).
- **Unit (both):** exporter batching, the bounded buffer and drop count, timeout, one retry, shutdown flush, OTLP JSON shape, attribute allow-list, disabled when no endpoint.
- **Invariants (both):** an agent event with `severity: critical` is classified only by the rules; a critical event published through MCP is delivered inline exactly as through REST; with the collector hanging, a critical event's delivery is not delayed; with export off, decisions are identical to today.
- **Contract:** files 16 and 17 pass in Go and Java, including wrong methods, auth refusals and every validation message; the `traceparent` round trip passes in both.
- **Stores:** a traced event written by Go is read by Java with the same `traceId` and `parentEventId`, and the reverse.
- **Real stack (39d):** the Python SDK publishes a run (plan, tool failure, retry, success) to Java and the console Traces screen shows it; a real MCP client (Claude Code via `claude mcp add`, and the MCP Inspector) lists and calls all four tools through the stdio server against Go and against Java; with the `observability` profile up, one request through each service appears in Jaeger with the expected span tree and service name.
- Offline `mvn verify` keeps the coverage gate; the Go suite, `go vet` and `gofmt` stay clean; CI green.

## 8. Acceptance criteria
- Java accepts, stores and returns agent events and traces exactly as Go does, proved by contract file 16; the console Traces screen works against Java.
- Java's `POST /api/mcp` answers exactly as Go's, proved by contract file 17; the stdio MCP server works against either service.
- With `OTEL_EXPORTER_OTLP_ENDPOINT` set, both services export the same spans with the same names to a real collector; with it unset, nothing changes.
- A request carrying `traceparent` keeps its trace id through both services.
- A hanging or absent collector never changes a request's result or a critical event's timing; dropped spans are counted.
- No payload, title, message, recipient or secret appears in an exported span.
- Every invariant test passes; a real MCP client and a real trace viewer were used and the results are recorded in `milestone-39.md` and `implementation-sync.md` (F9 updated for MCP).

## Decisions (owner, 2026-11-21)
1. **Exporter:** a hand-written OTLP/HTTP JSON exporter in both services; no OpenTelemetry SDK.
2. **Slicing:** 39a, then 39b, then 39c, then 39d, with a stop and review after each slice.
3. **Real clients:** Claude Code and the MCP Inspector for MCP; Jaeger v2 (OTLP over HTTP on port 4318) in an optional compose profile.
4. **MCP protocol:** keep `2024-11-05` for parity; revisit after the 39d findings.

## Extra guardrails (owner, 2026-11-21)
- **39a:** Java stores `trace_id` and `parent_event_id` exactly as Go does (NULL versus empty string), and `Traces` breaks ties in exactly Go's order.
- **39b:** `17-mcp.json` locks the HTTP status returned for a JSON-RPC notification (such as `notifications/initialized`). `initialize` answers `2024-11-05` when a client proposes a newer version; if Go rejects that today, Go is fixed.
- **39c:** Java passes the span context explicitly (no `ThreadLocal`). The exporter retries only on a network error, 429 or 5xx, never on another 4xx. OTLP JSON rules are enforced and tested: hex ids, nanosecond times as strings, `Content-Type: application/json`. The no-secret rule covers span `status.message` as well as attributes. The `traceparent` sampled flag is handled identically in Go and Java.
