# Milestone 28: Learning engine (feedback to reviewable suggestions)

## Goals
- Close the loop from Layer 4 (feedback) back to Layers 1 and 2: stored dismissals, mutes, opens, replies and "always notify" choices become concrete, reviewable proposals.
- Nothing changes on its own. An admin reads the evidence and applies or dismisses each suggestion; applying is audited.

## Architecture decisions
- **`pkg/learning`** holds a `Service`, a `Repository` interface (memory and PostgreSQL) and small interfaces for everything it touches (`FeedbackReader`, `EventReader`, `RuleEditor`, `OverrideWriter`). `feedback.Store`, the event repository, `*rules.Service` and `*overrides.Service` satisfy them, so each is faked in tests and the package imports no HTTP or SQL.
- **How evidence is read.** Feedback (default last 30 days, at most 5000 entries) is joined to the stored event by ID to learn the rule that matched it and its attention class, then grouped by source, event type, matched rule and class. The repository is kept on a suggestion only when every report came from one repository.
- **Rules of suggestion** (thresholds `LEARNING_MIN_SIGNALS`, default 5, and 2 for "always"):
  - A **standard** group with at least 5 dismissed or never signals and **zero** opened, reply or always signals: if an unlocked standard rule matched and is not adaptive, suggest enabling **adaptive** (the gentlest step); if it is already adaptive, suggest **demoting** it to background; if no editable rule matched, suggest a scoped **never override**.
  - A **background** group with at least 2 always signals and **no** negative signal: suggest **promoting** the background rule to standard, or an **always override** if no rule matched.
- **The guardrail, enforced twice.** Feedback about a critical event is ignored. A rule that is locked, disabled or critical is never the subject of a suggestion. When applying, the rule is read again and refused with `ErrGuardrail` if it has since become locked or critical, and no apply path can write a critical class. The existing rules and overrides services keep their own protections underneath.
- **Feedback from before a change does not argue for the next one.** After a suggestion is applied, only later feedback about the same source, type and rule counts. (A unit test found that without this, applying "adaptive" immediately produced a "demote" suggestion from the same old dismissals.)
- **Stable identities.** A suggestion's ID is a hash of kind, source, type, repository and rule, so analysis is idempotent: a pending one has its evidence refreshed in place, and an applied or dismissed one is never made again.
- **Analysis runs on read.** `GET /api/learning/suggestions` refreshes and returns the pending ones. It is cheap (one bounded feedback read) and avoids a background job until Milestone 29 adds a worker.

## Database changes
New table `learning_suggestions` (id, status checked to pending, applied or dismissed, JSON body, created and updated times) and index `(status, updated_at DESC)`, both idempotent. A migration test guards them.

## APIs
| Route | Access | Purpose |
|---|---|---|
| `GET /api/learning/suggestions` | Signed in | Pending suggestions, refreshed from the latest feedback, with the evidence threshold in use |
| `POST /api/learning/suggestions/{id}/apply` | Admin | Makes the change (edits a rule or creates an override), marks it applied, writes audit entry `learning.apply` |
| `POST /api/learning/suggestions/{id}/dismiss` | Admin | Marks it dismissed so it is not made again; audit entry `learning.dismiss` |

Errors: 404 unknown suggestion, 409 already resolved or rule deleted, 403 guardrail or not an admin, 405 wrong method. New settings: `LEARNING_MIN_SIGNALS` (default 5), `LEARNING_LOOKBACK` (default 720h).

## Folder changes
New `pkg/learning` (model, memory and PostgreSQL repositories, service, tests, README) and `pkg/server/learning_handlers.go`. `pkg/db/migrate.go`, `pkg/config`, `pkg/server` wiring. Web: `lib/learning.ts` (types, demo client, HTTP client), `features/manage/LearningPanel.tsx`, shown on the Rules screen. As with digests and overrides, the demo data lives with the client rather than in `lib/demo.ts`; demo mode shows three sample suggestions and applying or dismissing them works.

## Risks
| Risk | Handling |
|---|---|
| A suggestion weakens something important | Admin review, guardrail twice, critical and locked rules excluded, audit trail, rules stay editable and reversible |
| Noisy or biased feedback | Needs 5 or more negative signals with no positive one; one opened or reply blocks it |
| Stale evidence re-suggests after a change | Feedback before the last applied suggestion for the same scope is ignored |
| Events older than the recent window cannot be classified | Their feedback is skipped, not guessed |

## Tests
`pkg/learning` (80.9 percent): each suggestion kind, thresholds, a single opened signal blocks, repository scoping, background promotion and its blocks, **locked and critical rules never suggested**, **feedback on critical events ignored**, **apply refused when a rule became locked or critical and nothing is written**, apply never yields a critical rule, apply adaptive, demote, promote and override (mode and author), rule deleted, dismissed ones not returned and pending evidence refreshed in place, unknown ID, lookback window, unknown events skipped, repository contract. `pkg/server`: the whole flow over HTTP (dismiss five pull requests, see the suggestion, a member can read but not apply, admin applies, the rule is now background, audit entry written, second apply is 409, it does not come back), dismiss changes nothing, **eight critical advisories dismissed and muted produce no suggestion and the critical rule is untouched**, access and validation table. `pkg/config`, `pkg/db`. Web: evidence helpers, demo client, HTTP client, refused change reads clearly (220 Vitest tests).

## Acceptance criteria
- [x] Repeated negative feedback with no positive signal produces a human-reviewable suggestion.
- [x] Repeated "always" on background events produces a promotion suggestion.
- [x] Critical events and locked or critical rules are never suggested for and cannot be changed (unit and HTTP tests).
- [x] Apply is admin only, changes the rule or creates the override, and is audited.
- [x] Learning Suggestions panel on the Rules screen with Apply and Dismiss, working in demo mode.
- [x] `gofmt`, `go vet` (also with `-tags integration`), `go test -cover ./...`, `npm test`, `npm run build` pass locally.
- [ ] The PostgreSQL repository contract passes against a real database. Not run locally; CI runs it.

## Known limits
- Suggestions are workspace-wide; per-person learning needs Milestone 30.
- The rules are simple counts, not a statistical model. Weights and thresholds are named constants and one config value.
- Only the newest 1000 stored events can be joined to feedback.
- No model assistance yet; a model could later phrase the summary through the existing `Completer`, behind the same guardrail.
