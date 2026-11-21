# Implementation status

The single place that says **what is built, what was proven, what is open and who decided what**. Rule: the code and tests are the truth; if this file disagrees with them, this file is the bug. Update it in the same commit as any milestone work. Never mark something done that has not been run.

*Last updated: 2026-11-21, after milestone 39b (merged to `master` as PR #21). The version before the 2026-11-07 cleanup, with the decision log for milestones 26 to 33, is [archived/design-notes/implementation-sync-through-m35.md](archived/design-notes/implementation-sync-through-m35.md).*

## 1. Ground rules
1. Critical events never touch AI and are never merged, muted, budgeted, delayed, deferred or queued. They are delivered inline. Every milestone adds a test proving it.
2. Clean Architecture: handlers decode, call one service, encode; no SQL outside repositories; constructor injection only.
3. Every change updates [CHANGELOG.md](CHANGELOG.md) and the relevant document in the same commit.
4. "Verified" always names the command or CI run. Checks that could not run are listed in section 4 as **not verified**.

## 2. What is built

| Area | Go service | Java service | Detail |
|---|---|---|---|
| Webhook ingestion, normalization, rules (strict YAML, locked critical rules) | Yes | Yes (M34) | [architecture.md](architecture.md), [java-service.md](java-service.md) |
| Dedup and threading (memory, or Redis shared across replicas and services) | Yes | Yes (M34, M35) | |
| Critical path (inline, never suppressed) | Yes | Yes (16 invariant tests) | |
| `/notify`, rules and policies API, durable delivery queue | Yes | Yes (M35, contract 07 to 09) | [contract/](contract/README.md) |
| Rule `payloadMatch` and per-rule `channels` | Yes | Yes (M37a, contract 11) | |
| Accounts, sessions, roles, sign-up with admin approval | Yes | Yes (M36, contract 10) | |
| OpenID Connect single sign-on | Yes | **No** (M46) | |
| Feedback (`/feedback`) and always/never overrides (`/api/overrides`) | Yes | Yes (M37b, contract 12) | Overrides cache is per process in both |
| Heuristic Attention Intelligence: classifier, explanations, digests, attention budget | Yes | Yes (M37c, contract 13; digests stored, not delivered; no personal digests) | Budget shared through Redis |
| Language-model classification and digest wording (Ollama, Claude) | Yes | **No** (refused at start) | |
| Quiet-hours preferences, async attention worker | Yes | **No** | |
| Learning suggestions (review, apply, dismiss) | Yes | Yes (M38a, contract 14) | Same suggestion id hash |
| Learning score tuning (offline, approval-gated) | Yes (M38b, contract 15) | Yes (M38c, contract 15) | Same arithmetic, ids and shared tables; one running run across both |
| Agent steps and traces (`/api/events`, `/api/traces`) | Yes | Yes (M39a, contract 16) | Same tie order; chain columns NULL outside a run |
| MCP server (`/api/mcp`; stdio through Go's `cmd/mcp-server`) | Yes | Yes (M39b, contract 17; the stdio server also works against Java) | Protocol `2024-11-05` |
| `traceparent` propagation and span logs | Yes | **No** (M39c) | No export to a collector yet in either service |
| SMS, Novu, channels API | Yes | **No** (M40) | |
| Metrics and analytics | Yes | **No** (M41) | |
| Inbox, event retention | Yes | **No** (M42) | |
| Web console, 15 screens, demo mode | Talks to Go | Not wired | [frontend.md](frontend.md) |
| Python SDK | Talks to Go | Uses only `/api/events`, which Java serves since M39a; not yet run against Java | [../sdk/python/README.md](../sdk/python/README.md) |

Finished milestone records, 1 to 38, are in [archived/milestones/](archived/milestones/); the current one is in [milestones/](milestones/README.md).

## 3. Decisions in force
| Decision | Why | Source |
|---|---|---|
| Store contract tests run against memory stores always and PostgreSQL under `-tags integration` | The memory run proves the test is right before trusting it against a database | M27 |
| A model only supplies wording for digests; groups and counts come from the heuristic | A model cannot invent or drop an event | M26 |
| The attention budget is a rolling window, default 10 standard events per hour | Spec; held events are explained and digested, never dropped | M29 |
| `ATTENTION_ASYNC` defaults off; the worker always runs | Async was not run against a real database | M29 |
| Java: plain JDK, Jackson, HikariCP, no Spring; Go owns the schema; Java never migrates | Fast start; two migrators on one schema drift | M34 |
| Every Java feature is held to a shared contract file written and run against Go first; Go is the reference, and a Go behaviour found wrong is fixed in Go in the same slice | "The same" is proven by tests, not by reading code | M34 onward |
| Java: PostgreSQL classes and the Redis adapter sit outside the 80 percent coverage gate | Owner decision: `mvn verify` must pass with no database; CI runs the integration tests | M35 |
| Java: critical delivery fails if any channel fails | A partly delivered security alert must be retried, not recorded as sent | M35 |
| Java SSRF check resolves and refuses private addresses just before connecting | The JDK HTTP client has no dial hook (finding F6) | M35 |
| Two services on one database run one queue dispatcher (`DISPATCHER_ENABLED=false` on the other), one digest schedule and one learning schedule | Each runs its own background loops on shared tables | M35, PR #13, M37c, M38 |
| Sign-up is off by default; when on, an account starts **pending** and an admin must approve it | One workspace: everyone signed in sees every event | M36 |
| The API key is for services; the console hides it under "Advanced", and signing out forgets it | People use accounts; a key left in the tab is access | M36 |
| Java hashes passwords as Go does (argon2id, same format, BouncyCastle) | An account made by one service signs in on the other | M36 |
| Images are published for both services tagged with the `VERSION` file (for example `0.39.0`, `0.39`, `latest`, `sha-<commit>`); `/health` returns the version; each new version also gets a GitHub Release | A running service can be identified without a commit hash | M36 |
| Learning is offline or asynchronous and never in the live path; **every adjustment needs an admin's approval**, whatever its size; defaults ±20 points, 5 signals from 2 people, 30-day lookback, 14-day half-life | Predictability and auditability first | M38 plan |
| Event lists break `created_at` ties by `id` in every store (PostgreSQL and memory, Go and Java) | One order everywhere, so traces match between services and between test and production | M39a |
| MCP argument refusals are the service's own words (`unknown argument "x"`, `argument limit must be a whole number`), not `encoding/json`'s text | Go's text named Go types and changed between Go versions | M39b |
| MCP stays on protocol `2024-11-05` and answers it whatever the client proposes; revisit after the real-client check | Parity first | M39 plan |
| The OTLP span export is hand-written OTLP/HTTP JSON in both services, off unless `OTEL_EXPORTER_OTLP_ENDPOINT` is set, and never carries payloads or secrets | No SDK dependency tree; a collector can never slow a request | M39 plan |
| Placeholder `example` identity renamed to `agentpulse` (Java package, Go module) | Owner decision after the M34 review | M34 |
| `PROMPT_SPEC.md` stays untracked | It is an input prompt, not product documentation | M27 |

## 4. Verification gates
| Gate | Status |
|---|---|
| `gofmt`, `go vet` (with and without `-tags integration`), `go test ./...` | Pass locally (milestone 39b) |
| Go integration tests (`-tags integration`) against PostgreSQL 16 and Redis 7 | Pass locally (milestone 39a, 2026-11-21) and in CI |
| `go test -race`, `golangci-lint`, `govulncheck` | Not runnable locally; **green in CI** |
| `mvn verify` (Maven container) | Offline: 376 tests, 5 skipped, 90.4 percent (milestone 39b). With PostgreSQL and Redis: 387 tests, none skipped, 94.7 percent (milestone 39a) |
| `npm test -- --run`, `npm run build` | Pass locally (238 Vitest tests, milestone 36; the console has not changed since) |
| Shared HTTP contract (17 files) | Pass on Go (`TestContract`) and Java (`ContractTest`) |
| Go and Java on one PostgreSQL | One agent run published through both gave identical traces (M39a); Go's stdio MCP server answered against the Java jar as against Go (M39b) |
| Latest recorded CI | `master` after merging PR #20 (run 44879506261) and PR #21 (run 44879871735): `go-service`, `java-service`, `web`, `python-sdk` all green. The "Publish service images" workflow succeeded on both merges (runs 44879506245, 44879871660); `VERSION` is still `0.36.0`, so no new GitHub Release was made |
| Sign-up, approval and sign-in in a browser against the live service | **Not verified** |
| Real Ollama, Claude, Novu, SNS, OpenID provider | **Not verified.** Fakes and replay tests only (milestone 48) |
| Real MCP client, real trace viewer | **Not verified** (slice 39d) |

Coverage is below 80 percent locally in `db`, `event`, `feedback`, `inbox`, `mail`, `delivery`, `rules` and `auth` (mostly PostgreSQL code only the integration tests reach); raising them is housekeeping in milestone 45. New code lands at 80 percent or more.

## 5. Open questions for the owner
1. **Default budget.** `ATTENTION_BUDGET_PER_HOUR=10` quietly holds events in busy workspaces after an upgrade. Keep it, or default to 0 and announce it?
2. **Critical delivery inside the request (F3).** Keep it (a failure makes the sender retry) or accept a bounded wait? Today the slowest channel sets webhook latency (10 s provider timeout, 40 s server write timeout).
3. **Versioning policy.** Answered for now: a release is cut when the owner asks, with the minor number following the milestone (`0.39.0` covers milestones 37 to 39b). Still open: start at `1.0.0` for the first hosted release?
4. **MCP and notifications.** A `tools/call` sent as a JSON-RPC notification (no id) still runs, in both services, and is locked in contract file 17 for parity. Keep it, or refuse to run tools without an id?

Answered or scheduled: two dispatchers (`DISPATCHER_ENABLED`, PR #13); Java retention and the F10 read window (milestone 42); rate limiting F1, the critical double send F2 and the trusted proxy F11 (milestone 44); per-person delivery, email verification and password reset (milestone 46); multi-tenancy (milestone 49). See [roadmap.md](roadmap.md).

## 6. Audit findings
First logged on 2026-11-07 by a reverse-engineering audit of the code (archived as [archived/design-notes/feature-flows.md](archived/design-notes/feature-flows.md), section 6). They were read from the code, not reproduced. An item is closed only when a milestone changes the code and a test proves it.

| ID | Severity | Finding | State |
|---|---|---|---|
| F1 | High | No inbound rate limiting on ingestion endpoints (limiters exist only for sign-in, channel tests, model calls, SMS) | Open; milestone 44 (also `/api/mcp` and `/api/traces`) |
| F2 | High | Check-then-act on duplicate deliveries: two concurrent deliveries of one critical event can both send (`Exists` then `Save` in the pipeline, both services) | Open; milestone 44 (moved from 45 by the owner, 2026-11-21) |
| F3 | Medium | Critical delivery runs inside the webhook request; latency equals the slowest channel | Open question 2 |
| F4 | Medium | Workers poll PostgreSQL, so deferred and retried work waits up to one interval; NATS unused | Milestone 47 |
| F5 | Medium | Two dispatchers on one database can dead-letter a job for a missing channel | Mitigated by `DISPATCHER_ENABLED` (PR #13) |
| F6 | Medium | Java SSRF check is pre-flight, not dial-time, so a changing DNS answer is not covered | Open; milestone 44 |
| F7 | Medium | Dedup, budget and classifier fail open by design: no alert is hidden, duplicates or extra interruptions are possible | Accepted behaviour; documented in [architecture.md](architecture.md) |
| F8 | Medium | Java had no accounts; the API key was its only gate | **Closed** in milestone 36 |
| F9 | Medium | Ollama, Claude, Novu, SNS, OpenID Connect, a real MCP client and the Java SMTP success path have only run against fakes | Open; MCP client in slice 39d, the rest in milestone 48 |
| F10 | Medium | Inbox, digests, learning, traces, MCP reads and explanations read the newest 1000 events into memory (`event.List` in Go, `listRecent` in Java): older events are invisible to them and cost grows per request | Open; milestone 42 |
| F11 | Medium | Behind a proxy every person shares one address, so the per-address sign-up and sign-in limits are shared by everyone | Open; milestone 44 |
| F12 | Medium | Sessions need the console and the API on one site, or `AUTH_COOKIE_SAMESITE=none`; browsers that block third-party cookies still refuse it | Open; milestone 44 |
| F13 | Medium | An approved member sees every event: one workspace, no tenancy | Open; milestone 49 |
| F14 | Medium | CI did not run on `master`, the default branch | **Closed** in milestone 36; CI runs on `master` (runs above) |
| L1 | Low | Local secrets are hardcoded in `docker-compose.yml` (`local-dev-key`, webhook secret, SMTP password), local use only | Milestone 44 (secret management) |
| L2 | Low | Not audited: every list query for an unbounded `SELECT` | Milestone 45 |
| L3 | Low | Not profiled: inbox and analytics queries for N+1 patterns | Milestone 45 |
| L4 | Low | Not inspected: query plans of heavy queries | Milestone 45 |
| L5 | Low | Not searched: every discarded error | Milestone 45 |

## 7. Owner decisions (dated)
- **2026-11-21, milestone 39 plan:** approved with four decisions (hand-written OTLP/HTTP JSON exporter in both services; slices 39a to 39d with a stop after each; Claude Code, the MCP Inspector and Jaeger v2 for the real-client check; MCP protocol `2024-11-05` kept) and extra guardrails per slice, recorded in [milestones/milestone-39-plan.md](milestones/milestone-39-plan.md).
- **2026-11-21, slice 39a review:** the Go tie-break change and Java's fixed order for two bad ids accepted; the in-memory stores to order like PostgreSQL (done). Merged as PR #20.
- **2026-11-21, roadmap:** F2 moves into milestone 44; milestone 48 is split into 48 Production readiness and 49 Hosted product; milestone 43 records the SDK leanings (hand-written, TypeScript and Go first, `/notify` covered).
- **2026-11-21, release:** cut release `0.39.0` (milestones 37 to 39b), the first since `0.36.0`.
- **2026-11-21, documentation:** finished milestone records (34 to 38), the AgentPulse MVP vision and the milestone 36 audit moved to the archive; current documents brought up to milestone 39b.
- **2026-11-10, milestone 38 plan:** approved with six decisions (admin approval for every adjustment; the defaults in section 3; per-person preferences left to milestone 46; manual runs by default; Go changes accepted; three slices).
- **2026-11-10, milestone 37:** Java parity for rule fields, feedback and overrides, and heuristic Attention Intelligence, built in three slices with a contract file each; merged as PR #14.
- **2026-11-07, milestone 36:** the console starts at sign-in, the API key is "Advanced", sign-up with admin approval (default off), Java gets the same accounts, and both services are published as versioned images from `0.36.0`.
- **2026-11-07, Java parity:** implement the Java service like the Go service, milestone by milestone, with the same guarantees, API, tests and documentation, and a shared contract extended each time.
- **2026-11-07, repository secret:** `API_KEY` added to the GitHub repository secrets; where every secret lives and how to rotate them is in [deployment.md](deployment.md#secrets-and-variables).
- **2026-11-07, documentation cleanup:** keep a few focused, detailed current documents; move superseded ones to [archived/](archived/README.md).

## 8. Keeping this honest
Update sections 2 to 4 with the work. Add questions to section 5 and move them to section 3 or 7 when answered. Log every new finding in section 6 with an ID and where it is tracked. Superseded notes go to the archive, never deleted.
