# Milestone 24: Feedback and overrides

## Goals
- Capture what people do with a notification (opened, dismissed, undo, always notify, never notify, replies) as stored, attributed signals.
- Make "always notify" and "never notify" real: a visible, reversible choice that the deterministic layer applies to later events.
- Keep the guarantee: **critical events never follow an override**, and "never" cannot even be recorded against one.

## Architecture decisions
- **Layer 4 feeds Layer 1, deterministically.** An override is a plain rule (source, event type, repository; `*` allowed in the repository). The most specific match wins; on a tie **"always" wins**, because missing something is worse than one extra message. No model is involved.
- **Overrides outrank quiet hours and mutes.** "Never" turns a standard event into a background one with status `digest`, so nothing is queued for delivery; "always" keeps it deliverable whatever preferences say. The decision's reason names the override ("never notify override for github / star"), so the Why panel explains it.
- **Scope of a choice:** from a notification, a person picks *this kind of event* (default), *this repository* or *this source*.
- **Undo reverts the override the same person's latest always or never created.** It skips unrelated signals in between, never reverts someone else's choice, and undoing twice does nothing more.
- **Attributed and audited.** Every feedback row and override records who made it (the person's email, or `api-key`), and creating or removing an override is written to the security log.
- **Workspace-wide, not per person.** I had planned per-user preferences. Delivery targets (the email list, the Slack and Teams webhooks, SMS numbers) belong to the workspace, so a per-person setting would change nothing a person receives. Overrides and preferences therefore stay workspace-wide, with the person recorded. Per-person delivery is a separate piece of work.
- **Who may change what is delivered:** by default any signed-in person. Set `OVERRIDES_ADMIN_ONLY=true` to limit that to admins; a member's always or never is then stored as a signal only and the console says so.
- **Opened** is recorded once per person and event when someone opens a notification.

## Database changes
`feedback` (id, event, person, action, scope, reason, override id, and the event's source, type and repository at the time) and `overrides` (unique per scope, mode check). Both tables are idempotent migrations.

## APIs
| Route | Purpose |
|---|---|
| `POST /feedback` | `{eventId, action, scope?, reason?}`. Actions: `opened`, `dismissed`, `undo`, `always`, `never`, `reply`. Returns `override`, `overrideSkipped`, `reverted`. 400 for a bad action, scope, empty reply or `never` on a critical event; 404 for an unknown event |
| `GET /api/overrides` | Every override, newest first, and whether only admins may change them |
| `POST /api/overrides` | Add one directly: `{mode, source?, eventType?, repository?, note?}` (`createdBy` is always the caller) |
| `DELETE /api/overrides/{id}` | Remove one |

Configuration: `OVERRIDES_ADMIN_ONLY` (default `false`).

## Folder changes
New `pkg/overrides`; `pkg/feedback` rewritten around a `Store` (memory and PostgreSQL) and a `Service` (the old in-memory recorder is gone); `pkg/pipeline` gains `WithOverrides`; `pkg/server/feedback_handlers.go`; web `lib/overrides.ts` and `features/manage/OverridesPanel.tsx` (on the Preferences screen); the feedback panel in the notification drawer gains an "Applies to" choice and tells a member when their choice was only recorded.

## Risks and how they are handled
| Risk | Handling |
|---|---|
| Someone silences something important | Overrides are listed with who chose them, removable by anyone allowed, audited, and `OVERRIDES_ADMIN_ONLY` exists. A critical event is immune |
| A broad "never" hides too much | Scope is explicit, most specific wins, and "always" wins ties |
| Feedback on an event that has aged out | The lookup reads the newest 1000 events (the same window as the inbox); older ones answer 404 |
| Stale overrides with several replicas | Decisions use a per-process cache loaded at start and updated on change; other replicas pick a change up on restart |

## Tests
`pkg/overrides` (validation, matching and wildcards, critical immunity, specificity and ties, replace-by-scope, reload, reasons); `pkg/feedback` (signals with context, scope mapping, critical refusal, signal-only callers, undo rules, validation, store filters and bounds); `pkg/pipeline` (never, always, precedence over preferences, critical untouched); `pkg/server` (feedback changes the next event, critical keeps arriving, undo, validation, attribution and audit, admin-only mode, override endpoints, opened once). Web: `lib/overrides.test.ts`. 207 Vitest tests.

## Acceptance criteria
- [x] "Never notify" for a kind of event moves later events of that kind to the digest and queues nothing; other kinds are unaffected.
- [x] A critical event from the same source still arrives and is sent, whatever overrides exist.
- [x] Undo restores normal delivery.
- [x] Every choice is listed, attributed and removable.

## Known limits (honest list)
- **Not per person:** see above. A "never" from one person applies to everyone.
- **The PostgreSQL stores for feedback and overrides were not run against a real database** (Docker was not running); the memory stores are fully tested.
- Overrides cannot target a Slack channel or a person, only events.
- No expiry on an override. A "snooze this kind for a week" is a possible next step.
- New text is English only; other languages fall back.

## Review checklist
- [ ] Open a notification, choose Never notify me (this kind of event), send another of that kind: it is in the digest and the Why panel names the override.
- [ ] Send a security advisory: still critical and sent.
- [ ] Open Preferences, see the choice with your name, remove it.
- [ ] Set `OVERRIDES_ADMIN_ONLY=true`, repeat as a member: the console says it was only recorded.
