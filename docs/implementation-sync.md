# Implementation status

The single place that says **what is built, what was proven, what is open and who decided what**. Rule: the code and tests are the truth; if this file disagrees with them, this file is the bug. Update it in the same commit as any milestone work. Never mark something done that has not been run.

*Last updated: 2026-11-07, after milestone 35 (branch `feature/java-parity`). The full pre-cleanup version, with the decision log for milestones 26 to 33, is in [archived/design-notes/implementation-sync-through-m35.md](archived/design-notes/implementation-sync-through-m35.md).*

## 1. Ground rules
1. Critical events never touch AI and are never merged, muted, budgeted, delayed, deferred or queued. They are delivered inline. Every milestone adds a test proving it.
2. Clean Architecture: handlers decode, call one service, encode; no SQL outside repositories; constructor injection only.
3. Every change updates [CHANGELOG.md](CHANGELOG.md) and the relevant document in the same commit.
4. "Verified" always names the command or CI run. Checks that could not run are listed in section 4 as **not verified**.

## 2. What is built

| Area | Go service | Java service | Detail |
|---|---|---|---|
| Webhook ingestion, normalization, rules (strict YAML, locked critical rules) | Yes | Yes | [architecture.md](architecture.md), [java-service.md](java-service.md) |
| Dedup and threading (memory, or Redis shared across replicas and services) | Yes | Yes | |
| Critical path (inline, never suppressed) | Yes | Yes (9 invariant tests) | |
| `/notify`, rules and policies API, durable delivery queue | Yes | Yes | [contract/](contract/README.md) |
| Rule `payloadMatch` and per-rule `channels` | Yes | **No** (refused, not ignored) | |
| SMS, Novu, metrics, inbox, channels API, retention | Yes | **No** | |
| Accounts, sessions, roles, OpenID Connect | Yes | **No** | |
| Attention Intelligence (heuristic, Ollama, Claude), digests, budgets, async worker | Yes | **No** | |
| Feedback, overrides, learning engine | Yes | **No** | |
| AgentPulse traces, MCP server | Yes | **No** | [agentpulse.md](agentpulse.md) |
| Web console, 15 screens, demo mode | Talks to Go | Not wired | [frontend.md](frontend.md) |
| Python SDK | Talks to Go | n/a | [../sdk/python/README.md](../sdk/python/README.md) |

Milestones 1 to 33 (Go, console, SDK) are recorded in [archived/milestones/](archived/milestones/); milestones 34 and 35 (Java) in [milestones/](milestones/README.md).

## 3. Decisions in force
| Decision | Why | Source |
|---|---|---|
| Store contract tests run against memory stores always and PostgreSQL under `-tags integration` | The memory run proves the test is right before trusting it against a database | M27 |
| A model only supplies wording for digests; groups and counts come from the heuristic | A model cannot invent or drop an event | M26 |
| The attention budget is a rolling window, default 10 standard events per hour | Spec; held events are explained and digested, never dropped | M29 |
| `ATTENTION_ASYNC` defaults off; the worker always runs | Async was not run against a real database | M29 |
| Java: plain JDK, Jackson, HikariCP, no Spring; Go owns the schema; Java never migrates | Fast start; two migrators on one schema drift | M34 |
| Java: PostgreSQL classes and the Redis adapter sit outside the 80 percent coverage gate | Owner decision: `mvn verify` must pass with no database; CI runs the integration tests | M35 |
| Java: critical delivery fails if any channel fails | A partly delivered security alert must be retried, not recorded as sent | M35 |
| Java: `payloadMatch` and `channels` rules are refused | Ignoring a condition would widen a rule | M35 |
| Java SSRF check resolves and refuses private addresses just before connecting | The JDK HTTP client has no dial hook | M35 |
| Two services on one database must be configured alike | Both run a dispatcher on `delivery_jobs`; found end to end | M35 |
| Placeholder `example` identity renamed to `agentpulse` (Java package, Go module) | Owner decision after the M34 review | M34 |
| `PROMPT_SPEC.md` stays untracked | It is an input prompt, not product documentation | M27 |

## 4. Verification gates
| Gate | Status |
|---|---|
| `gofmt`, `go vet`, `go test -cover ./...` | Pass locally (35 packages) |
| `go test -race -tags integration`, `golangci-lint`, `govulncheck`, PostgreSQL 16, Redis 7 | Not runnable locally; **green in CI** |
| `npm test -- --run`, `npm run build` | Pass locally (230 Vitest tests) |
| `mvn verify` (Maven container) | Pass: 121 tests with no database, 132 with PostgreSQL and Redis; 95.8 percent in gate scope |
| Shared HTTP contract (9 files) | Pass on Go (`TestContract`) and Java (`ContractTest`) |
| Latest recorded CI | Run 42370422148 on acc8b8c: `java-service`, `go-service`, `web`, `python-sdk` all green |
| Real Ollama, Claude, Novu, SNS, OpenID provider, MCP client | **Not verified.** Fakes and replay tests only |

Coverage is below 80 percent locally in `db`, `event`, `feedback`, `inbox`, `mail`, `delivery`, `rules` and `auth` (mostly PostgreSQL code that only the integration tests reach). It is not scheduled as its own milestone; new code lands at 80 percent or more.

## 5. Open questions for the owner
1. **Two dispatchers on one database.** Keep "configure alike", or add a setting to turn a service's dispatcher off?
2. **Default budget.** `ATTENTION_BUDGET_PER_HOUR=10` silently quiets busy workspaces after upgrade. Keep it, or default to 0 and announce it?
3. **Java retention.** Port event purging (Go has it), or leave it Go-only?
4. **Milestone 36 scope** (accounts, Attention Intelligence, feedback in Java) needs its AGENTS.md plan and approval. See [roadmap.md](roadmap.md).
5. **Per-person delivery.** Personal digests are stored and readable but not delivered, because jobs hold no address. Resolve recipients at send time as its own milestone?

## 6. Owner decisions (dated)
- **2026-11-07, SDKs for other languages:** document only, do not build. Proposal and open questions in [roadmap.md](roadmap.md).
- **2026-11-07, milestone 35:** approved and built (`/notify`, rules API, durable queue, Redis dedup, contract files 07 to 09). Milestone 36 is next and still needs a plan.
- **2026-11-07, after milestone 34:** push and read the Java CI job before milestone 35 (done, green); rename `example` to `agentpulse` (done); milestone 35 scope approved.
- **2026-11-07, Java parity:** implement the Java service like the Go service, milestone by milestone, with the same guarantees, API, tests and documentation, and a shared contract extended each time. Branch `feature/java-parity`, Maven built through Docker.
- **2026-11-07, documentation cleanup:** keep a few focused, detailed current documents; move superseded ones to [archived/](archived/README.md).

## 7. Keeping this honest
Update sections 2 to 4 with the work. Add questions to section 5 and move them to section 3 or 6 when answered. Superseded notes go to the archive, never deleted.
