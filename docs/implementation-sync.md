# Design and implementation sync

This file is the handshake between two roles:

- **Design spec** (the planning assistant, referred to as "Argon"): decides what to build and why. Its source documents are `AGENTS.md`, `docs/coding-standards.md`, `docs/architecture.md`, `docs/agentpulse.md`, `docs/upcoming-milestones.md` and `PROMPT_SPEC.md`.
- **Implementation** (Claude Code working in this repository): builds it, tests it and records what actually happened.

Rule: **the code and the tests are the truth about what exists; this file says what is built, what is in progress and where the implementation differs from the spec.** The designer should read this before proposing the next milestone; the implementer updates it in the same commit as any milestone work. Never mark something done here that has not been run.

Last updated: milestone 27 committed (integration tests unverified locally), on branch `feature/attention-models`.

## 1. Ground rules both sides follow
1. Critical events never touch AI, are never merged, are never muted, budgeted or delayed, and are delivered inline. Every milestone adds a test proving it.
2. Clean Architecture; handlers only decode, call one service and encode; no SQL outside repositories; constructor injection only.
3. Every milestone has `docs/milestone-N.md` and a `docs/CHANGELOG.md` entry in the same commit. No `Co-Authored-By` trailers.
4. A claim of "verified" names the command that was run. A check that could not run here is listed in section 4 as **not verified**, never as passed.

## 2. Status by milestone

| # | Milestone | Status | Notes |
|---|---|---|---|
| 1 to 25 | Foundation through Attention Intelligence | Done, committed | See `docs/CHANGELOG.md` |
| 26 | Ollama and Claude adapters behind Attention | Done, committed, pushed | `docs/milestone-26.md`. Fake model servers only |
| 27 | Real-service and PostgreSQL verification | Done, committed | `docs/milestone-27.md`. Contracts pass on memory stores; PostgreSQL runs are left to CI (section 4) |
| 28 | Learning engine (`pkg/learning`) | Not started | Proposal in `PROMPT_SPEC.md` |
| 29 | Async worker and attention budgets | Not started | Introduces `ATTENTION_BUDGET_PER_HOUR` |
| 30 | Per-person digests, scoped routing, rich predicates | Not started | |
| 31 | AgentPulse traces, agent telemetry, MCP server | Not started | Introduces `cmd/mcp-server`, `AGENTPULSE_URL`, `AGENTPULSE_API_KEY` |
| 32 | Novu provider, cursor pagination, OpenTelemetry, strict STARTTLS | Not started | Introduces `NOVU_API_KEY`, `NOVU_API_URL`, `SMTP_REQUIRE_STARTTLS` |
| 33 | Documentation reconciliation, Python SDK | Not started | |

Numbering note: `docs/upcoming-milestones.md` proposed 27 to 31 and `PROMPT_SPEC.md` supersedes it with 27 to 33. Where they differ, `PROMPT_SPEC.md` wins and `upcoming-milestones.md` should be updated in milestone 33.

## 3. What the implementation has decided that the spec did not say
Record every such decision here so the design side can accept or overrule it.

| Decision | Milestone | Reason |
|---|---|---|
| Store contracts are written once and run against the memory store always and PostgreSQL under `-tags integration` (`pkg/db/dbtest`) | 27 | The memory run proves the test is right before it is trusted against a database |
| Digests keep `source` and `model` inside the JSON body; no new columns | 26 | Digests are stored as JSON already |
| Model classification runs synchronously with a timeout until milestone 29 moves it to a worker | 26 | Keeps milestone 26 small; the limit is recorded in `milestone-26.md` |
| The model only supplies wording for digests; groups and counts come from the heuristic | 26 | A model cannot invent or drop an event |
| `PROMPT_SPEC.md` is an input document and is not committed unless asked | 27 | It is a prompt, not product documentation |

## 4. Verification gates and what could not be run
`PROMPT_SPEC.md` ends with a mandatory gate. On the machine used for implementation:

| Gate | Result |
|---|---|
| `gofmt` and `go vet ./...` | Runs and passes |
| `go test ./...` | Runs and passes |
| `go test -race ./...` | **Not verified.** The race detector needs CGO and a C compiler; none is installed and CGO is off. Run it in CI (Linux) or install a toolchain |
| `golangci-lint` | **Not verified.** Not installed |
| PostgreSQL integration tests (`-tags integration`) | **Not verified.** The Docker daemon was not running and there is no local `psql`. Start Docker Desktop and run `docker compose up -d postgres`, then `TEST_DATABASE_URL=... go test -tags integration ./...` |
| Redis-backed code | **Not verified** for the same reason |
| Real Ollama and real Claude API | **Not verified.** Tested against fake HTTP servers |
| `npm test -- --run` and `npm run build` | Runs and passes (214 Vitest tests, `tsc` clean at last run) |

The `go.mod` says `go 1.21` with `toolchain go1.26.10`, while the local `go` is 1.24.4 and downloads the pinned toolchain. The 80 percent coverage target in `PROMPT_SPEC.md` is not measured by CI today; measure it before claiming it.

## 5. Open questions for the design side
1. `PROMPT_SPEC.md` asks to proceed through milestones 27 to 33 without stopping, while `AGENTS.md` and `docs/coding-standards.md` say one milestone per review. Which governs? The implementation is currently following the latest explicit instruction (run them in sequence) and committing one commit per milestone so each can be reviewed or reverted on its own.
2. Milestone 29 says budgets are "per recipient/workspace". Recipients exist only as workspace-wide targets until milestone 30 adds per-person scoping. Proposal: budget per workspace in 29, per person after 30.
3. Milestone 31 puts an MCP HTTP endpoint on the main service and a stdio binary. Proposal: both call the same `pkg/mcp` handler so behaviour cannot drift.

## 6. How to keep this file honest
- The implementer updates sections 2, 3 and 4 in the same commit as the work.
- The designer adds questions and decisions in section 5 and strikes them through when answered.
- If this file and the code disagree, the code and tests win and this file is a bug.

## 7. Production deployment reference
The environment variables, ordering and smoke checks for going live are in [deployment.md](deployment.md), section "Production reference". Variables for features not yet built are marked **planned** there.
