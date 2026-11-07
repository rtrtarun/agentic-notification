# Roadmap

What is **not** built yet, in the order it is likely to happen. Every item needs its AGENTS.md plan (goals, decisions, database, APIs, folders, risks, tests, acceptance) and owner approval before code, and work stops after each milestone. What exists is in [implementation-sync.md](implementation-sync.md).

## Next: milestone 36, Java parity part 3 (approved in outline, no plan yet)
Accounts and sign-in, Attention Intelligence (heuristic classifier, digests, explanations, budgets) and feedback in the Java service, with new shared contract files. Rule `payloadMatch` and per-rule `channels` come with it or just before, because Java refuses them today. After that: learning, traces and MCP, SMS and Novu, metrics, inbox and retention, in the owner's order.

## Proposed, not started: SDKs for other languages
Recorded 2026-11-07 as a documented feature only. Today only Python has an SDK ([../sdk/python](../sdk/python/README.md)).

An SDK would publish agent steps to `POST /api/events` with `traceId` and `parentEventId` chaining, time tool calls, record retries, tokens and cost, never break the agent by default, and never decide severity (an agent's own severity is data; the rules set the class). It would use the standard library or built-in HTTP client only, be tested against a fake server and the real service binary, and get one CI job per SDK.

Suggested order (the owner chooses): TypeScript/JavaScript, Go, Java, then .NET and Ruby by demand.

Open questions:
1. Hand-written, or generated from an OpenAPI description (none exists yet)?
2. Which language first, and does it cover `/notify` or only agent traces?
3. A language-neutral SDK test contract, like `docs/contract`?
4. Versioning and publishing (npm, Maven Central, pkg.go.dev, NuGet): out of scope until an SDK exists.

## Hardening backlog (from the 2026-11-07 audit, findings in [implementation-sync.md](implementation-sync.md) section 6)
Not scheduled; each item needs a plan and your approval, and any change to the critical path needs a test proving the invariant still holds.

| Finding | Work | Acceptance |
|---|---|---|
| F1 inbound rate limiting | Per-key limits on `/webhooks/github`, `/api/events`, `/notify` (middleware or edge), 429 when exceeded, same behaviour in Go and Java, new contract file | A flood is limited; critical events from a key within its limit are unaffected; limits documented |
| F2 double send of a critical event | Reserve the event id before delivering (pending, deliver, mark sent), keeping delivery inline and dedup never merging critical events | Concurrent redelivery of one critical event sends once; a failed send still lets the sender retry; the invariant tests still pass |
| F6 Java SSRF | Check the address at connect time, or pin the resolved address, as Go does | A host whose DNS changes between check and connect is refused |
| L1 secrets | Secret management for deployments; keep local defaults clearly local | Deployment guide names where each secret lives |
| L2 to L5 audit gaps | Audit list queries for unbounded selects, profile inbox and analytics queries, inspect heavy query plans, search discarded errors | A written results table; fixes as small changes |

## Platform follow-ups (unscheduled)
| Item | Goal |
|---|---|
| Verify against real services | Run PostgreSQL, Redis, Ollama, Claude, Novu, SNS, an OpenID provider and a real MCP client against the code; record results; fix what they find |
| Per-person delivery | Resolve a person's address at send time without storing it in jobs; deliver personal digests; per-person schedules and budgets |
| Console completeness | Screens for predicates, per-rule channels, personal preferences and "my digest"; translations for the newer screens; component tests |
| Queue and scale | NATS (named in the stack, unused today) if polling stops being enough; share the circuit breaker across replicas |
| Observability | An OpenTelemetry exporter for the spans already recorded |
| Providers | Courier, Knock and custom providers behind `NotificationProvider`, by demand |
| Product | Event replay, agent registry, multi-tenancy and billing for the hosted offering |
| Production hardening | AWS deployment, backup and restore drills, secret management, edge rate limiting, SLO dashboard, security review, load tests (original milestone 10) |
| Housekeeping | Reconcile the two `k8s/` directories; raise packages below 80 percent coverage |
