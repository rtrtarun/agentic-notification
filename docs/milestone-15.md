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

## Commit 2: Inbox v2, detail drawer and backend state (next)

Per the approved plan: `notification_state` and `event_trail` tables, `/api/notifications` endpoints, 4-level priority display mapping, multi-filter tabs, bulk actions, optimistic updates, and the drawer with "why", lifecycle and related events.
