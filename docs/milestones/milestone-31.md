# Milestone 31: AgentPulse traces, agent telemetry and an MCP server

## Goals
- Treat the steps of an AI agent as first-class events: chained into runs (traces), carrying telemetry, and sent through the **same** deterministic pipeline as a webhook.
- Show a run as an ordered timeline with its cost, retries, duration and the decision the platform made for each step.
- Let Claude Desktop, Cursor or any MCP client publish steps, read runs, send a validated notification and ask why.

## Architecture decisions
- **Events gain three optional fields**: `traceId`, `parentEventId`, `agentName` (idempotent migration, partial index on `trace_id`). Telemetry stays inside the payload's `metadata` (`step_kind`, `tool_name`, `attempt`, `duration_ms`, `tokens_in`, `tokens_out`, `cost_usd`, `confidence`), so rules can match it with the milestone 30 predicates (`metadata.attempt gt 3`, `metadata.confidence lt 0.7`). No new rule language was needed.
- **Ingestion** (`POST /api/events`, `pkg/agentevents`). Input is validated and normalised (event type, ids, parent needs a trace, a known `step_kind`, non-negative numbers, `confidence` in 0 to 1, size caps) and handed to `pipeline.Handle`: same rules, dedup, preferences, budget, worker and delivery. A repeated `id` answers `200 {"status":"duplicate"}`. `/notify` is unchanged.
- **An agent cannot make its own event critical.** Its `severity` is stored as payload data for rules to read; it never sets the attention class. Only a rule decides, so the sacred invariant is intact (tested over REST and MCP).
- **Dedup fix found while building this.** Deduplication keyed on thread, type and action, so the retries of one run collapsed into the first and the "agent keeps retrying" alert never fired. Traced events are now one thread per run, and two steps merge only when they share the run, type, parent, step kind and attempt. A webhook's dedup is unchanged.
- **Trace read API** (`pkg/traces`, pure functions over stored events). `GET /api/traces` lists runs (duration, steps, retries, failures, tokens, cost, worst attention class, status: running, waiting, complete or failed). `GET /api/traces/{id}` returns the steps in time order with depth from the parent chain and the decision for each. It reads the most recent 1000 events.
- **MCP** (`pkg/mcp`). One transport-free dispatcher (JSON-RPC 2.0: `initialize`, `ping`, `tools/list`, `tools/call`; notifications get no answer; batches refused) with four tools: `publish_event`, `get_recent_events`, `notify_user`, `explain_decision`. Tools act through a `Backend`. **Both transports share the same dispatcher**, as the design ruling required: `cmd/mcp-server` (stdio, newline-delimited, logs to stderr) uses `mcp.HTTPBackend` against the REST API with an API key, and `POST /api/mcp` uses an in-process backend. Tool arguments are decoded strictly (a misspelt argument is a tool error, not ignored), bounded in size, and failures come back as readable tool errors. `notify_user` goes through the validating notification service, so the SSRF host allow-list and recipient limits apply. A client can do through the tools only what the API key already allows.
- **Console.** A **Traces** screen: a list of runs, and for the selected run a timeline indented by parent chain, duration bars (failed steps in the distress colour), retry badges, tokens, cost, confidence, the decision text, and **Inspect**, which opens the normal detail drawer. The hosted demo ships three runs: a tool timeout then three retries until a threshold rule alerts Slack, a run paused for human approval, and a clean run. The demo steps are also ordinary demo events, so Inspect works there. No new UI dependencies.

## Database changes
`events.trace_id`, `parent_event_id`, `agent_name` (all nullable, added with `ADD COLUMN IF NOT EXISTS`) and index `events_trace_id_idx` (partial, `trace_id IS NOT NULL`). Guarded by migration tests and a storage round-trip contract (memory and PostgreSQL).

## APIs
| Route | Purpose |
|---|---|
| `POST /api/events` | Ingest one agent step. 202 accepted, 200 duplicate, 400 invalid |
| `GET /api/events?traceId=&limit=` | Recent events newest first, one run with `traceId` |
| `GET /api/traces?limit=` | Runs with totals and status |
| `GET /api/traces/{id}` | One run's ordered steps and decisions |
| `POST /api/mcp` | MCP over HTTP: one JSON-RPC message per request (202 for a notification) |

All require the API key or a session. `GET /events` is unchanged apart from trace fields.

## Using it
Build the stdio server: `go build -o bin/mcp-server ./cmd/mcp-server`. Configure the client with `AGENTPULSE_URL` and `AGENTPULSE_API_KEY` (see `docs/deployment.md`). Example rule for runaway retries, saved through the Rules API or YAML:

```yaml
- name: agent-retries
  when:
    source: agent
    payloadMatch: [{field: metadata.attempt, op: gt, value: 3}]
  then: {attentionClass: standard, reason: an agent keeps retrying, channels: [slack]}
```

## Risks
| Risk | Handling |
|---|---|
| An agent flooding the pipeline | Same API-key auth, body limit, dedup, attention budget and rules as any source |
| A tool abused through an LLM | Strict arguments, size caps, the validating notify service, and the key's own permissions |
| A prompt-injected agent claiming severity | Severity is data only; rules decide the class |
| A very busy store hides old runs | Traces read the latest 1000 events (documented limit) |

## Tests
`pkg/agentevents` (98.5 percent): normalisation, defaults and generated ids, 18 invalid shapes, severity never sets the class, no shared maps. `pkg/traces` (98.8): totals, ordering, depth, every status, worst class, grouping, malformed parent chains, string and junk telemetry. `pkg/dedup`: retries are distinct, the same step twice still merges, a run is one thread. `pkg/event`: trace fields round trip on memory and PostgreSQL. `pkg/mcp` (95.0): protocol and error codes, notifications, the four tools, argument strictness, bounds, backend errors, and the HTTP backend (key header, endpoints, safe error messages, odd replies). `cmd/mcp-server`: the stdio loop, cancellation, oversized lines; the real binary was also run by hand. `pkg/server`: a six-step failing run through the real pipeline firing the threshold rule to SMS and reading back through the trace API, severity cannot make an event critical, duplicates and bad input, filters, limits and access, and the MCP endpoint end to end. Web: formatting, demo runs (timeout, three retries, alert, approval pause, parent chain, cost totals, never critical), demo events, both clients (230 Vitest tests).

## Acceptance criteria
- [x] Agent events accepted through `POST /api/events` and normalised into the same pipeline; threshold rules evaluate them on ingest.
- [x] `GET /api/traces` and `GET /api/traces/{id}` with the ordered timeline and per-step decisions.
- [x] Traces screen with timeline, duration bars, retry badges, token and cost breakdown and Inspect; demo mode complete.
- [x] MCP server over stdio and over HTTP from one shared dispatcher, with the four tools.
- [x] A critical event can never be created by an agent's own severity (tests over REST and MCP).
- [x] `gofmt`, `go vet` (also `-tags integration`), `go test -cover ./...`, `npm test`, `npm run build` pass locally.
- [x] The PostgreSQL trace-field round trip passes against a real database, confirmed in CI run 42356002884 on commit 367d735.
- [ ] Verified against Claude Desktop or Cursor: **not done.** The protocol was exercised by tests and by piping messages into the real binary, not by a real client.

## Known limits
- MCP over HTTP is plain request and response; there is no server-sent event stream, so server-initiated notifications are not supported.
- The Traces screen cannot yet filter or search runs, and the Rules screen has no fields for predicates.
- Trace totals come from what the agent reports; the platform does not measure tokens or cost itself.
- Per-trace data is limited to the most recent 1000 events, and there is no separate trace table.
