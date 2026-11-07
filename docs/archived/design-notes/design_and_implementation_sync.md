> **Archived 2026-11-07.** Historical; it does not describe the current system. Current documents: [docs/README.md](../../README.md).

# Design and implementation sync

This file is the handshake between two roles:

- **Design spec** (the planning assistant, referred to as "Argon"): decides what to build and why. Its source documents are `AGENTS.md`, `docs/rules/coding-standards.md`, `docs/architecture.md`, `docs/agentpulse.md`, `docs/upcoming-milestones.md` and `PROMPT_SPEC.md`.
- **Implementation** (Claude Code working in this repository): builds it, tests it and records what actually happened.

Rule: **the code and the tests are the truth about what exists; this file says what is built, what is in progress and where the implementation differs from the spec.** The designer should read this before proposing the next milestone; the implementer updates it in the same commit as any milestone work. Never mark something done here that has not been run.

Last updated: design sync approved for sequential execution of milestones 27 to 33 on branch `feature/attention-models`.

## 1. Ground rules both sides follow
1. Critical events never touch AI, are never merged, are never muted, budgeted or delayed, and are delivered inline. Every milestone adds a test proving it.
2. Clean Architecture; handlers only decode, call one service and encode; no SQL outside repositories; constructor injection only.
3. Every milestone has `docs/milestones/milestone-N.md` and a `docs/CHANGELOG.md` entry in the same commit. No `Co-Authored-By` trailers.
4. A claim of "verified" names the command that was run. A check that could not run here is listed in section 4 as **not verified**, never as passed.

## 2. Status by milestone

| # | Milestone | Status | Notes |
|---|---|---|---|
| 1 to 25 | Foundation through Attention Intelligence | Done, committed | See `docs/CHANGELOG.md` |
| 26 | Ollama and Claude adapters behind Attention | Done, committed, pushed | `docs/milestones/milestone-26.md`. Fake model servers only |
| 27 | Real-service and PostgreSQL verification | **Approved to complete** | Contract tests in `pkg/db/dbtest` run against memory store always and PostgreSQL under `-tags integration`. Commit once all store contracts pass `go test ./...` |
| 28 | Learning engine (`pkg/learning`) | **Approved for execution** | Human-reviewable suggestions from feedback + event trail; never alters locked/critical rules |
| 29 | Async worker and attention budgets | **Approved for execution** | Workspace-level budget in M29 (`ATTENTION_BUDGET_PER_HOUR`); async worker drains non-critical queued events |
| 30 | Per-person digests, scoped routing, rich predicates | **Approved for execution** | Strict YAML decoding, payload predicates, per-user digests/preferences, and per-person budget scope |
| 31 | AgentPulse traces, agent telemetry, MCP server | **Approved for execution** | Shared `pkg/mcp` handler backing both `cmd/mcp-server` (stdio) and `/api/mcp` (HTTP); Trace Explorer UI in `apps/web` |
| 32 | Novu provider, cursor pagination, OpenTelemetry, strict STARTTLS | **Approved for execution** | `NOVU_API_KEY`, `NOVU_API_URL`, `SMTP_REQUIRE_STARTTLS`, `?cursor=` pagination, trace spans |
| 33 | Documentation reconciliation, Python SDK | **Approved for execution** | Reconcile `architecture.md`, `orchestration-platform.md`, `upcoming-milestones.md`, `deployment.md`, and add `sdk/python/agentpulse.py` |

Numbering note: `docs/upcoming-milestones.md` proposed 27 to 31 and `PROMPT_SPEC.md` supersedes it with 27 to 33. Where they differ, `PROMPT_SPEC.md` wins and `upcoming-milestones.md` will be updated in milestone 33.

## 3. What the implementation has decided that the spec did not say
Record every such decision here so the design side can accept or overrule it.

| Decision | Milestone | Reason | Design Verdict |
|---|---|---|---|
| Store contracts are written once and run against the memory store always and PostgreSQL under `-tags integration` (`pkg/db/dbtest`) | 27 | The memory run proves the test is right before it is trusted against a database | **Accepted.** Keep this pattern for all new stores in M28–M32 |
| Digests keep `source` and `model` inside the JSON body; no new columns | 26 | Digests are stored as JSON already | **Accepted.** Avoids redundant schema columns |
| Model classification runs synchronously with a timeout until milestone 29 moves it to a worker | 26 | Keeps milestone 26 small; the limit is recorded in `milestones/milestone-26.md` | **Accepted.** Move to the async worker in M29 |
| The model only supplies wording for digests; groups and counts come from the heuristic | 26 | A model cannot invent or drop an event | **Accepted.** Preserve this deterministic guardrail permanently |
| `PROMPT_SPEC.md` is an input document and is not committed unless asked | 27 | It is a prompt, not product documentation | **Accepted.** Keep `PROMPT_SPEC.md` untracked; commit `docs/implementation-sync.md` |

## 4. Verification gates and what could not be run
`PROMPT_SPEC.md` ends with a mandatory gate. On the machine used for implementation, run every available local gate and record the exact output:

| Gate | Local Requirement / Status |
|---|---|
| `gofmt -s -w .` and `go vet ./...` | **Mandatory local gate.** Must run and pass after every milestone |
| `go test -cover ./...` | **Mandatory local gate.** Run with `-cover` (does not require CGO) to measure statement coverage per package and verify tests pass |
| `go test -race ./...` | **Not verified locally** (no CGO / C compiler installed). Keep enforced in `.github/workflows/ci.yml` for Linux CI |
| `golangci-lint` | **Not verified locally** (not installed). Keep enforced in `.github/workflows/ci.yml` |
| PostgreSQL integration tests (`-tags integration`) | **Not verified locally** when Docker daemon is stopped. Ensure shared contract suites pass against in-memory implementations locally and compile cleanly with `go vet -tags integration ./...` |
| Redis-backed code | **Not verified locally** when Docker daemon is stopped. Test against mock/in-memory `KeyStore` / budget store locally |
| Real Ollama and real Claude API | **Not verified locally.** Tested against deterministic `httptest.Server` fakes covering success, timeout, circuit-breaker trip, and rate-cap fallback |
| `npm test -- --run` and `npm run build` | **Mandatory local gate.** Must run and pass after every frontend change |

## 5. Open questions for the design side (Resolved)
1. ~~`PROMPT_SPEC.md` asks to proceed through milestones 27 to 33 without stopping, while `AGENTS.md` and `docs/rules/coding-standards.md` say one milestone per review. Which governs?~~
   - **Design Ruling:** Proceed sequentially through **Milestones 27 to 33 without stopping**, creating **one atomic commit per milestone** (updating `docs/milestones/milestone-N.md`, `docs/CHANGELOG.md`, and `docs/implementation-sync.md` in each commit). This preserves single-milestone reviewability and revert safety while completing the full build in one run.
2. ~~Milestone 29 says budgets are "per recipient/workspace". Recipients exist only as workspace-wide targets until milestone 30 adds per-person scoping. Proposal: budget per workspace in 29, per person after 30.~~
   - **Design Ruling:** **Approved.** Implement workspace-scoped budgets in Milestone 29 (`scope = "workspace"`), and extend budget keys to support `user_id` (`scope = "user:<id>"` falling back to `"workspace"`) when per-person scoping lands in Milestone 30.
3. ~~Milestone 31 puts an MCP HTTP endpoint on the main service and a stdio binary. Proposal: both call the same `pkg/mcp` handler so behaviour cannot drift.~~
   - **Design Ruling:** **Approved.** Create `pkg/mcp` holding the protocol types, tool registry, and JSON-RPC 2.0 dispatcher. Both `cmd/mcp-server` (stdio transport) and `/api/mcp` on the main HTTP server must delegate to `pkg/mcp` so tool schemas and execution paths never drift.

## 6. How to keep this file honest
- The implementer updates sections 2, 3 and 4 in the same commit as the work.
- The designer adds questions and decisions in section 5 and strikes them through when answered.
- If this file and the code disagree, the code and tests win and this file is a bug.

## 7. Production deployment reference
The environment variables, ordering and smoke checks for going live are in [deployment.md](../../deployment.md), section "Production reference". Variables for features not yet built are marked **planned** there and promoted to **active** as each milestone lands.