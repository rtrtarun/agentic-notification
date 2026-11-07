# Design and implementation sync

This file is the handshake between two roles:

- **Design spec** (the planning assistant, referred to as "Argon"): decides what to build and why. Its source documents are `AGENTS.md`, `docs/rules/coding-standards.md`, `docs/architecture.md`, `docs/agentpulse.md`, `docs/upcoming-milestones.md` and `PROMPT_SPEC.md`.
- **Implementation** (Claude Code working in this repository): builds it, tests it and records what actually happened.

Rule: **the code and the tests are the truth about what exists; this file says what is built, what is in progress and where the implementation differs from the spec.** The designer should read this before proposing the next milestone; the implementer updates it in the same commit as any milestone work. Never mark something done here that has not been run.

Last updated: milestone 33 complete. All milestones 27 to 33 are committed on branch `feature/attention-models`. CI is the remaining gate: see section 9.

## 1. Ground rules both sides follow
1. Critical events never touch AI, are never merged, are never muted, budgeted or delayed, and are delivered inline. Every milestone adds a test proving it.
2. Clean Architecture; handlers only decode, call one service and encode; no SQL outside repositories; constructor injection only.
3. Every milestone has `docs/milestones/milestone-N.md` and a `docs/CHANGELOG.md` entry in the same commit, together with this file. No `Co-Authored-By` trailers.
4. A claim of "verified" names the command that was run. A check that could not run here is listed in section 4 as **not verified**, never as passed.

## 2. Status by milestone

| # | Milestone | Status | Notes |
|---|---|---|---|
| 1 to 25 | Foundation through Attention Intelligence | Done, committed | See `docs/CHANGELOG.md` |
| 26 | Ollama and Claude adapters behind Attention | Done, committed, pushed | `docs/milestones/milestone-26.md`. Fake model servers only |
| 27 | Real-service and PostgreSQL verification | Done, committed, pushed | `docs/milestones/milestone-27.md`. Contracts pass on memory stores; PostgreSQL runs are left to CI |
| 28 | Learning engine (`pkg/learning`) | Done, committed | `docs/milestones/milestone-28.md`. Guardrail tested at suggest and apply time; PostgreSQL repository left to CI |
| 29 | Async worker and attention budgets | Done, committed | `docs/milestones/milestone-29.md`. PostgreSQL queue and Redis script left to CI; default budget of 10 per hour flagged in section 5 |
| 30 | Per-person digests, scoped routing, rich predicates | Done, committed | `docs/milestones/milestone-30.md`. Per-person digests built as filtered views (approved); personal email delivery, schedules and console screens are not included |
| 31 | AgentPulse traces, agent telemetry, MCP server | Done, committed | `docs/milestones/milestone-31.md`. Not tried with a real MCP client; the PostgreSQL trace round trip left to CI. Dedup bug fixed along the way 
| 32 | Novu provider, cursor pagination, OpenTelemetry, strict STARTTLS | Done, committed | `docs/milestones/milestone-32.md`. Tracing is context propagation and span logs, not an OpenTelemetry exporter; Novu not run against the real service 
| 33 | Documentation reconciliation, Python SDK | Done, committed | `docs/milestones/milestone-33.md`. SDK verified against the real service and MCP binaries; older architecture sections 6 to 11 and 15 to 18 are marked, not rechecked line by line 
| 34 | **Java parity**: implement the Java service the same way the Go service was built | **Planned, not started** | Decided by the project owner. Needs a proposal first (what "the same" covers, in what order) and approval, per AGENTS.md |

Numbering note: `docs/upcoming-milestones.md` proposed 27 to 31 and `PROMPT_SPEC.md` supersedes it with 27 to 33. Where they differ, `PROMPT_SPEC.md` wins and `upcoming-milestones.md` will be updated in milestone 33.

## 3. What the implementation has decided that the spec did not say
Record every such decision here so the design side can accept or overrule it.

| Decision | Milestone | Reason | Design verdict |
|---|---|---|---|
| Store contracts are written once and run against the memory store always and PostgreSQL under `-tags integration` (`pkg/db/dbtest`) | 27 | The memory run proves the test is right before it is trusted against a database | **Accepted.** Keep this pattern for all new stores in M28 to M32 |
| Digests keep `source` and `model` inside the JSON body; no new columns | 26 | Digests are stored as JSON already | **Accepted.** Avoids redundant schema columns |
| Model classification runs synchronously with a timeout until milestone 29 moves it to a worker | 26 | Keeps milestone 26 small; the limit is recorded in `milestones/milestone-26.md` | **Accepted.** Move to the async worker in M29 |
| The model only supplies wording for digests; groups and counts come from the heuristic | 26 | A model cannot invent or drop an event | **Accepted.** Preserve this guardrail permanently |
| `PROMPT_SPEC.md` is an input document and is not committed unless asked | 27 | It is a prompt, not product documentation | **Accepted.** Keep it untracked; commit this file |
| Attention budget uses a rolling window (sorted set in Redis), not fixed hourly buckets | 29 | The spec says "rolling window"; a fixed bucket allows twice the cap across a boundary | Pending review |
| `ATTENTION_ASYNC` defaults to `false`; the worker always runs so delayed events are released in both modes | 29 | Async changes when delivery happens and was not run against a real database; opt-in is safer | Pending review |
| `ATTENTION_BUDGET_PER_HOUR` defaults to 10 as in the spec, which **changes behaviour**: a busy workspace's 11th standard notification in an hour is now held for the digest | 29 | Followed the spec; held events are explained and appear in the digest, never dropped | **Needs a ruling:** keep 10, or default to 0 (off) and let operators opt in |
| The work queue lives on the `events` table (columns `attention_*`) rather than a separate table | 29 | The spec asked for `FOR UPDATE SKIP LOCKED` on `events`; a column marks what is still undecided | Pending review |
| The budget is skipped when no standard route is configured, and for "always notify" overrides | 29 | Nothing interrupts anyone in those cases | Pending review |

## 4. Verification gates and what could not be run
`PROMPT_SPEC.md` ends with a mandatory gate. Run every available local gate after each milestone and record what happened.

| Gate | Local requirement and status |
|---|---|
| `gofmt -s -w .` and `go vet ./...` | **Mandatory local gate.** Ran and passed after milestone 27 |
| `go test -cover ./...` | **Mandatory local gate.** Ran and passed after milestone 29 (new `pkg/budget` 96.6, `pkg/learning` 80.9, `pkg/pipeline` 95.6, `pkg/attention` 87.3 percent); the table below is from milestone 27 |
| `go test -race ./...` | **Not verifiable locally** (no CGO or C compiler); **confirmed in CI**: run 42356002884 on commit 367d735 passed `go test -race -tags integration ./...` |
| `golangci-lint` | **Not available locally**; **confirmed in CI**: lint (and govulncheck) pass on commit 367d735, after about 25 findings across four rounds were fixed |
| PostgreSQL integration tests (`-tags integration`) | **Not runnable locally** (Docker daemon stopped, no `psql`); **confirmed in CI** against PostgreSQL 16 (commit 367d735). The first real run found two defects the in-memory tests could not see; both were fixed |
| Redis-backed code | **Not runnable locally**; **confirmed in CI** against Redis 7: the budget contract ran through the real Lua script (commit 367d735) |
| Real Ollama and real Claude API | **Not verified locally.** Replay tests through the real clients cover success, timeout, breaker, rate cap and redaction on the wire |
| `npm test -- --run` and `npm run build` | **Mandatory local gate.** Ran after milestone 28: 220 tests pass, `tsc -b` clean, `npm run build` succeeds |
| CI result for this branch | **Read from the public GitHub API.** Run 42356002884 on commit 367d735: `go-service` (format, vet, lint, vulnerability scan, race and integration tests, coverage gate) **success**, `python-sdk` success, `web` success, `java-service` **failure** (code this work did not touch). CI had not run on this branch at all until milestone 29, because `ci.yml` only triggered on `main`, `feature-impl` and pull requests into `main`; `feature/**` was added |

### Measured coverage after milestone 27 (`go test -cover`, no integration tag)
The 80 percent target in `PROMPT_SPEC.md` is **not met by every package** locally. Most gaps are PostgreSQL code that only the integration tests reach, so CI's figure (with `-tags integration`) will be higher.

| At or above 80 percent | Below 80 percent |
|---|---|
| attention 85, attention/llm 92, channels 95, claude 94, config 94, dedup 88, insights 92, integrations 100, metrics 92, notification 100, notification/impl 100, ollama 90, overrides 82, pipeline 97, policy 86, preferences 82, server 85, sms 89, stream 96 | **db 48, event 59, feedback 69, inbox 70, mail 72, delivery 76, rules 75, auth 79** |

Raising these is not scheduled as its own milestone. New code in milestones 28 to 33 should land at or above 80 percent on its own.

The `go.mod` says `go 1.21` with `toolchain go1.26.10`, while the local `go` is 1.24.4 and downloads the pinned toolchain.

## 5. Open questions for the design side (resolved)
1. ~~`PROMPT_SPEC.md` asks to proceed through milestones 27 to 33 without stopping, while `AGENTS.md` and `docs/rules/coding-standards.md` say one milestone per review. Which governs?~~
   - **Design ruling:** Proceed sequentially through milestones 27 to 33 without stopping, with **one atomic commit per milestone** (updating `docs/milestones/milestone-N.md`, `docs/CHANGELOG.md` and this file in each). This keeps each milestone reviewable and revertable while completing the build in one run.
2. ~~Milestone 29 says budgets are "per recipient/workspace". Recipients exist only as workspace-wide targets until milestone 30 adds per-person scoping.~~
   - **Design ruling:** Approved. Workspace-scoped budgets in M29 (`scope = "workspace"`); extend the key to `user:<id>`, falling back to `workspace`, in M30.
3. ~~Milestone 31 puts an MCP HTTP endpoint on the main service and a stdio binary. Proposal: both call the same handler.~~
   - **Design ruling:** Approved. `pkg/mcp` holds the protocol types, tool registry and JSON-RPC 2.0 dispatcher. `cmd/mcp-server` (stdio) and `/api/mcp` both delegate to it.

## 6. How to keep this file honest
- The implementer updates sections 2, 3 and 4 in the same commit as the work.
- The designer adds questions and decisions in section 5 and strikes them through when answered.
- If this file and the code disagree, the code and tests win and this file is a bug.
- This is the one canonical copy. The designer's working copy `docs/design_and_implementation_sync.md` was merged into it; delete that copy, or tell me to, so the two cannot drift.

## 7. Production deployment reference
The environment variables, ordering and smoke checks for going live are in [deployment.md](deployment.md), section "Production reference". Variables for features not yet built are marked **planned** there and promoted to **active** as each milestone lands.

## 8. Open questions for the design side (new)
1. **Default budget.** `ATTENTION_BUDGET_PER_HOUR` is 10 by default per `PROMPT_SPEC.md`. That silently quiets busy workspaces after upgrade. Keep it, or default to 0 and announce it?
2. **CI never ran on this branch** until milestone 29 added `feature/**` to its triggers. Open a pull request or watch the Actions tab for the first real result of the PostgreSQL, Redis, race, lint and coverage gates.
3. **Per-person digests (milestone 30, part 2).** Events, overrides and delivery targets are workspace-wide and jobs store no address, so "a person's digest" is undefined. Proposal: per-user preferences with workspace fallback; a per-person digest is the workspace digest filtered by that person's mutes and "never" choices, stored and readable in the app, on request first and on a schedule later; per-person email delivery as its own milestone that adds recipient resolution to delivery. Which model do you want?

## 9. CI findings so far (read from the public GitHub API)
- CI had never run on this branch before milestone 29. Its first runs failed at **Lint**; about 25 findings across four rounds (gosec, errorlint, staticcheck, bodyclose, unused, ineffassign) were fixed. The next failure was the integration tests, which found two real defects (overrides could not be saved in PostgreSQL; a `TRUNCATE` across a foreign key). **After those fixes the whole `go-service` job passed** on commit 367d735 (run 42356002884): format, vet, lint, vulnerability scan, `go test -race -tags integration` against PostgreSQL 16 and Redis 7, and the 80 percent coverage gate. `web` and `python-sdk` pass too.
- The `java-service` job (`mvn verify`) fails in `Build and test`. That is code this work did not touch; it needs its own look.
- `golangci-lint` could not be run locally (building it from source was blocked by the tool-use policy), so every lint finding came from CI, one round trip at a time.

## 10. Decisions from the project owner
1. **Java parity (recorded 2026-11-07).** The Java service (`java-server/notification-service`) is to be implemented the same way the Go service was: the same layers, the same guarantees (above all, critical events never touch AI, are never merged, muted, delayed, budgeted or deferred, and cannot be created by an agent's own severity), the same API, tests and documentation, built milestone by milestone with a review after each. Nothing has been started. Facts to plan from: the Java service predates this work and was never reviewed; its CI job (`mvn verify`) fails; the Compose file points its build at a folder that does not exist (`./java-server/production`).
   - **Suggested first step, not yet agreed:** write the proposal that AGENTS.md requires (goals, decisions, folders, risks, tests, acceptance) and decide what "the same" means: all of milestones 1 to 33, or the core first (ingestion, rules, critical delivery, dedup) and the rest later.
   - **Suggested way to check parity:** a single HTTP contract test suite (requests and expected answers) that both services must pass, so "the same" is verified by tests instead of by reading code.
