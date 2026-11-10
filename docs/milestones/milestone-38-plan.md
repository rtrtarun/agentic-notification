# Milestone 38 plan: Learning

Status: **approved by the owner on 2026-11-10** (decisions recorded at the end). Build order: 38a, then 38b, then 38c, stopping after each slice for review. Follows [milestone 37](milestone-37.md). Roadmap entry: [roadmap.md](../roadmap.md#milestone-38-learning).

## Guardrails (hard-coded into the design, not optional)
1. **Offline or async only.** Learning is a background process that reads stored feedback and writes proposals. It is never a step in the live event path, adds no latency to it, and never calls a model, a network service or a database from it. The live path only reads a small in-memory snapshot, and if that snapshot is missing or stale the event is decided exactly as it is today.
2. **Critical events are untouchable.** Feedback about a critical event is never learned from. A critical event, a critical rule or a locked rule is never the subject of a proposal and is never adjusted. This is enforced when a proposal is made, when it is approved, and when it would be applied, and it is covered by invariant tests.
3. **Explicit beats inferred.** A person's explicit override (always, never) outranks anything learned. Events an override covers are never adjusted, and learning never edits or removes an override.
4. **Nothing changes by itself.** Every proposal is reviewable, attributable, audited, bounded and reversible. **An admin must explicitly approve every adjustment, whatever its size.** There is no automatic activation, no size under which one is skipped, and no autonomous drift: predictability and auditability come first in this first version.
5. **Not a black box.** "Training" here means a deterministic, explainable statistical rule over counts: the same feedback always gives the same proposals, each with its evidence in plain language. No neural network, no language model, no randomness.

## What exists today (Go, milestone 28)
`pkg/learning` turns stored feedback into **suggestions** an admin reviews: adaptive, demote, promote or an override, using evidence counts (dismissed, never, opened, reply, always) per source, event type and rule. Applying re-checks the guardrail and is audited; `GET /api/learning/suggestions` refreshes them on demand. Config: `LEARNING_MIN_SIGNALS`, `LEARNING_LOOKBACK`. Table `learning_suggestions`. Java has none of it. Two limits are known and are not fixed here: analysis runs inside the `GET` request, and it reads only the newest 1000 events and 5000 feedback rows (finding F10, milestone 42).

## 1. Goals
- **38a:** the Java service gets the same suggestions engine and API as Go (parity), proved by a shared contract file.
- **38b:** a new **offline score tuning** step, built in Go first: a background run reads stored feedback and proposes small, bounded **score adjustments** for the built-in heuristic, per source and event type (and optionally repository). An admin approves, rejects or retires each one. Approved adjustments change how the heuristic scores later non-critical events, so noise that people keep dismissing drifts to the digest and things they keep opening are protected from demotion.
- **38c:** the same score tuning in Java, held to a shared contract file.
- The suggestions flow stays as it is; score tuning is added beside it, not instead of it.

## 2. Architecture decisions
- **Same layers, same style.** Plain JDK in Java, the existing Go packages. A `learning` package per service with small interfaces (`FeedbackReader`, `EventReader`, `RuleEditor`, `OverrideWriter`, plus a new `AdjustmentStore`), so each part is testable alone.
- **Proposals, then approval, then a snapshot.** A run writes `proposed` adjustments. Only an admin's explicit approval makes one `active`, and no code path activates one otherwise (a test asserts this). The heuristic reads an in-memory snapshot of the active adjustments (`source`, `eventType`, `repository` mapped to a score offset), refreshed when one is approved or retired and on a short interval. The live path does a map lookup and no I/O.
- **How an adjustment is used.** The heuristic adds the offset to its score and adds a reason ("learned adjustment -12: 9 of 11 people dismissed this"). It still can only move an **adaptive standard** event to background (the existing pipeline rule); a positive offset only protects an event from demotion. `GuardedClassifier` and the critical refusal in `Heuristic.classify` are unchanged, so a critical event never reaches it. Events covered by an override never reach the classifier (existing behaviour, kept and tested).
- **The rule that proposes (deterministic, explainable):**
  - Count signals per scope over the lookback window, weighting recent signals more (fixed half-life, no randomness).
  - Positive = opened, reply, always. Negative = dismissed, never.
  - Use only signals about **delivered** events (queued or sent), not events already held for the digest, to avoid a feedback loop where a demoted event can no longer be opened and so looks worse and worse.
  - Require at least `LEARNING_MIN_SIGNALS` signals from at least `LEARNING_MIN_PEOPLE` distinct people, so one person cannot steer it.
  - `offset = round(max × smoothed(positive − negative) / (positive + negative + k))`, clamped to ±`LEARNING_MAX_ADJUSTMENT`. Small evidence gives a small offset.
  - A proposal also reports an **impact estimate**: how many of the last events it would have changed, computed by replaying recent events through the heuristic with and without it (a shadow run; nothing is changed).
- **Versioned and reversible.** Each active set has a version shown on decisions (`heuristic-v1+tuned-N`) so an event can be traced to the adjustments in force. Retiring an adjustment removes it from the snapshot; nothing else is rewritten.
- **Triggers.** The run is async: on a schedule (`LEARNING_TRAIN_EVERY`, default `0` = only when asked) or by `POST /api/learning/runs`, which returns at once with a run id. The existing suggestions refresh keeps working on `GET`.
- **Go first, then Java.** For 38b the contract file is written and run against the new Go code, which is the reference; 38c ports it to Java and must pass the same file.
- **Not in this milestone:** per-person learning and per-person preferences (needs per-person delivery, milestone 46), language models, changes to critical rules, automatic activation.

## 3. Database changes (Go owns migrations; additive and idempotent; Java never migrates)
- `learning_adjustments`: `id`, `scope_key` (unique), `source`, `event_type`, `repository`, `delta` (int), `status` (`proposed`, `active`, `rejected`, `retired`), `evidence` (JSONB: counts, people, window, summary), `impact` (JSONB: events replayed, events that would change), `version` (int, set on activation), `created_at`, `updated_at`, `resolved_by`, `resolved_at`.
- `learning_runs`: `id`, `status` (`running`, `done`, `failed`), `started_at`, `finished_at`, `feedback_read`, `events_read`, `proposals_made`, `error` (redacted).
- `learning_suggestions` is unchanged.

## 4. APIs (paths to be confirmed against Go before the contract files are written)
- **38a (Java, matching Go today):** `GET /api/learning/suggestions`, `POST /api/learning/suggestions/{id}/apply`, `POST /api/learning/suggestions/{id}/dismiss` (admin).
- **38b/38c (new, both services):**
  - `GET /api/learning/adjustments?status=` (any signed-in person).
  - `POST /api/learning/adjustments/{id}/approve`, `/reject`, `/retire` (admin; audited).
  - `POST /api/learning/runs` (admin; async, 202 with a run id) and `GET /api/learning/runs` (latest runs and their outcome).
- No change to `/events`, `/notify`, `/webhooks/github` or any critical path.

## 5. Folder changes
- Go: `pkg/learning` (tuning, adjustment and run stores), `pkg/attention` (the heuristic accepts an adjustment snapshot), `pkg/server` (handlers, scheduler), `pkg/config`, `pkg/db`.
- Java: new `Learning` (suggestions), `Tuning` (adjustments and runs), their stores, `LearningApi`; changes to `Heuristic`, `Config`, `HttpApi`, `Main`.
- Contract: `docs/contract/14-learning-suggestions.json` (38a, runs against today's Go) and `15-learning-adjustments.json` (38b, written against new Go).
- Docs: this plan, `milestone-38.md`, `java-service.md`, `implementation-sync.md`, `roadmap.md`, `CHANGELOG.md`, package READMEs.

## 6. Risks
- **Feedback loops.** Demoting an event makes it less visible, so fewer opens, so more demotion. Mitigations: learn only from delivered events, bounded offsets, a minimum number of distinct people, retirement by an admin, and the shadow impact estimate before approval.
- **Poisoning or noise.** One person dismissing everything. Mitigation: distinct-people minimum and a cap on how much one person's signals count.
- **Silent change of behaviour.** Mitigation: nothing is active until approved; every decision names the adjustment version; approvals are audited; retiring restores today's scores.
- **Critical-path leakage.** Mitigation: critical feedback is dropped at read, critical rules are excluded when proposing, approving and activating, and the classifier path is guarded as before; invariant tests for each.
- **Two services, one database.** Both read the same adjustments and runs. Two schedulers would double-propose, so a run takes a database lock and a proposal is keyed by scope (a second run updates it rather than adding one). Only one service should schedule runs (`LEARNING_TRAIN_EVERY=0` on the other), as with digests.
- **Data window.** Reads are limited to the newest events and feedback rows (F10); results say how much was read. Fixed in milestone 42.
- **Size.** Three slices in one milestone; each is committed and verified separately, and I will stop and ask if one runs long.

## 7. Tests
- **Unit:** the tuning rule (thresholds, distinct people, decay, smoothing, clamping, determinism, delivered-only), the impact replay, the snapshot (refresh, failure falls back to no adjustment), run locking, and Go versus Java agreement on the same fixtures.
- **Invariants (new, in both services):** feedback about critical events is never counted; a critical rule or locked rule never gets a proposal and approving one is refused; a critical event is never adjusted or classified; an event covered by an override is never adjusted; learning never edits or removes an override; with no active adjustments, scores and decisions are identical to today.
- **Contract:** files 14 and 15 pass in Go and Java; wrong methods, non-admin refusals, unknown ids, already-resolved proposals, and the guardrail refusals have exact messages.
- **Stores:** memory and PostgreSQL held to one contract test each; Java reads rows Go wrote and the reverse.
- **Real stack:** PostgreSQL and Redis with Go and Java together: feedback given through one service, a run in the other, approval, and the next event scored by both with the same adjusted score and reason.
- Offline `mvn verify` keeps the coverage gate; Go suite and `gofmt` stay clean.

## 8. Acceptance criteria
- Java returns the same suggestions as Go for the same feedback, and applying one changes the same rule or override.
- A run is asynchronous, returns at once, records what it read and made, and can be repeated without duplicating proposals.
- An approved adjustment changes the heuristic score and reason for matching non-critical events in both services identically; retiring it restores the old score.
- Nothing is active before approval, and every approval and retirement is audited.
- No critical event is ever counted, adjusted, classified, delayed or narrowed; every invariant test passes.
- A person's explicit override still wins over any adjustment.
- All contract files pass in Go and Java; `mvn verify` and the Go suite pass; CI is green; the live event path makes no new database or network call.

## Decisions (owner, 2026-11-10)
1. **Approval:** every adjustment must be explicitly approved by an admin, regardless of size. No automatic activation.
2. **Defaults:** at most ±20 points, at least 5 signals from at least 2 distinct people, a 30-day lookback and a 14-day half-life.
3. **Scope:** per-person preferences stay fenced off for milestone 46. This milestone is only global heuristic score tuning and the existing suggestion kinds.
4. **Trigger:** manual by default; `LEARNING_TRAIN_EVERY` turns on scheduled runs.
5. **Go changes:** yes. Go is the reference, so the new tables, endpoints and heuristic input are built there first (38b), and Java follows (38c).
6. **Slicing:** all three slices (38a Java suggestions parity, 38b Go tuning, 38c Java tuning) are one milestone, built in that order, with a stop and review after each slice.
