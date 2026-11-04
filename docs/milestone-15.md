# Milestone 15: app shell, design system, Inbox v2 and detail drawer

Delivered in two commits with one review stop at the end.

## Commit 1: shell and design system (done)

**Goals.** Replace the single-page console with a routed shell; every navigation entry resolves.

**What changed (frontend only, no API or database change).**

- Routing with `react-router-dom` (the one new dependency) using `HashRouter`, so deep links survive GitHub Pages and any static host. Screens are lazy-loaded.
- One navigation map, [lib/nav.ts](../apps/web/src/lib/nav.ts), drives the sidebar, mobile bottom bar, router and command palette. Unbuilt screens carry `plannedIn` and render an honest "not built yet" page: no sample numbers, no fake controls.
- Shell (`apps/web/src/app/`): collapsible sidebar (state remembered, tooltips and `aria-label` when collapsed, active bar indicator, unread badge on Inbox), workspace switcher (one workspace; says sign-in is needed for more), top bar (search, time range, palette button, bell, account menu), skip link, phone bottom bar, drawer below 62rem.
- Command palette, Ctrl/Cmd+K: fuzzy search over screens, mark all read, search, connection settings, themes, languages. Combobox/listbox semantics, focus returned on close.
- Toasts (`design/Toast.tsx`): polite live region; errors stay until dismissed.
- Design tokens (`design/tokens.css`): dark-first **Attention** theme (navy `#0b1020`, accent `#8f7bff`, critical `#ff526d`) is the default unless the system asks for light; semantic aliases (`--bg`, `--surface`, `--text`, `--accent`, ...) follow every theme. The existing themes still work.
- Shared state moved from `App.tsx` into `AppProvider` so the shell and every screen see the same events, read state and filters. The inbox list moved to `pages/InboxPage.tsx` with view tabs (replacing the old sidebar views). The detail sheet is hosted by the shell.
- 3 languages updated (key-parity test passes). 

**Known limits.**

- Not yet checked in a real browser or on a phone; layout CSS is untested visually.
- The list inside the inbox still has the old "signal flag" look; commit 2 redesigns it.
- `Overview`, `All Notifications`, `Activity`, `Rules`, `Policies`, `Channels`, `Integrations`, `Analytics`, `Preferences`, `Help` are placeholders.
- Primary colour `#7257FF` is 4.0:1 on the page, so text uses the lighter `#8f7bff` (5.1:1).

**Tests.** 87 Vitest tests (up from 77): fuzzy matcher, navigation map integrity.

## Commit 2: Inbox v2, detail drawer and backend state (done)

**Goals.** Read, archive, snooze, acknowledge and resolve are kept on the service (not in the browser); the inbox filters, sorts and pages on the server; a drawer explains each notification.

### Architecture decisions

- New package `go-server/notification-service/pkg/inbox` (the plan said `pkg/notification/state`; that name is taken by the send service). It reads events through `event.Repository` and writes only its own state, so the pipeline stays the only author of decisions. See its [README](../go-server/notification-service/pkg/inbox/README.md).
- **Four levels are a display mapping** over the three stored classes. Critical stays Critical and nothing can lower or raise it. Standard shows as **High** when the pipeline sent it to you, otherwise **Normal**. Background shows as **Low**. A person can override standard and background only. This is enforced in the service and tested, including in bulk.
- **Outcomes:** delivered (sent), held (queued, delayed, digest), suppressed (budget exceeded), merged, recorded.
- Filtering, sorting, counts and paging run in Go over the newest 1000 events, so memory and PostgreSQL behave the same. Merged repeats are hidden from the list and counted on the original (`repeatCount`).
- **Delete hides** the notification; the event is kept.
- **Snoozed** items stay listed (with the time) but do not count as unread. Snooze is 1 minute to 30 days.
- Frontend: one `InboxClient` interface with an HTTP and an in-memory (demo) implementation, so screens never branch on the data source. Optimistic updates (`features/inbox/optimistic.ts`) are rolled back, with a toast, if the service refuses. Search is debounced (250 ms). The old browser-only read list is sent to the service once and then forgotten.

### Database (idempotent, in `pkg/db/migrate.go`)

- `notification_state(event_id PK, read_at, archived_at, deleted_at, acknowledged_at, resolved_at, snoozed_until, priority_override, updated_at)`, cascading from `events`.
- `event_trail(id, event_id, step, detail, at)`: user actions only. Earlier steps are **reconstructed** from stored fields and flagged, because the store keeps one timestamp per event.
- Index on `events(dedup_key)`.

### API

All routes need the API key. Errors are `{"error": "..."}`.

| Method and path | Purpose |
|---|---|
| `GET /api/notifications` | `q`, `priority` and `outcome` (comma lists), `read`, `archived`, `source`, `repo`, `from`, `to`, `sort`, `page`, `pageSize`. Returns `items`, `total`, `page`, `pageSize`, `counts`. Sorts: `newest`, `oldest`, `priority`, `lowest`, `updated`. |
| `GET /api/notifications/{id}` | Item, trail, and related events (same thread, dedup key or repository). |
| `PATCH /api/notifications/{id}` | `read`, `archived`, `priority` (empty string clears). Returns the detail. |
| `DELETE /api/notifications/{id}` | Hide it. 204. |
| `POST /api/notifications/{id}/acknowledge` and `/resolve` | Also marks it read. |
| `POST /api/notifications/{id}/snooze` | `{"minutes": n}` or `{"until": <RFC 3339>}`. |
| `POST /api/notifications/bulk-actions` | `{ids (1 to 100), action, minutes?, priority?}`. One result per id; a failure does not stop the rest. |
| `POST /api/notifications/mark-all-read` | Returns `{changed}`. |

CORS now allows PATCH and DELETE. `/events`, `/feedback` and the other routes are unchanged. Metrics label every route above as `/api/notifications`, so ids cannot multiply labels.

### Frontend

- Inbox page with combinable filter tabs (All, Unread, Critical, High, Normal, Low, Held, Delivered, Suppressed, Archived) with counts, sort, time ranges (1h, 24h, 7d, 30d, all), selection with a bulk bar, a confirmation before delete, and a row menu (open, read/unread, acknowledge, resolve, snooze 1h or 1d, archive, copy link, delete).
- Loading skeletons, and empty and error states.
- The drawer shows why, the decision, details, the timeline (derived steps marked), related events, actions, change priority, and the existing feedback controls.
- A shared link `#/inbox?open=<id>` opens the drawer.

### Tests

- Go: `pkg/inbox` (mapping, filters, sorts, paging, state, snooze bounds, critical guard, bulk, trail, related, counts) and `pkg/server` handler tests (auth, validation, status codes, delete keeps the event). The whole module passes and `go vet` is clean. Coverage: inbox 75.9%, server 85.4%.
- Web: 101 Vitest tests, including the demo inbox, filter combining, query strings, optimistic updates and links. `tsc -b` and the build pass.
- Live check: started the service, sent three GitHub webhooks, and exercised list, acknowledge, priority, the critical guard, bulk and auth with curl.

### Known limits

- **Not yet seen in a browser** (this session had none). Layout, the drawer docking, the menu and the dialog need a visual pass at 360, 768 and 1280 px.
- Not done from the prompt, on purpose:
  - custom date range, group notifications, customize columns
  - mute source and retry delivery (they need the rules and delivery tables, milestones 18 and 19)
  - open original source and raw event JSON (the list API deliberately returns no payloads)
  - real-time push (milestone 20); the list refreshes every 15 seconds
- Lists run over the newest 1000 events. There is no PostgreSQL integration test for the new repository yet (it needs the `-tags integration` database).
- Stored decisions say "queued" for held events, so "suppressed" means only budget-exceeded events. Nothing is suppressed silently.
- The old signal-flag CSS for the removed list (`.row`, `.thread`) is still in `styles.css` and can go in a cleanup pass.

## Commit 3: dashboard redesign

The first inbox looked like a prototype. The console now follows the reference design.

- **Layout:** hero banner (title, "you're up to date" or unread and critical counts, mark all as read), four stat cards (Critical, High, Normal, Low) with real sparklines and click-to-filter, one list card with the filter tabs and rows, and a right rail with a **Quick stats** donut and **Recent activity**. The rail gives way to the detail drawer when one is open and drops below the list under 75rem; the stat cards go 4, 2, 2 across; below 40rem the bell art and sparklines are dropped.
- **Sparklines and the donut use real data.** The trend lines count events received per period from the loaded events (`lib/sparkline.ts`, tested); the donut uses the service's counts. Nothing is invented.
- **Components** in `features/dashboard/`: `InboxHero`, `StatCards`, `QuickStats`, `RecentActivity`, `BellArt`. Round, category-coloured `EventIcon` replaces the small flag shapes; the latest critical event is pinned above the list.
- **Look:** Inter (variable, self-hosted) replaces Bricolage Grotesque; rounded cards, tinted badges, gradient active nav item, quieter sidebar footer with a dark/light switch. A new light **Daylight** theme sits beside the dark **Attention** one; the full theme and language lists live in Settings.
- **Fixes found by screenshots:** grid tracks now shrink (`minmax(0, 1fr)`), so wide rows no longer push under the rail or sideways on a phone; tabs scroll with a fade instead of wrapping.
- Old list CSS (`.row`, `.thread`, `.lead` and friends) was deleted.
- Checked with headless Edge at 1600, 1024 and 500 px (the smallest window Edge allows; it is below the 40rem phone breakpoint). Not checked on a physical phone.

## Commit 4: more languages

The console now speaks nine languages: English, Spanish, **French, German, Portuguese (Brazil), Arabic, Japanese and Simplified Chinese** (plus Hindi from before). Every catalogue has all 330 strings, and a test fails if a language is missing a key or changes a `{placeholder}`.

- **Right to left.** Arabic sets `<html dir="rtl">` through the `dir` field on each `LOCALES` entry. The shell mirrors itself: the sidebar moves to the right, the rail to the left, drawers and toasts slide in from the other side, and arrows (`.arrow`) flip. Layout CSS now uses logical properties (`inline-start`, `inline-end`, `text-align: start`) instead of `left` and `right`; what is left lives in [app/rtl.css](../apps/web/src/app/rtl.css). Charts keep their left-to-right time axis; numbers and percentages are isolated so they read in order.
- **Choosing.** The saved choice wins, then the browser language, matching regional codes to the base language (`pt-BR`, `zh-TW`, `fr-CA` and so on), then English. The picker is in Settings and the command palette; each language is named in itself.
- **Fonts.** Inter covers Latin, Cyrillic and Greek. Arabic, Japanese, Chinese and Devanagari use the system fonts named in the `--font` stack, so nothing extra is downloaded.
- **Adding a language:** copy `en.ts` to `<code>.ts`, translate the values, add it to `Locale`, `LOCALES` (with `dir`) and `CATALOGS` in `lib/i18n/index.ts`, and add it to the catalogue list and locale-count test in `i18n.test.ts`.
- **Needs native review.** The translations were written for this change and have not been reviewed by native speakers. Dates and relative times use `Intl`, so they follow each language.
- Checked with headless Edge in Arabic and Japanese at 1600 px. The other new languages were not viewed; German and French strings are longer than English and the layout has not been checked against them.
