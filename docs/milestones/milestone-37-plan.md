# Milestone 37 plan: Java parity part 3 (rule fields, feedback, attention)

Status: **approved and built** (all three slices; digests are stored, not delivered). Record: [milestone-37.md](milestone-37.md). Branch: `feature/m37-attention-feedback`.

Follows [milestone 36](milestone-36.md) (Java accounts, sessions, sign-up). Goal: the Java service gains the next slice of the Go service's behaviour, proved by shared contract files.

## 1. Goals
- Rule `payloadMatch` (predicates) and per-rule `channels` work in Java, so Java stops refusing rules Go accepts.
- Feedback (Layer 4) in Java: record opened, dismissed, undo, always notify, never notify, replies; `always` and `never` create overrides.
- Heuristic Attention Intelligence in Java: deterministic classifier (standard vs background), explanations, digests, and the attention budget (a rolling cap on standard interruptions; over-budget standard events are held for the next digest, never dropped). No model, no LLM.
- Three new shared contract files that Go and Java both pass.
- The critical path is not touched: a critical event never reaches feedback-driven overrides that suppress it, never reaches the classifier, never joins a digest.

## 2. Architecture decisions
- Same stack as 34 to 36: plain JDK, Jackson, HikariCP. No Spring.
- Port, do not redesign. Each Java class mirrors its Go package: `policy/predicate.go` to `Predicates`, `feedback` to `Feedback`, `overrides` to `Overrides`, `attention/heuristic.go` and `digest.go` to `Heuristic` and `Digests`.
- **Contract first.** For each slice the shared contract file and the Go fixtures are written and run against Go before the Java code, so edge cases (type coercion, missing fields) are fixed by the contract, not by the port.
- Steps are ordered so each is runnable and reviewable alone:
  1. **37a** `payloadMatch` and `channels` (Rules API and policy engine). Shared fixtures from `pkg/policy/predicate_test.go`.
  2. **37b** Overrides store and API, then Feedback service and API. Needs the signed-in person from milestone 36.
  3. **37c** Heuristic classifier, explanations, budget, digests and the digest scheduler. The classifier and budget run in the event pipeline (standard and background events only), never on the critical path and not as a step inside `/notify`'s critical delivery.
- A `Guarded` wrapper (same as Go) refuses critical events and any class other than standard or background, so the guarantee is enforced in code, not by convention.
- Heuristic scoring must give the same score and reasons as Go for the same event. Shared fixtures prove it.
- `payloadMatch` uses Go's own rule: a dotted path into the payload plus `branch`, `author` and `severity` aliases, and the operators eq, ne, gt, gte, lt, lte, exists, max 5 predicates. No JSONPath library: a different path language would make Java match differently from Go.
- Budget state uses Redis (rolling window, same keys as Go) with an in-memory fallback, as Go does; it has no table. If Redis is down the budget fails open for standard events.
- Unsupported things stay refused, not ignored (widening a rule is unsafe). Model-backed classification, learning, traces and MCP stay out of scope.

## 3. Database changes
None. Java uses the Go-owned schema (`feedback`, `overrides`, `digests`, `attention` tables, the rules table's predicate and channel columns). Java never migrates. I will confirm the exact columns before writing the stores.

## 4. APIs (to be confirmed against the Go routes before contract files are written)
- 37a: `/api/rules` accepts and returns `payloadMatch` and `channels`; `/api/policies` unchanged.
- 37b: `POST /feedback` (the Go route; actions opened, dismissed, undo, always, never, reply, with scope type, repository or source; not a separate positive/negative signal), `GET /api/overrides`, `DELETE /api/overrides/{id}`.
- 37c: `GET /api/digests`, `GET /api/explain/{eventId}`, `GET /api/attention/status`.

## 5. Folder changes
- Java: new classes under `application`, `domain`, `infrastructure`, `interfaces` in `java-server/notification-service/src`, and tests beside them.
- `docs/contract/11-rule-predicates.json`, `12-feedback.json`, `13-attention.json`; `docs/contract/README.md` updated.
- Docs: `java-service.md`, `CHANGELOG.md`, `roadmap.md`, `implementation-sync.md`, a `milestone-37.md` record, package notes.

## 6. Risks
- **Rule widening.** A predicate port that matches more than Go would deliver or suppress the wrong events. Mitigation: shared fixtures; unknown operators or fields are rejected.
- **Feedback suppressing a critical event.** `never` on a critical event must be refused, as in Go. A new invariant test covers it.
- **Classifier drift.** Heuristic scores differing between services. Mitigation: shared score fixtures.
- **Two services, one database.** Duplicate digests if both run the scheduler. Mitigation: reuse the `DISPATCHER_ENABLED` idea (a scheduler setting), documented.
- **A budget that drops events.** It must hold, never drop, and never apply to critical events. Invariant tests cover both.
- **Size.** Three slices in one milestone. Mitigation: commit and verify 37a, 37b and 37c separately; stop and ask if any slice runs long.

## 7. Tests
- Unit tests for each new class; predicates tested against Go's fixtures.
- Invariant tests added to the existing set (now 9): a critical event is never classified, never digested, never suppressed by an override or feedback; `never` on critical is refused.
- Contract files 11 to 13 replayed by Go `TestContract` and Java `ContractTest`.
- Postgres tests for the new stores (with the existing CI database job).
- Offline `mvn verify` keeps the coverage gate; Go tests untouched and still pass.

## 8. Acceptance criteria
- A rule with `payloadMatch` and `channels` created through Java matches the same events as in Go.
- Feedback recorded through Java appears in Go and the reverse; `always` and `never` create overrides the other service honours.
- The same event gets the same heuristic class, score and reason in both services; digests contain no critical events.
- All contract files pass in Go and Java; `mvn verify` and the Go suite pass; CI green; no critical-path regression.

## Questions for you
1. Approve all three slices (37a to 37c) as one milestone, or stop after 37a and 37b?
2. Should digest delivery (sending the digest through the outbox) be in 37c, or only generating and storing digests?
