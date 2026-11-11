# Milestone 38: Learning

Status: **all three slices built; awaiting the owner's review.** Slice 38a is merged (PR #17, CI green); 38b (Go) and 38c (Java) are built and reviewed together in one pull request, as two commits. Plan, with the owner's six decisions: [milestone-38-plan.md](milestone-38-plan.md). The plan's guardrails apply to every slice: offline or async only, critical events untouchable, explicit overrides outrank anything learned, and nothing changes without an admin.

## Slice 38a: Java parity for the suggestions engine (merged, PR #17)

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
- [x] CI green on the branch (all four jobs, PR #17).

### Review checklist
- Read `Learning.Service.analyze`, `suggest`, `changeRule` and `Learning.changeable`.
- Run the contract files against both services and the invariant tests.
- Check `LearningApi` for who may do what.

## Slice 38b: score tuning in Go (built)

### Goals (met)
- A background run turns stored feedback into small, bounded, explainable **score adjustments** for the built-in heuristic; an admin approves, rejects or retires each one; approved ones change how later non-critical events are scored.
- Go is the reference: the contract file `15-learning-adjustments.json` was written against this code.

### Decisions
- **The rule** (the owner's numbers): per source and event type, count signals in a 30-day lookback about **delivered** (queued or sent), non-critical events; weight by age with a 14-day half-life (rounded to six decimals so two implementations agree); cap each person's influence at 3 weighted signals; `offset = round(20 x (P - N) / (P + N + 2))`, clamped to +-20. Needs 5 signals from 2 people. Positive: opened, reply, always. Negative: dismissed, never. Undo is ignored.
- **Nothing is active until approved.** A run only ever writes `proposed` rows; the only code that sets `active` is `Approve`, and a test asserts runs never activate anything. Rejected is final; a retired scope can only come back as a fresh proposal from feedback given after the retirement; an active or rejected scope is never re-proposed.
- **Snapshot, not a query.** The heuristic reads `attention.Tuning`, an immutable map swapped in whole; refreshed on approval and retirement and every 30 seconds (so a change made by the other service arrives), and ignored once 5 minutes old. The live path does no I/O. Without a snapshot the score is exactly what it was (a test compares them).
- **Version on every tuned decision:** `heuristic-v1+tuned-N`, where N is the set version, which rises each time an approval or retirement changes the set. Each tuned reason reads `learned adjustment -14: 5 of 5 signals from 2 people were negative`.
- **Impact estimate** on every proposal: recent standard events of the scope replayed through the heuristic with and without it. It is a shadow calculation; nothing changes.
- **Runs:** `POST /api/learning/runs` records a run and returns 202 at once; the work continues in the background. `?wait=true` waits and returns 200 (used by the contract and by tools). One run at a time across both services: a partial unique index on `learning_runs (status) WHERE status = 'running'`; a run left running for 10 minutes is marked failed as abandoned. `LEARNING_TRAIN_EVERY` schedules runs (default off). An error is stored shortened and with any address-with-credentials word removed.
- **Scope:** per source and event type. The tables, store and snapshot already carry a repository so a later, narrower adjustment is possible, but this slice proposes only type-wide ones (a repository-specific one wins over a type-wide one in the snapshot; a test covers it).
- **Explicit overrides still win:** the pipeline never classifies an event an override covers, and the tuner neither reads nor writes overrides (it has no dependency on them).

### Database (Go owns migrations; additive, idempotent)
`learning_adjustments` (one row per scope, `scope_key` unique, evidence and impact as JSONB, `version`, resolution fields) and `learning_runs`. `learning_suggestions` is unchanged.

### APIs
`GET /api/learning/adjustments?status=` (any signed-in person; also returns the set `version`, `maxAdjustment`, `minSignals`, `minPeople`), admin `POST /api/learning/adjustments/{id}/approve|reject|retire` (audited as `learning.approve|reject|retire`), `GET /api/learning/runs` (latest 20 and the schedule), admin `POST /api/learning/runs[?wait=true]` (audited as `learning.run`). Errors: 404 unknown id or action, 409 not proposed, not active or a run already running, 403 a guardrail refusal, 400 an unknown status. No change to `/events`, `/notify`, `/webhooks/github` or any critical path.

### Folder changes
Go: `pkg/attention/tuning.go`, `heuristic.go`, `attention.go` (`SourceOf` knows the tuned name); `pkg/learning/tuning*.go`; `pkg/server/tuning_handlers.go` and wiring; `pkg/config` (four settings); `pkg/db/migrate.go`. Contract: `docs/contract/15-learning-adjustments.json`, with `LEARNING_MIN_PEOPLE=1` for the contract run.

### Risks and how they were handled
- **Feedback loop:** only delivered events count, offsets are bounded, a person's influence is capped, at least two people are needed, an admin approves each one, retiring restores the old score, and the impact estimate is shown first.
- **One person steering it:** the per-person cap and the distinct-people minimum (tests: 40 dismissals from one person propose nothing; a noisy person counts as three signals).
- **Critical leakage:** critical feedback is dropped at read; events decided by a locked or critical rule are dropped; a scope a critical or locked rule covers is never proposed, refused at approval, and left out of the snapshot; the heuristic and `Guarded` still refuse a critical event. Tests for each.
- **Two services, one database:** one running run across both, one row per scope, so a second run updates rather than duplicates.
- **Known limit, unchanged:** a run reads the newest events and 5000 feedback rows (finding F10, milestone 42); a run reports how many it read.
- **Contract file 15 and Java:** the 38b commit's Java contract test skipped this one file so that commit stayed green; the skip is removed in 38c.

### Tests
- `attention/tuning_test.go`: unchanged scores without a snapshot (nil, empty, unrelated), the score, reason and version with one, protection versus demotion, critical refused, repository beats type, clamping, a never-loaded or stale snapshot is ignored, nil safety.
- `learning/tuning_test.go`: the rule's numbers, decay, thresholds, one person, the per-person cap, delivered-only, critical feedback, locked rules, undo and unknown events, refresh without duplicates, determinism, nothing active before approval, approve, retire and the heuristic's score, rejected, active and retired scopes, the approval guardrail, snapshot exclusion, the impact estimate, asynchronous runs and the one-running-run rule, a failed run with a redacted error, an abandoned run, config bounds, the id hash, the schedule and refresher.
- `learning/tuning_repository_contract_test.go`: one contract for each store, run against memory always and PostgreSQL in the integration build (it passed against PostgreSQL 16).
- `server/tuning_handlers_test.go`: the whole path through HTTP with two people, members refused, audit, an override still wins, critical feedback teaches nothing, refusals.
- `server/contract_test.go` runs file 15; `db/migrate_test.go` and `config_test.go` cover the new tables and settings.
- Totals: `go test ./...` passes, `go vet` and `gofmt` are clean.

### Acceptance criteria
- [x] A run is asynchronous, returns at once, records what it read and made, and can be repeated without duplicating proposals.
- [x] An approved adjustment changes the heuristic's score and reason for matching non-critical events, and retiring restores the old score (Go).
- [x] Nothing is active before approval, and every approval, rejection, retirement and run is audited.
- [x] No critical event is counted, adjusted or classified; the invariant tests pass.
- [x] A person's explicit override still wins.
- [x] Contract file 15 passes in Go; the Go suite passes.
- [x] Java passes file 15 (slice 38c).

### Review checklist
- Read `Tuner.analyze`, `propose`, `offset`, `learnable`, `Approve`, `refresh` and `coveredByProtectedRule`.
- Read `attention.Tuning` and the few lines in `Heuristic.Classify`.
- Run `go test ./pkg/learning ./pkg/attention ./pkg/server` and the contract file.

## Slice 38c: score tuning in Java (built)

### Goals (met)
- Java proposes the same adjustments as Go from the same feedback, an admin approves them through the same API, and the heuristic scores the same way afterwards.
- The contract file `15-learning-adjustments.json`, written against Go in 38b, passes in Java; its skip is removed.

### Decisions
- A port, not a redesign: `Tuning` holds the rule, the records, the stores' interfaces, `Tuner` and `Snapshot`; `TuningStores` the memory and PostgreSQL stores; `TuningApi` the HTTP; `Heuristic.withTuning` the three lines that apply an offset.
- **Same numbers.** Floating point can disagree between languages, so both services now take the sums in a fixed order (by person) and round to six decimals, and round halves away from zero. The Go rule was changed in the 38c commit for this: Go's map order is random, so its sums could differ in the last digit from run to run. The weights use `StrictMath.pow`. A test pins the Go values (`-14`, `-13`, `-8`, `-16`, `0.997939`).
- **Same ids and rows.** The adjustment id is the same hash (`68abe35a59d2dd14` for `github` and `pull_request`), the `scope_key` is written the way Go writes it, and Java reads rows Go wrote (a test inserts one with plain SQL). So two services on one database never propose the same scope twice, and one service's approval reaches the other's snapshot within 30 seconds.
- **One running run across both services:** the same partial unique index and `INSERT ... ON CONFLICT DO NOTHING`; a second start gets 409.
- **Scheduling:** a background refresher every 30 seconds; a schedule only when `LEARNING_TRAIN_EVERY` is set (default off). Set it on one service only.
- **Not changed:** no Go behaviour other than the arithmetic order above, no schema change, no change to the live event path.

### APIs
The same as 38b: `GET /api/learning/adjustments?status=`, admin `POST .../{id}/approve|reject|retire`, `GET /api/learning/runs`, admin `POST /api/learning/runs[?wait=true]`, with the same statuses and messages.

### Folder changes
Java: `application/Tuning.java`, `infrastructure/TuningStores.java`, `interfaces/TuningApi.java`, `Heuristic`, `HttpApi`, `Main`, `Config` (four settings). Tests: `TuningTest`, `TuningApiTest`, `PostgresTuningStoresTest`, one more case in `SacredInvariantTest`, one in `InfrastructureTest`. Contract: the Java harness gives the digests the same ticking clock as the events, and uses `LEARNING_MIN_PEOPLE=1`, like the Go harness.

### Risks and how they were handled
- **Drift from Go:** contract file 15 (64 steps), pinned numbers and ids, and a real-stack check with both services on one database.
- **Floating point:** the fixed order, the rounding and the pinned values above.
- **Critical leakage:** the same guards and a new invariant test: critical feedback teaches nothing, a crafted proposal against a locked critical scope is refused and stays proposed, an active row for that scope is left out of the snapshot, and the heuristic and `GuardedClassifier` refuse a critical event even with an adjustment loaded.
- **Known limits, unchanged:** a run reads the newest 1000 events and 5000 feedback rows (finding F10, milestone 42); the overrides cache is per process.

### Tests
- `TuningTest` (33): the rule's numbers, rounding and decay, thresholds, one person, the cap, delivered-only, critical and locked, undo, refresh without duplicates, determinism, nothing active before approval, approve, reject and retire with the heuristic's score, rejected, active and retired scopes, the approval guardrail, the snapshot, the impact estimate, asynchronous runs, a failed run, an abandoned run, config bounds, the id hash, JSON shape, and the heuristic with and without a snapshot (stale, nil, empty, repository versus type, clamping, protection).
- `TuningApiTest` (7): the whole path with two people, who may do what, audit entries, an override still wins, critical feedback, refusals, the endpoints absent until turned on.
- `PostgresTuningStoresTest` (4): the store contract, a row written by Go, the scope key, and the one-running-run index.
- Contract: all 15 files pass in Java (`ContractTest`) and in Go (`TestContract`).
- Totals: Java `mvn verify` 356 tests with PostgreSQL and Redis, none skipped; offline 329 (5 skipped); 88.7 offline, 94.2 with PostgreSQL percent of lines. Go `go test ./...` passes.
- Real stack (PostgreSQL 16, Redis 7, the Go binary and the Java jar on one database): see the CHANGELOG entry.

### Acceptance criteria
- [x] Java returns the same proposals as Go for the same feedback and scores the same after approval.
- [x] A run is asynchronous, returns at once, records what it did, and a second cannot start while one runs, across both services.
- [x] Nothing is active before approval; every approval, rejection, retirement and run is audited.
- [x] No critical event is counted, adjusted or classified; the invariant tests pass; an explicit override still wins.
- [x] All 15 contract files pass in Go and Java; `mvn verify` and the Go suite pass.
- [x] CI green on the pull request (PR #18: go-service, java-service, web and python-sdk all passed).

### Review checklist
- Read `Tuning.Tuner.analyze`, `propose`, `approve`, `refresh`, and `Tuning.offset`, `weight`, `roundHalfAway`.
- Compare with Go's `Tuner.analyze`, `propose` and `offset`: the same steps, the same order.
- Run `mvn verify`, `go test ./...`, and the contract files against both services.
