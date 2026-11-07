# Milestone 33: Documentation reconciliation and the Python SDK

## Goals
- Make every table, diagram claim, milestone count and endpoint list in the main documents match what milestones 1 to 33 actually built, and say plainly what has and has not been proven.
- Ship the Python helper described in `docs/agentpulse.md`, tested against the real service.

## What changed in the documents
- **`architecture.md`** (it still said "after Milestone 14"): the header, the executive summary (section 3, rewritten, with an explicit list of what is not as complete as it looks), the terminology table, the feature inventory (statuses corrected, new rows for agent traces, predicates, the worker, cursors, strict STARTTLS and per-person digests), the milestone history and quality gates (section 12, now separating what was verified locally from what CI confirmed), the roadmap (13), the delta summary (14), the endpoint table (19, every route that exists), and status-at-a-glance (22, with a "proven" column). Sections 6 to 11 and 15 to 18 describe the original pipeline and are marked as such rather than redrawn.
- **`orchestration-platform.md`**: the decision-status lifecycle, the attention budget section (default 10 per rolling hour, workspace-wide, held for the digest, never dropped), the technology table, the implementation status table and the roadmap.
- **`frontend.md`**: a table of the 15 screens with their milestones, the languages and themes, the correct test count (230), and the limits that were out of date.
- **`ui-architecture.md`**: a status note for milestone 33 and the patterns every new screen follows.
- **`deployment.md`**: the sign-in statement (accounts exist since milestone 23, with how the first admin is created), plus the production reference with every variable added since milestone 26.
- **Root `README.md`**: the current milestone and what is included.

Corrections found while reconciling, so the record is honest: NATS is described in `AGENTS.md` as part of the intended stack but is **not in the Compose file and nothing uses it**; an earlier draft said it was in the Compose file. The console has 15 screens (the pages folder has 16 files because it includes a placeholder). Nine language catalogs exist.

## The Python SDK
`sdk/python/agentpulse.py`: standard library only, no install.

- `AgentPulse(url, api_key, agent_name=...)`; `with pulse.run() as run:` starts a trace. Each step chains to the previous one with `parentEventId`, gets a deterministic ID (`<trace>-001`, so a retried publish is a duplicate, not a second event), and carries telemetry in `metadata`.
- Named steps: `plan`, `retry`, `reflect`, `needs_approval`, `complete`, and `run.tool(name)`, a timed context manager that publishes `tool.call` or, if the block raises, `tool.call.failed` with the error and duration and re-raises. Leaving the `with` block completes the run, or reports `agent.failed` if it raised; exceptions are never swallowed.
- **Reporting never breaks the agent.** By default an unreachable or refusing service is logged and the call returns `None`; `raise_errors=True` raises `AgentPulseError` with the status. The chain continues after a step that could not be sent. The key is sent only as the `X-API-Key` header and appears in no error or log.
- Read helpers: `traces()`, `trace(id)`, `explain(event_id)`.
- `example.py`: a research agent whose search times out three times, retries and finishes.

## Tests and verification
- **21 unit tests** against a local fake service (`python -m unittest` in `sdk/python`): chaining and ids, the key header, step-kind validation, lifecycle and exceptions, timed tool calls, error shortening, unreachable and refusing services with safe messages, the chain continuing after a failure, a non-JSON answer, read helpers. CI runs them in a new `python-sdk` job.
- **Run against the real service.** I built the real service binary, ran it locally, and ran `example.py` against it: 10 steps, 3 retries, status `complete`, cost `$0.0128` (the sum of the reported costs). I also drove the real `cmd/mcp-server` binary against it over stdio: `initialize`, `publish_event`, `get_recent_events` and `explain_decision` all worked, and an agent publishing `severity: "critical"` was classed `standard` by the rules, confirming in a real run that an agent cannot make its own event critical.
- Go (`gofmt`, `go vet`, `go test -cover ./...`) and web (`npm test`, `npm run build`) gates were re-run after the documentation changes and still pass; this milestone changed no Go or web code.

## Acceptance criteria
- [x] `architecture.md`, `orchestration-platform.md`, `frontend.md`, `ui-architecture.md`, `deployment.md` and the root `README.md` updated so tables, milestone counts and endpoints match milestones 1 to 33.
- [x] `sdk/python/agentpulse.py` and `example.py` emit chained `trace_id` and `parent_event_id` runs against the Go server (verified against the real binary).
- [x] The SDK has unit tests, and CI runs them.
- [ ] Every statement in the older sections (6 to 11, 15 to 18 of `architecture.md`) re-checked line by line: **not done.** They are marked as describing the original pipeline.
- [ ] The final CI result is green end to end: see the sync document. At the time of writing the Go job's lint and vulnerability steps had passed on earlier commits and the later test step had failed on two defects that were then fixed; the result after the fix was not yet read.

## Known limits
- The SDK was written for Python 3.8 and later but tested on 3.13 (locally) and 3.12 (CI), not on 3.8.
- It sends each step synchronously with a 10 second timeout; a slow service slows the agent. A background queue would fix that and was not built.
- The SDK has no retry or buffering: a step that cannot be sent is logged and dropped.
- The MCP and SDK checks used the real service binary but no real MCP client such as Claude Desktop.
