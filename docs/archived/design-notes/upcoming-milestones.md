> **Archived 2026-11-07.** Historical; it does not describe the current system. Current documents: [docs/README.md](../../README.md).

# Upcoming milestones

> **Historical.** This was the first proposal for milestones 27 onward. Milestones 27 to 33 were built as `PROMPT_SPEC.md` defined them (they differ from the list below); see [implementation-sync.md](../../implementation-sync.md) for what was built and what is still open.

Planned work after milestone 26, taken from the "known limits" of milestones 22 to 26 and from the original plan. Each is a proposal: per [AGENTS.md](../../../AGENTS.md), a milestone gets a full plan (goals, decisions, database, APIs, folders, risks, tests, acceptance) and an approval before any code is written, and the work stops after each one.

## Milestone 27: Verify against real services
**Why:** Every adapter so far is tested against fakes. Real behaviour is unproven.
- Run the Ollama and Claude adapters against live endpoints; record fallback rate and latency in `milestones/milestone-27.md`.
- Run the PostgreSQL stores (digests, feedback, overrides, delivery jobs) against a real database in CI via Docker Compose.
- Run SNS and Entra ID sign-in once end to end.
- Acceptance: a CI job that starts Compose and passes integration tests; a written results table.

## Milestone 28: Learning engine (original milestone 9)
**Why:** Feedback (opened, dismissed, undo, always, never, replies) is stored since milestone 24 but nothing learns from it.
- A `pkg/learning` package (the name freed by the `pkg/ollama` rename) that turns feedback into per-source and per-type adjustments to the heuristic's score, with every adjustment visible and reversible.
- Learning only ever moves events between standard and background; it never touches critical events and never overrides a person's always/never choice.
- Optional model-assisted summaries of feedback through the existing `Completer`.
- Acceptance: a measurable change in held-versus-sent for repeated dismissals in a test, a "why did this change" explanation, and a reset button.

## Milestone 29: Asynchronous attention
**Why:** Classification runs inside event handling, bounded only by a timeout; the breaker and rate cap are per process.
- Classify through NATS (the platform's messaging layer) so a slow model never delays ingestion.
- Share the rate cap and breaker state across replicas through Redis.
- Acceptance: ingestion latency unchanged with the model down; two replicas respect one cap.

## Milestone 30: Per-person digests and time-of-day schedules
**Why:** Digests are workspace-wide and run on an interval after start, not at a set time.
- Per-person delivery targets and preferences; digest at a chosen local time per person.
- Acceptance: two people receive different digests at different times; critical events still never appear in one.

## Milestone 31: Production hardening (original milestone 10)
- Deployment on AWS, backups and restore drills, secret management, rate limiting at the edge, an SLO dashboard and alerts, a security review, load tests.
- Acceptance: a documented deploy from scratch, a restore test, and a load test result.

## Milestone 34: Java foundation (built)
The Java service now has the deterministic core and a shared contract with the Go service; see [milestones/milestone-34.md](../../milestones/milestone-34.md). Milestones 35 and later bring the rest of the Go service to Java in the order the project owner chooses (suggested: `/notify`, rules and policies API, durable delivery, Redis dedup, accounts, Attention Intelligence, feedback, learning, traces and MCP).

## Milestone 35: Java parity part 2 (built)
`/notify`, the rules API, the durable delivery queue and Redis deduplication in Java, with three more contract files; see [milestones/milestone-35.md](../../milestones/milestone-35.md). Milestone 36 (approved in outline): accounts and sign-in, Attention Intelligence and feedback in Java.

## Proposed, not started: SDKs for other languages
**Why:** Agents are written in many languages; today only Python has an SDK ([sdk/python](../../../sdk/python), milestone 33). Everyone else must hand-write HTTP calls and the trace chaining.
**Recorded 2026-11-07 as a documented future feature only. Nothing is built and no language order is chosen.** Like every milestone it needs its AGENTS.md plan and an approval before code.

What an SDK would be, taken from what the Python one does:
- Publish agent steps to `POST /api/events` with the right `traceId` and `parentEventId` chaining, time tool calls, and record retries, tokens and cost.
- Never break the agent by default: a failed publish is swallowed or reported, not thrown, unless the caller asks.
- Never decide anything: an agent's own severity is data only; the service's rules set the attention class, and critical events keep their guarantees however the event arrives.
- Standard library or the language's built-in HTTP client only, no runtime dependencies where the language allows it; small enough to read in one sitting.
- Tested against a fake server and against the real service binary, with a CI job per SDK, as the Python SDK is.

Candidate languages, in a suggested order (the owner chooses): **TypeScript/JavaScript** (most agent frameworks, Node and the browser), **Go** (the platform's own language, easy to dogfood), **Java** (follows the Java service, milestones 34 onward), then **.NET** and **Ruby** by demand.

Open questions for the design side:
1. Hand-written SDKs, as the Python one is, or generated from an OpenAPI description of `/api/events`, `/notify` and the traces API? Generating needs that description to exist first; none does yet.
2. Which language first, and does the first one also cover `/notify` (generic notifications) or only agent traces?
3. Shared SDK contract: a language-neutral test file set (like `docs/contract`) that every SDK must pass against a fake server, so the SDKs behave the same?
4. Versioning and publishing (npm, Maven Central, pkg.go.dev, NuGet): out of scope until an SDK exists.

## Provider abstraction follow-ups
Courier, Knock and custom delivery providers behind `NotificationProvider` stay on the list; they are scheduled by demand rather than numbered.
