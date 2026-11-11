# Milestone 38: Learning

Status: **in progress.** Slice 38a is built and awaiting review; 38b and 38c are not started. Plan, with the owner's six decisions: [milestone-38-plan.md](milestone-38-plan.md). The plan's guardrails apply to every slice: offline or async only, critical events untouchable, explicit overrides outrank anything learned, and nothing changes without an admin.

## Slice 38a: Java parity for the suggestions engine (built)

### Goals (met)
- Java makes the same suggestions as Go from the same feedback, and applying one changes the same rule or override.
- A shared contract file proves it; it was written and run against today's Go before any Java.

### Decisions
- A port of Go's `pkg/learning`: kinds adaptive, demote, promote and override; the same evidence rules; the same wording; the same suggestion id (`sha256` of kind, source, event type, repository and rule name joined with NUL, first 8 bytes as hex), checked against Go's values in a test, so two services on one database never make the same suggestion twice.
- Small interfaces (`FeedbackReader`, `RuleEditor`, `OverrideWriter`, `Repository`) so each part is testable alone.
- Guardrail enforced twice: when a suggestion is made and again when it is applied.
- No Go change and no database change: the Go-owned `learning_suggestions` table and its JSON are reused.

### APIs
`GET /api/learning/suggestions` (any signed-in person; refreshes and lists pending), `POST /api/learning/suggestions/{id}/apply` and `/dismiss` (admin, audited as `learning.apply` and `learning.dismiss`).

### Folder changes
Java: `Learning`, `LearningStores`, `LearningApi`; wiring in `HttpApi`, `Main`, `Config` (`LEARNING_MIN_SIGNALS`, `LEARNING_LOOKBACK`). Contract: `docs/contract/14-learning-suggestions.json`.

### Risks and how they were handled
- Drift from Go: contract file 14 pins kind, wording, evidence, rule change and the final states; the id hash is tested against Go's values.
- Critical leakage: feedback about critical events is ignored, locked, critical and disabled rules are never suggested for, apply re-checks, and an invariant test tries a crafted suggestion against a locked critical rule.
- Known limits, unchanged from Go: the analysis runs inside the `GET` request and reads only the newest events and feedback (finding F10, milestone 42). Slice 38b adds the offline run.

### Tests
- Contract: file 14 passes in Go (`TestContract`) and Java (`ContractTest`).
- Java: `LearningTest` (thresholds, every kind, guardrails, apply and dismiss, not made again, cutoff after apply, refresh keeps creation time, lookback, JSON shape, id hash), `LearningApiTest` (member refused, admin applies and is audited, CSRF, paths and methods), `PostgresLearningStoresTest` (including a row written the way Go writes it), one more invariant test (learning never learns from critical events and never changes a locked critical rule).
- Totals: 309 tests with PostgreSQL and Redis, none skipped; 286 offline (5 skipped), 91 percent of lines.
- Real stack (PostgreSQL, the Go binary and the Java jar on one database): five dismissals given through Java produced one suggestion; Go listed the same id and did not add a second; Go applied it; Java then had nothing pending; the rule showed as background in Go.

### Acceptance criteria
- [x] Java returns the same suggestions as Go for the same feedback, and applying one changes the same rule or override.
- [x] The guardrails hold: no critical feedback is learned from, no critical, locked or disabled rule is changed.
- [x] Contract file 14 passes in Go and Java; `mvn verify` passes offline and with PostgreSQL and Redis.
- [ ] CI green on the branch (not yet seen).

### Review checklist
- Read `Learning.Service.analyze`, `suggest`, `changeRule` and `Learning.changeable`.
- Run the contract files against both services and the invariant tests.
- Check `LearningApi` for who may do what.

## Slice 38b: score tuning in Go (not started)
## Slice 38c: score tuning in Java (not started)
