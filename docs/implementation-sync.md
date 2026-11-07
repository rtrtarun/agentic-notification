# Design and implementation sync

This file is the handshake between two roles:

- **Design spec** (the planning assistant, referred to as "Argon"): decides what to build and why. Its source documents are `AGENTS.md`, `docs/coding-standards.md`, `docs/architecture.md`, `docs/agentpulse.md`, `docs/upcoming-milestones.md` and `PROMPT_SPEC.md`.
- **Implementation** (Claude Code working in this repository): builds it, tests it and records what actually happened.

Rule: **the code and the tests are the truth about what exists; this file says what is built, what is in progress and where the implementation differs from the spec.** The designer should read this before proposing the next milestone; the implementer updates it in the same commit as any milestone work. Never mark something done here that has not been run.

Last updated: milestone 27 committed; milestones 28 to 33 approved for sequential execution on branch `feature/attention-models`.

## 1. Ground rules both sides follow
1. Critical events never touch AI, are never merged, are never muted, budgeted or delayed, and are delivered inline. Every milestone adds a test proving it.
2. Clean Architecture; handlers only decode, call one service and encode; no SQL outside repositories; constructor injection only.
3. Every milestone has `docs/milestone-N.md` and a `docs/CHANGELOG.md` entry in the same commit, together with this file. No `Co-Authored-By` trailers.
4. A claim of "verified" names the command that was run. A check that could not run here is listed in section 4 as **not verified**, never as passed.

## 2. Status by milestone

| # | Milestone | Status | Notes |
|---|---|---|---|
| 1 to 25 | Foundation through Attention Intelligence | Done, committed | See `docs/CHANGELOG.md` |
| 26 | Ollama and Claude adapters behind Attention | Done, committed, pushed | `docs/milestone-26.md`. Fake model servers only |
| 27 | Real-service and PostgreSQL verification | Done, committed, pushed | `docs/milestone-27.md`. Contracts pass on memory stores; PostgreSQL runs are left to CI |
| 28 | Learning engine (`pkg/learning`) | **Approved for execution** | Human-reviewable suggestions from feedback and the event trail; never alters locked or critical rules |
| 29 | Async worker and attention budgets | **Approved for execution** | Workspace-level budget (`ATTENTION_BUDGET_PER_HOUR`); async worker drains non-critical queued events |
| 30 | Per-person digests, scoped routing, rich predicates | **Approved for execution** | Strict YAML decoding, payload predicates, per-user digests and preferences, per-person budget scope |
| 31 | AgentPulse traces, agent telemetry, MCP server | **Approved for execution** | Shared `pkg/mcp` handler backing both `cmd/mcp-server` (stdio) and `/api/mcp` (HTTP); Trace Explorer UI |
| 32 | Novu provider, cursor pagination, OpenTelemetry, strict STARTTLS | **Approved for execution** | `NOVU_API_KEY`, `NOVU_API_URL`, `SMTP_REQUIRE_STARTTLS`, `?cursor=`, trace spans |
| 33 | Documentation reconciliation, Python SDK | **Approved for execution** | Reconcile `architecture.md`, `orchestration-platform.md`, `upcoming-milestones.md`, `deployment.md`; add `sdk/python/agentpulse.py` |

Numbering note: `docs/upcoming-milestones.md` proposed 27 to 31 and `PROMPT_SPEC.md` supersedes it with 27 to 33. Where they differ, `PROMPT_SPEC.md` wins and `upcoming-milestones.md` will be updated in milestone 33.

## 3. What the implementation has decided that the spec did not say
Record every such decision here so the design side can accept or overrule it.

| Decision | Milestone | Reason | Design verdict |
|---|---|---|---|
| Store contracts are written once and run against the memory store always and PostgreSQL under `-tags integration` (`pkg/db/dbtest`) | 27 | The memory run proves the test is right before it is trusted against a database | **Accepted.** Keep this pattern for all new stores in M28 to M32 |
| Digests keep `source` and `model` inside the JSON body; no new columns | 26 | Digests are stored as JSON already | **Accepted.** Avoids redundant schema columns |
| Model classification runs synchronously with a timeout until milestone 29 moves it to a worker | 26 | Keeps milestone 26 small; the limit is recorded in `milestone-26.md` | **Accepted.** Move to the async worker in M29 |
| The model only supplies wording for digests; groups and counts come from the heuristic | 26 | A model cannot invent or drop an event | **Accepted.** Preserve this guardrail permanently |
| `PROMPT_SPEC.md` is an input document and is not committed unless asked | 27 | It is a prompt, not product documentation | **Accepted.** Keep it untracked; commit this file |

## 4. Verification gates and what could not be run
`PROMPT_SPEC.md` ends with a mandatory gate. Run every available local gate after each milestone and record what happened.

| Gate | Local requirement and status |
|---|---|
| `gofmt -s -w .` and `go vet ./...` | **Mandatory local gate.** Ran and passed after milestone 27 |
| `go test -cover ./...` | **Mandatory local gate.** Ran and passed after milestone 27; per-package coverage below |
| `go test -race ./...` | **Not verified locally** (no CGO or C compiler). Enforced in `.github/workflows/ci.yml` |
| `golangci-lint` | **Not verified locally** (not installed). Enforced in `.github/workflows/ci.yml` |
| PostgreSQL integration tests (`-tags integration`) | **Not verified locally** (Docker daemon stopped, no `psql`). Contract suites pass against the in-memory stores and `go vet -tags integration ./...` compiles them. CI runs them against PostgreSQL 16 |
| Redis-backed code | **Not verified locally.** Use an in-memory store in unit tests |
| Real Ollama and real Claude API | **Not verified locally.** Replay tests through the real clients cover success, timeout, breaker, rate cap and redaction on the wire |
| `npm test -- --run` and `npm run build` | **Mandatory local gate.** Ran after milestone 26 (214 tests); re-run after every frontend change |
| CI result for this branch | **Not seen.** The `gh` CLI is not installed here, so the CI run cannot be read from this machine. Check the Actions tab; milestone 27's integration tests are only proven once that run is green |

### Measured coverage after milestone 27 (`go test -cover`, no integration tag)
The 80 percent target in `PROMPT_SPEC.md` is **not met by every package** locally. Most gaps are PostgreSQL code that only the integration tests reach, so CI's figure (with `-tags integration`) will be higher.

| At or above 80 percent | Below 80 percent |
|---|---|
| attention 85, attention/llm 92, channels 95, claude 94, config 94, dedup 88, insights 92, integrations 100, metrics 92, notification 100, notification/impl 100, ollama 90, overrides 82, pipeline 97, policy 86, preferences 82, server 85, sms 89, stream 96 | **db 48, event 59, feedback 69, inbox 70, mail 72, delivery 76, rules 75, auth 79** |

Raising these is not scheduled as its own milestone. New code in milestones 28 to 33 should land at or above 80 percent on its own.

The `go.mod` says `go 1.21` with `toolchain go1.26.10`, while the local `go` is 1.24.4 and downloads the pinned toolchain.

## 5. Open questions for the design side (resolved)
1. ~~`PROMPT_SPEC.md` asks to proceed through milestones 27 to 33 without stopping, while `AGENTS.md` and `docs/coding-standards.md` say one milestone per review. Which governs?~~
   - **Design ruling:** Proceed sequentially through milestones 27 to 33 without stopping, with **one atomic commit per milestone** (updating `docs/milestone-N.md`, `docs/CHANGELOG.md` and this file in each). This keeps each milestone reviewable and revertable while completing the build in one run.
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
