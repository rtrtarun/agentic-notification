# Roadmap

What is **not** built yet, as one numbered sequence of milestones. What exists is in [implementation-sync.md](implementation-sync.md); the records of finished milestones are in [milestones/](milestones/README.md).

## Rules for every milestone
- **Plan first.** Before any code, write the AGENTS.md plan: goals, architecture decisions, database changes, APIs, folder changes, risks, tests and acceptance criteria. **Owner approval is required before any code is written.**
- **Hard stop.** Implement, test, document and commit, then **stop**. Never continue to the next milestone automatically; wait for review.
- **Both services.** Each feature that exists in Go is built in Java too, with a shared contract file in [contract/](contract/README.md) written and run against Go first. A milestone that is Go-only or Java-only says so in its plan.
- **The sacred invariant.** Critical events never touch AI, merging, muting, delay, budget, the queue or chosen channels. Any change near the critical path needs a test proving this still holds.
- Order is a proposal: the owner may reorder or split milestones when approving a plan.

## Status
| Milestone | State |
|---|---|
| 34 to 36 | Done and merged |
| 37 Java parity part 3 (rule fields, feedback and overrides, heuristic Attention Intelligence) | **Done and merged** (PR #14, 2026-11-10; [record](milestones/milestone-37.md)) |
| 38 Learning | **Plan approved 2026-11-10** ([plan](milestones/milestone-38-plan.md)); building slice 38a (Java suggestions parity) |
| 39 to 48 | Not started; no plan approved yet. Each needs its plan and owner approval first |

`DISPATCHER_ENABLED` (a setting in both services that turns a process's queue dispatcher off, so one service can own the queue when two share a database) is also merged (PR #13).

## Milestone 38: Learning
Feedback loops and dynamic model training. Layer 2 only: learn from the stored feedback signals (opened, dismissed, undo, always, never, replies) to suggest and refine preferences, overrides and scores.
- Learning is an **offline or async process** that learns from stored feedback to adjust heuristic scores and routing preferences. It is not a black-box model sitting in the live path and it never intercepts live traffic.
- Learning engine and suggestions API in both services; suggestions are visible and reviewable, never silent.
- Guardrails: learning only moves non-critical events between standard and background, never touches critical events, and a person's explicit override always outranks it.
- Reads of feedback must not depend on the 1000-event window (see milestone 42).

## Milestone 39: Observability and MCP
Traces, an OpenTelemetry exporter and MCP integration.
- Traces and agent event chaining (`traceId`, `parentEventId`) in Java; an OpenTelemetry exporter for the spans already recorded.
- MCP server in Java, matching Go.
- Verify against a real MCP client.

## Milestone 40: Delivery channels
SMS, Novu and further providers behind `NotificationProvider`.
- SMS (with the daily cap and no caller-chosen numbers) and the `NovuProvider` in Java; the channels API.
- Custom providers such as Courier and Knock, by demand, behind the same abstraction; never tightly coupled to one vendor.
- Critical events still go to every configured channel, inline.

## Milestone 41: Metrics
System health and usage metrics.
- `/metrics` and the analytics endpoints in Java, matching Go; counters for decisions per class, queue depth, dead letters, budget use and digest counts.
- Documented names and an example dashboard.

## Milestone 42: Inbox and read-path optimization
Inbox features, retention policies and finding F10.
- Inbox in Java, and event retention (purging old events) in Java as well as Go.
- **F10:** replace `inbox.Service.load` and the digest and learning reads of `event.List` with SQL filtering, sorting and keyset paging; counters for analytics.
- Acceptance: the inbox shows events older than the newest 1000, results match the current behaviour for small data, and response time stays flat as events grow.

## Milestone 43: SDK foundation
TypeScript/JavaScript and Go SDKs, and a standard `/api/events` contract. Today only Python has an SDK ([../sdk/python](../sdk/python/README.md)).
- An SDK publishes agent steps to `POST /api/events` with `traceId` and `parentEventId` chaining, times tool calls, records retries, tokens and cost, never breaks the agent by default, and never decides severity (an agent's own severity is data; the rules set the class). Standard library or built-in HTTP client only; tested against a fake server and the real service binary; one CI job per SDK.
- A language-neutral SDK test contract, like `docs/contract`.
- Open questions to settle in the plan: hand-written or generated from an OpenAPI description (none exists yet); whether `/notify` is covered or only agent traces. Java, .NET and Ruby SDKs and publishing (npm, Maven Central, pkg.go.dev, NuGet) come later, by demand.

## Milestone 44: Security and edge hardening
- **F1** inbound rate limiting: per-key limits on `/webhooks/github`, `/api/events` and `/notify`, 429 when exceeded, the same in Go and Java, a new contract file. A flood is limited; critical events from a key within its limit are unaffected.
- **F11** trusted proxy: a `TRUSTED_PROXY` setting that reads `X-Forwarded-For` only from the proxy, in both services, so per-address limits are per person and a spoofed header from outside is ignored.
- **F12** sessions across sites: host the console and the API on one site (custom domain) so sign-in works without `AUTH_COOKIE_SAMESITE=none`.
- **F6** Java SSRF: check the address at connect time, or pin the resolved address, as Go does; a host whose DNS changes between check and connect is refused.
- **L1** secrets: secret management for deployments, local defaults kept clearly local; the deployment guide names where each secret lives.

## Milestone 45: Core resilience and audit
- **F2** double send of a critical event: reserve the event id before delivering (pending, deliver, mark sent), keeping delivery inline and dedup never merging critical events. Concurrent redelivery sends once; a failed send still lets the sender retry; the invariant tests still pass.
- **L2 to L5** audit gaps: audit list queries for unbounded selects, profile inbox and analytics queries, inspect heavy query plans, search discarded errors. A written results table; fixes as small changes.
- Housekeeping: raise packages below 80 percent coverage; reconcile the two `k8s/` directories.

## Milestone 46: Identity and console
- Account lifecycle: email verification and password reset (needs SMTP or Novu configured), single sign-on in Java.
- Per-person delivery: resolve a person's address at send time without storing it in jobs; personal digests, personal schedules and budgets.
- Console: screens for rule predicates, per-rule channels, channels, personal preferences and "my digest"; translations for the newer screens; component tests.

## Milestone 47: Queue and scale
- NATS (named in the stack, unused today) if polling stops being enough.
- Share the circuit breaker across replicas.
- Acceptance: several replicas of Go and Java on one database and one broker behave as one service, and the invariant tests still pass.

## Milestone 48: Production and product
- Verify against real services: PostgreSQL, Redis, Ollama, Claude, Novu, SNS, an OpenID provider and a real MCP client; record the results and fix what they find.
- Production hardening: AWS deployment, backup and restore drills, SLO dashboard, security review, load tests (the original milestone 10).
- Product: multi-tenancy and billing for the hosted offering; event replay and an agent registry.

## Where each earlier backlog item went
| Earlier item | Milestone |
|---|---|
| Learning, traces, MCP, SMS and Novu, metrics, inbox and retention | 38, 39, 40, 41, 42 |
| SDKs for other languages | 43 |
| F1, F11, F12, F6, L1 | 44 |
| F2, L2 to L5, housekeeping | 45 |
| F10 read-path window | 42 |
| Per-person delivery, console completeness, account lifecycle | 46 |
| Queue and scale, observability exporter | 47, 39 |
| Providers (Courier, Knock, custom) | 40 |
| Verify against real services, production hardening, product | 48 |
