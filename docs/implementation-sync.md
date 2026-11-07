# Implementation status

The single place that says **what is built, what was proven, what is open and who decided what**. Rule: the code and tests are the truth; if this file disagrees with them, this file is the bug. Update it in the same commit as any milestone work. Never mark something done that has not been run.

*Last updated: 2026-11-07, after milestone 36 (branch `feature/signup-and-sign-in`, not merged yet; the live service still runs the previous release). The full pre-cleanup version, with the decision log for milestones 26 to 33, is in [archived/design-notes/implementation-sync-through-m35.md](archived/design-notes/implementation-sync-through-m35.md).*

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
| Accounts, sessions, roles | Yes | Yes (milestone 36) | [milestones/milestone-36.md](milestones/milestone-36.md) |
| Sign-up with admin approval (`AUTH_SIGNUP=approval`, default off) | Yes | Yes | same |
| OpenID Connect single sign-on | Yes | **No** | |
| Attention Intelligence (heuristic, Ollama, Claude), digests, budgets, async worker | Yes | **No** | |
| Feedback, overrides, learning engine | Yes | **No** | |
| AgentPulse traces, MCP server | Yes | **No** | [agentpulse.md](agentpulse.md) |
| Web console, 15 screens, demo mode | Talks to Go | Not wired | [frontend.md](frontend.md) |
| Python SDK | Talks to Go | n/a | [../sdk/python/README.md](../sdk/python/README.md) |

Milestones 1 to 33 (Go, console, SDK) are recorded in [archived/milestones/](archived/milestones/); milestones 34 to 36 in [milestones/](milestones/README.md).

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
| Sign-up is off by default; when on, an account starts **pending** and an admin must approve it | One workspace: everyone signed in sees every event, so sign-up cannot grant access by itself | M36 |
| The API key is for services; the console hides it under "Advanced", and signing out forgets it | People use accounts; a key left in the tab is access | M36 |
| Java hashes passwords as Go does (argon2id, same format, BouncyCastle) | An account made by one service signs in on the other | M36 |
| Images are published for both services (`notification-service`, `notification-service-java`) tagged with the `VERSION` file (`0.36.0`, `0.36`, `latest`, `sha-<commit>`); `/health` returns the version | A running service can be identified without a commit hash. Pre-1.0 the minor number follows the milestone | M36 |
| Placeholder `example` identity renamed to `agentpulse` (Java package, Go module) | Owner decision after the M34 review | M34 |
| `PROMPT_SPEC.md` stays untracked | It is an input prompt, not product documentation | M27 |

## 4. Verification gates
| Gate | Status |
|---|---|
| `gofmt`, `go vet`, `go test -cover ./...` | Pass locally (35 packages) |
| `go test -race -tags integration`, `golangci-lint`, `govulncheck`, PostgreSQL 16, Redis 7 | Not runnable locally; **green in CI** |
| `npm test -- --run`, `npm run build` | Pass locally (230 Vitest tests) |
| `mvn verify` (Maven container) | Pass: 156 tests with no database (95.2 percent in gate scope), 170 with PostgreSQL (3 Redis tests skipped) |
| Go integration tests (`-tags integration`: `pkg/auth`, `pkg/db`, `pkg/server`) against local PostgreSQL 16 | Pass (milestone 36, local) |
| Both images built locally with the release version; `/health` returned `0.36.0` | Pass (milestone 36, local) |
| Shared HTTP contract (10 files) | Pass on Go (`TestContract`) and Java (`ContractTest`) |
| Latest recorded CI | Run 42370422148 on acc8b8c: `java-service`, `go-service`, `web`, `python-sdk` all green. **Milestone 36 has not run in CI yet**, and the "Publish service images" workflow has not run |
| Sign-up, approval and sign-in in a browser against the live service | **Not verified** |
| Real Ollama, Claude, Novu, SNS, OpenID provider, MCP client | **Not verified.** Fakes and replay tests only |

Coverage is below 80 percent locally in `db`, `event`, `feedback`, `inbox`, `mail`, `delivery`, `rules` and `auth` (mostly PostgreSQL code that only the integration tests reach). It is not scheduled as its own milestone; new code lands at 80 percent or more.

## 5. Open questions for the owner
1. **Two dispatchers on one database.** Keep "configure alike", or add a setting to turn a service's dispatcher off?
2. **Default budget.** `ATTENTION_BUDGET_PER_HOUR=10` silently quiets busy workspaces after upgrade. Keep it, or default to 0 and announce it?
3. **Java retention.** Port event purging (Go has it), or leave it Go-only?
4. **Milestone 37 scope** (Attention Intelligence, feedback, learning in Java) needs its AGENTS.md plan and approval. See [roadmap.md](roadmap.md).
5. **Per-person delivery.** Personal digests are stored and readable but not delivered, because jobs hold no address. Resolve recipients at send time as its own milestone?
6. **Inbound rate limiting (F1, high).** Add per-key limits on `/webhooks/github`, `/api/events` and `/notify` in middleware, at the edge, or both? Which limits?
7. **Double send of a critical event (F2, high).** Reserve the event id before delivering (insert as pending, deliver, mark sent) so concurrent redeliveries cannot both send? This changes the critical path, so it needs your approval and a test that proves the invariant still holds.
8. **Critical delivery inside the request (F3).** Keep it (a failure makes GitHub retry) or accept a bounded wait? Today the slowest channel sets webhook latency (10 s provider timeout, 40 s server write timeout).
9. ~~**Java accounts (F8).**~~ Built in milestone 36 (no single sign-on, no CORS).
10. **Read-path window (F10).** Is a 1000-event window acceptable for now, or should inbox, digests and learning query PostgreSQL with keyset pagination and indexes (a change to Go read paths, no schema change expected)?
11. **Multi-tenancy.** Sign-up approval is the only gate on a single workspace. Is real separation (tenants) planned? It decides whether sign-up can ever be open.
12. **Email verification and password reset** are not built (no email provider is configured on the live service). Build them with SMTP or Novu, or keep admin-approved accounts and admin-set passwords?
13. **Trusted proxy (F11).** Behind Render every person shares one address for the per-address limits. Add a trusted-proxy setting that reads `X-Forwarded-For`?
14. **Versioning policy.** `VERSION` is `0.36.0` (minor follows the milestone before 1.0). Keep that, or start at `1.0.0` for the first hosted release?

## 6. Audit findings (2026-11-07)
From the reverse-engineering report in [feature-flows.md](feature-flows.md) section 6. They were read from the code, not reproduced; nothing is fixed yet. "Tracked in" says where the work is recorded; an item is closed only when a milestone changes the code and a test proves it.

| ID | Severity | Finding | Tracked in |
|---|---|---|---|
| F1 | High | No inbound rate limiting on ingestion endpoints (limiters exist only for sign-in, channel tests, model calls, SMS) | Open question 6; [roadmap.md](roadmap.md) "Hardening backlog" |
| F2 | High | Check-then-act on duplicate deliveries: two concurrent deliveries of one critical event can both send (`pkg/pipeline/pipeline.go`, `Exists` then `Save`) | Open question 7; roadmap "Hardening backlog" |
| F3 | Medium | Critical delivery runs inside the webhook request; latency equals the slowest channel | Open question 8 |
| F4 | Medium | Workers poll PostgreSQL, so deferred and retried work waits up to one interval; NATS unused | roadmap "Queue and scale" |
| F5 | Medium | Two dispatchers on one database can dead-letter a job for a missing channel | Open question 1; section 3 |
| F6 | Medium | Java SSRF check is pre-flight, not dial-time, so a changing DNS answer is not covered | Section 3 (decision in force); roadmap "Hardening backlog" |
| F7 | Medium | Dedup, budget and classifier fail open by design: no alert is hidden, duplicates or extra interruptions are possible | Accepted behaviour; documented in [architecture.md](architecture.md) |
| F8 | Medium | Java has no accounts; the API key is its only gate | Open question 9; milestone 36 in roadmap |
| F9 | Medium | Ollama, Claude, Novu, SNS, OpenID Connect, a real MCP client and the Java SMTP success path have only run against fakes | Section 4; roadmap "Verify against real services" |
| F10 | Medium | Inbox, digests and learning read the newest 1000 events (`event.List`, `DefaultListLimit`) and all notification states into memory (`pkg/inbox/service.go load`, `pkg/attention/digest.go`, `pkg/learning/service.go`): older events are invisible to them and cost grows per request | Open question 10; [roadmap.md](roadmap.md) "Hardening backlog" |
| F11 | Medium | Behind a proxy (Render) every person shares one address, so the per-address sign-up limit (20 per hour) and the per-address sign-in lock are shared by everyone (`clientIP` in `pkg/server/auth_handlers.go`, `clientIp` in `AuthApi.java`) | Open question 13; roadmap "Hardening backlog" |
| F12 | Medium | Sessions need the console and the API on one site, or `AUTH_COOKIE_SAMESITE=none`; browsers that block third-party cookies still refuse it. The console now explains a blocked cookie | Milestone 36 risks; roadmap |
| F13 | Medium | An approved member sees every event: one workspace, no tenancy | Open question 11; roadmap "Product" |
| F14 | Medium | CI did not run on `master`, the default branch (it ran on `main`, `feature/**` and pull requests into `main` only), so merges and pull requests into `master` were unchecked. Fixed in milestone 36: `ci.yml` now also runs on `master` and on pull requests into it; takes effect once merged | Fixed in the milestone 36 branch (unverified until it runs) |
| L1 | Low | Local secrets are hardcoded in `docker-compose.yml` (`local-dev-key`, webhook secret, SMTP password), local use only | Roadmap "Hardening backlog" (secret management) |
| L2 | Low | Not audited: every list query for an unbounded `SELECT` (list endpoints take `limit` 1 to 500 and cursors) | Roadmap "Hardening backlog" |
| L3 | Low | Not profiled: inbox and analytics queries for N+1 patterns; none seen in the pipeline path | Roadmap "Hardening backlog" |
| L4 | Low | Not inspected: query plans of heavy queries (14 indexes exist, lookup indexes are test-guarded) | Roadmap "Hardening backlog" |
| L5 | Low | Not searched: every discarded error; known non-critical queue and publish failures are logged and ignored on purpose | Roadmap "Hardening backlog" |

## 7. Owner decisions (dated)
- **2026-11-07, SDKs for other languages:** document only, do not build. Proposal and open questions in [roadmap.md](roadmap.md).
- **2026-11-07, milestone 35:** approved and built (`/notify`, rules API, durable queue, Redis dedup, contract files 07 to 09). Milestone 36 is next and still needs a plan.
- **2026-11-07, after milestone 34:** push and read the Java CI job before milestone 35 (done, green); rename `example` to `agentpulse` (done); milestone 35 scope approved.
- **2026-11-07, Java parity:** implement the Java service like the Go service, milestone by milestone, with the same guarantees, API, tests and documentation, and a shared contract extended each time. Branch `feature/java-parity`, Maven built through Docker.
- **2026-11-07, repository secret:** `API_KEY` was added to the GitHub repository secrets so automation can use it; documented, with where every secret lives and how to rotate them, in [deployment.md](deployment.md#secrets-and-variables). No workflow uses it yet.
- **2026-11-07, sign-up and sign-in (milestone 36):** the console starts at sign-in, the API key is "Advanced", people can ask for an account and an admin approves it (default off, nothing to see until approved), Java gets the same accounts, and both services are published as versioned images starting at `0.36.0`. Approved in conversation; built on `feature/signup-and-sign-in`.
- **2026-11-07, documentation cleanup:** keep a few focused, detailed current documents; move superseded ones to [archived/](archived/README.md).

## 8. Keeping this honest
Update sections 2 to 4 with the work. Add questions to section 5 and move them to section 3 or 7 when answered. Log every new finding in section 6 with an ID and where it is tracked. Superseded notes go to the archive, never deleted.
