# Upcoming milestones

Planned work after milestone 26, taken from the "known limits" of milestones 22 to 26 and from the original plan. Each is a proposal: per [AGENTS.md](../AGENTS.md), a milestone gets a full plan (goals, decisions, database, APIs, folders, risks, tests, acceptance) and an approval before any code is written, and the work stops after each one.

## Milestone 27: Verify against real services
**Why:** Every adapter so far is tested against fakes. Real behaviour is unproven.
- Run the Ollama and Claude adapters against live endpoints; record fallback rate and latency in `milestone-27.md`.
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

## Provider abstraction follow-ups
Courier, Knock and custom delivery providers behind `NotificationProvider` stay on the list; they are scheduled by demand rather than numbered.
