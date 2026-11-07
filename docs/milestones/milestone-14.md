# Milestone 14: Operator Console and Read API

**Status: complete.** Built after Milestone 13. Not in the original `AGENTS.md` plan; it implements step A of the forward roadmap ([architecture.md](../architecture.md) section 13).

## Goals

1. Let a person see what the system decided about each event and why (the backend stored decisions but exposed no way to read them).
2. Give feedback from the same screen, with the platform's rules visible (critical events cannot be silenced).
3. Replace the placeholder dashboard (a health check and a feedback form) with a console that has its own identity.

## Architecture decisions

- **Read model, not the entity.** `GET /events` returns an `eventSummary` without the raw payload. Payloads can carry names and emails, and the list does not need them.
- **`Event.CreatedAt`.** The table already had `created_at`; the entity now carries it so the console can show when. The in-memory repository sets it on save; PostgreSQL returns it from `List`.
- **Newest first, capped.** The handler reverses `List` (oldest first, capped at 1,000) and applies `limit` (default 100, maximum 500). Cursor pagination is deferred until the table outgrows the cap.
- **No new frontend runtime dependencies** beyond the self-hosted font. Plain `fetch`, React state and one hook.
- **API key in `sessionStorage`**, entered by the user, instead of a key baked into the bundle.
- **Design language from the subject** (signal flags): see [frontend.md](../frontend.md).
- Generated TypeScript output (`tsbuildinfo`, `vite.config.js`, `vite.config.d.ts`) is now untracked and ignored.

## Database changes

None. `created_at` already existed (Milestone 13 step 6 added its index).

## APIs

| Route | Auth | Behaviour |
|---|---|---|
| `GET /events?limit=N` | `X-API-Key` | `{"events":[{id, source, eventType, repository, attentionClass, decisionState, threadId, mergedInto, createdAt}]}` newest first. `limit` 1 to 500, otherwise 400. 500 returns a generic error |

`/events` is a known metrics route label. CORS and auth behave as for other protected routes.

## Folder changes

```
apps/web/src/{App.tsx, styles.css}            rewritten
apps/web/src/components/*                      new (Header, Lead, Ledger, EventSheet, SettingsDialog, Notices, Flag)
apps/web/src/hooks/useEvents.ts                new
apps/web/src/lib/{api,events,settings}.ts      new, with *.test.ts
apps/web/{index.html, vite.config.ts, package.json}   favicon and description; vitest; test script
go-server/notification-service/pkg/event/{model,repository,postgres_repository}.go   CreatedAt
go-server/notification-service/pkg/server/{handlers,middleware}.go                  eventsHandler, route label
docs/{frontend.md, milestone-14.md, architecture.md, CHANGELOG.md}
```

## Risks

| Risk | Mitigation |
|---|---|
| The console shows `queued` events that nothing will ever process, which could look broken | The sheet states plainly that standard events are held back and digests are not available yet |
| An API key in a built bundle is public | Key entered at runtime and kept per tab; `VITE_API_KEY` documented as local only |
| `List` is capped at 1,000 rows, so older events are invisible | Documented; cursor pagination is the next step when needed |
| Polling load | 15 s, only while the tab is visible; one cheap indexed query |
| Colour-only meaning | Every class also has a distinct shape and an accessible name; Fog and Pennant never carry text |

## Tests

- **Go:** newest-first order, `limit` handling and rejection of `0`, `-1`, `abc`, `501`, empty store serialises as `[]`, payloads (and emails inside them) never appear, auth and method checks, generic 500, `createdAt` populated, route label. PostgreSQL integration test asserts `CreatedAt` round-trips.
- **Web (34, Vitest):** the API client (URL joining, key header, every HTTP status mapped to a kind, network failure, abort passthrough, JSON bodies), the language helpers (labels, thread grouping with and without the original loaded, last-hour counts including the boundary, sentence building, relative time, plurals) and settings storage (defaults, round trip, corrupt and wrong-typed storage, URL validation).
- **Browser check** (Edge, real backend with seeded events): desktop list, open sheet, feedback and undo, settings dialog, mobile list and sheet, and the empty, unauthorized and unreachable states.

## Acceptance criteria

- [x] The console lists recent events newest first with class, status and time.
- [x] Opening an event shows the reason, the matched rule and what its class means.
- [x] Dismiss, Always notify me, Never notify me, notes and Undo reach `POST /feedback`; critical events do not offer "never".
- [x] Merged events appear under the event they were merged into.
- [x] Wrong key, missing key configuration, unreachable service and empty service each show a specific message and next action.
- [x] No raw payload leaves the service through the list.
- [x] Works at 390px width, with keyboard only, and with reduced motion.
- [x] Go and web tests, lint, vulnerability scan and build pass.
