# learning

Turns stored feedback into suggestions an admin can review. It never changes anything by itself.

- `Service.Review` refreshes suggestions from recent feedback and returns the pending ones; `Apply` and `Dismiss` resolve one.
- Suggestion kinds: `adaptive` (let Attention refine a standard rule), `demote`, `promote`, `override` (a scoped always or never).
- Evidence rule: 5 or more dismissed or never signals with no opened, reply or always signal suggests quieting; 2 or more always signals with no negative signal on a background rule suggests promoting. Set with `LEARNING_MIN_SIGNALS`.
- **Guardrail:** feedback about critical events is ignored; a locked, disabled or critical rule is never suggested for; `Apply` re-checks the rule and refuses with `ErrGuardrail`.
- Feedback from before an applied suggestion does not count towards the next one for the same scope.
- `Repository`: `MemoryRepository` and `PostgresRepository` (table `learning_suggestions`), both held to one contract test.

Every dependency is a small interface (`FeedbackReader`, `EventReader`, `RuleEditor`, `OverrideWriter`). See [docs/milestones/milestone-28.md](../../../../docs/archived/milestones/milestone-28.md).

## Score tuning (milestone 38b)
`Tuner` turns the same stored feedback into bounded **score adjustments** for the built-in heuristic. It is offline: a run reads feedback, events and rules and writes proposals; it is never part of the live event path.

- **The rule** (deterministic and explainable): for each source and event type, signals about *delivered* (queued or sent), non-critical events in the lookback are weighted by age (half-life `LEARNING_HALF_LIFE`, default 14 days, to six decimals), each person's influence is capped (`PerPersonCap`), and `offset = round(max × (P − N) / (P + N + 2))`, clamped to ±`LEARNING_MAX_ADJUSTMENT` (default 20). Positive signals are opened, reply and always; negative are dismissed and never. At least `LEARNING_MIN_SIGNALS` signals from at least `LEARNING_MIN_PEOPLE` (default 2) people are needed.
- **Proposals, then approval, then a snapshot.** A run saves `proposed` adjustments (one per scope; running again refreshes it) with their evidence and an **impact estimate** (recent events replayed through the heuristic with and without it). Only an admin's `Approve` makes one `active`; `Reject` is final; `Retire` switches an active one off. Approving and retiring bump the set version and refresh `attention.Tuning`, the in-memory snapshot the heuristic reads (`Heuristic.WithTuning`). If the snapshot was never loaded or goes stale (5 minutes without a refresh) events are scored exactly as without learning.
- **Guardrail**, enforced when proposing, when approving and when building the snapshot: feedback about a critical event is never counted; a scope covered by a critical or locked rule is never proposed, approved or applied. Explicit overrides are never read or written by the tuner, and the pipeline does not classify events an override covers, so an override always wins.
- **Runs**: `Start` (returns at once, the run continues in the background), `RunNow`, `Schedule` (`LEARNING_TRAIN_EVERY`, default off). One run at a time across every service sharing the database (`RunRepository.Start` and a partial unique index); a run left running for 10 minutes is treated as abandoned.
- Stores: `MemoryAdjustments`, `PostgresAdjustments` (table `learning_adjustments`), `MemoryRuns`, `PostgresRuns` (table `learning_runs`), each held to one contract test (memory always, PostgreSQL in the integration build).
