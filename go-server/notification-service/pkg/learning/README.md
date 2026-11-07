# learning

Turns stored feedback into suggestions an admin can review. It never changes anything by itself.

- `Service.Review` refreshes suggestions from recent feedback and returns the pending ones; `Apply` and `Dismiss` resolve one.
- Suggestion kinds: `adaptive` (let Attention refine a standard rule), `demote`, `promote`, `override` (a scoped always or never).
- Evidence rule: 5 or more dismissed or never signals with no opened, reply or always signal suggests quieting; 2 or more always signals with no negative signal on a background rule suggests promoting. Set with `LEARNING_MIN_SIGNALS`.
- **Guardrail:** feedback about critical events is ignored; a locked, disabled or critical rule is never suggested for; `Apply` re-checks the rule and refuses with `ErrGuardrail`.
- Feedback from before an applied suggestion does not count towards the next one for the same scope.
- `Repository`: `MemoryRepository` and `PostgresRepository` (table `learning_suggestions`), both held to one contract test.

Every dependency is a small interface (`FeedbackReader`, `EventReader`, `RuleEditor`, `OverrideWriter`). See [docs/milestones/milestone-28.md](../../../../docs/milestones/milestone-28.md).
