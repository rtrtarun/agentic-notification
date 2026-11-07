# Frontend: the Attention console

The web app in [`apps/web`](../apps/web) is an operator console. It answers two questions for the person running the platform: **what did the system decide about each event, and why?** and **was that right?** It reads the platform's read APIs (inbox, rules, channels, digests, traces and more) and writes feedback, rules, preferences and overrides back. A complete demo mode runs the whole console on sample data with no service.

Component-level detail, design tokens, wireframes and the skills audit: [ui-architecture.md](archived/design-notes/ui-architecture.md). Architecture context: [architecture.md](architecture.md) (sections 9, 10.6, 19). Milestone record: [milestones/milestone-14.md](archived/milestones/milestone-14.md).

## Audience and job

- **Audience:** engineers on call who run the service and tune what interrupts people.
- **Primary job:** see recent decisions at a glance, open one to see the reason, and correct it.
- **Also the job, since milestones 15 to 33:** managing rules, channels, preferences and digests, reviewing suggestions from feedback, and following AI agent runs. Newer screens are described in "Screens as of milestone 33" below; the sections after it describe the inbox core.

## Design language

The identity comes from the subject, not a template: notifications are signals, and critical means distress, so the console borrows from maritime signal flags.

### Colour

| Name | Hex | Use |
|---|---|---|
| Harbour | `#E6ECEE` | Page ground (cool, not cream) |
| Deck | `#F4F8F9` | Raised surfaces: the detail sheet, selected rows, fields |
| Deep sea | `#10283B` | Ink and primary buttons (not black) |
| Distress | `#C8281F` | Critical events, errors |
| Signal blue | `#2058B8` | Standard events, links, keyboard focus |
| Pennant | `#E3B21C` | Merged events and the thread line |
| Fog | `#8DA0AB` | Shapes and rules only. **Never text** (contrast 2.4:1) |

Contrast on Harbour: Deep sea about 12:1, Distress about 4.9:1, Signal blue about 5.7:1. Pennant and Fog are never used for text.

### Type

One family, **Bricolage Grotesque** (variable, weight 200 to 800, width 75% to 100%), self-hosted through `@fontsource-variable`. The **width axis carries meaning**, so the type is part of the information:

| Class | Width | Weight | Size |
|---|---|---|---|
| Critical | 100% (full) | 750 | 1.95rem |
| Standard | 90% | 500 | 1.25rem |
| Background | 78% (condensed) | 350 | 1rem |

Scale: major third on a 1rem base (0.875, 1, 1.25, 1.5625, 1.953) plus a fluid lead size. Prose lines stay under 62 characters. Everything is sentence case. There are no all-caps labels, no monospace "data" face and no eyebrow labels.

### Flags: colour and shape both encode the class

| Kind | Shape | Colour |
|---|---|---|
| Critical | Solid pennant (triangle) | Distress |
| Standard | Square flag | Signal blue |
| Background | Thin outlined flag | Fog |
| Merged | Swallowtail | Pennant |

Shape means the meaning survives without colour vision. In forced-colours mode the flags fall back to the system text colour. Each flag has an accessible name.

### Layout

- Left-aligned, one view, no sidebar and no stat cards.
- **Row height follows importance:** critical rows are tall, standard are medium, background are slim. Density is information.
- **Merged events** hang from the original on a pennant-yellow thread line.
- **Lead:** the first thing on the page is the latest critical event in the largest type, with the rule's reason, plus one sentence about the last hour ("In the last hour: 2 sent to you, 5 held back, 3 merged."). It replaces a row of counters.
- **Detail sheet:** docks beside the list from 80rem and fills the screen below that; the sidebar is a fixed column from 62rem and a drawer below it.
- Structure is only used where it encodes something: the thread line (a thread), flags (class), the numbered steps (a real sequence: setting up a webhook).

### Motion

One thing moves: the detail sheet fades and slides in (180 ms, strong ease-out) when someone opens an event, which shows what changed. Controls scale to 0.97 while pressed (140 ms). Nothing animates on load or scroll, and closing the sheet is instant. `prefers-reduced-motion` keeps a fade and removes the movement and the press scale.

## Inbox, languages and colour

The console works like an inbox. The **sidebar** lists the views (All, Critical, Standard, Background) with counts. The **top bar** has a search box (every word must match, in the language shown), a time range (1h, 24h, 7d, All) and a **bell** with the unread count and a short list of the latest unread events. Opened events become read; **Mark all as read** clears the rest. The list shows 10 threads per page.

Each row has a **coloured chip** for what kind of event it is (Security, Code, Issues, Builds, Releases, Social, Other) next to the flag that says how much it matters. Five **colour themes** (Harbour, Violet, Forest, Sunset, Night) and three **languages** (English, Español, हिन्दी) are chosen in the sidebar and remembered. Details: [ui-architecture.md](archived/design-notes/ui-architecture.md) section 14a.

## First use and orientation

A first visit shows a **welcome screen** rather than an error: one sentence on what Attention does, the three classes in plain words, and two choices, **Try the demo** and **Connect your service**. **Demo mode** uses built-in sample events (`lib/demo.ts`), sends nothing and saves nothing; a banner and the header's "Sample data" status make that clear, and "Connect your service" leaves it. The hosted build starts in demo mode (`VITE_DEFAULT_DEMO=true`). Above the list, a short key explains Critical, Standard and Background, and a line says to select an event. In the sheet, the heading is **Was this right?** and each choice has a sentence under it saying what it does.

## Screens as of milestone 33

Fifteen screens, all in the navigation map (`src/lib/nav.ts`), all working in demo mode. Each new screen has a typed client in `src/lib/<area>.ts` with a demo and an HTTP variant, loads through `useLoad` and `LoadGate`, and keeps every `fetch` inside `lib/api.ts`.

| Screen | Route | Milestone | What it is for |
|---|---|---|---|
| Overview | `/overview` | 17 | What needs attention now, and a summary of decisions |
| Inbox | `/inbox` | 15 | Notifications with state, filters, bulk actions and a detail drawer |
| All notifications | `/all` | 16 | A sortable, filterable table with CSV export |
| Activity | `/activity` | 17 | A feed of what the platform did |
| Digests | `/digests` | 25 | What was held back, written by rules or a model (labelled) |
| Traces | `/traces` | 31 | AI agent runs: timeline, retries, tokens, cost and decisions per step |
| Rules | `/rules` | 18 | Create, order, dry-run rules; suggestions from feedback (28) |
| Policies | `/policies` | 18 | What the rules enforce, including the locked critical rules |
| Channels | `/channels` | 19, 22 | Where messages go, test sends, the delivery queue |
| Integrations | `/integrations` | 19 | Sources and their status |
| Users and access | `/users` | 23 | Accounts, roles, audit trail |
| Analytics | `/analytics` | 17 | Charts of volume and outcomes |
| Preferences | `/preferences` | 20, 24 | Quiet hours, mutes, always and never choices |
| Settings | `/settings` | 14 onward | Connection, theme and language |
| Help | `/help` | 21 | Get started, glossary, FAQ, a copyable diagnostics summary |

Not yet in the console (API only): rule payload predicates and per-rule channels (milestone 30), personal preferences and "my digest" (30). Themes: Harbour, Violet, Forest, Sunset and Night. Languages: English, Spanish, French, German, Portuguese, Hindi, Arabic, Chinese and Japanese; screens added from milestone 16 on are English only.

## Screens and states

| State | When | What it says | Action |
|---|---|---|---|
| Welcome | First visit, nothing chosen | What Attention does, how events are sorted | Try the demo, Connect your service |
| Events | Service has events | Sidebar views, search, time range, bell, lead banner, list with coloured chips and unread dots, pager | Open an event, search, filter, mark all read |
| Event detail | A row is selected | Why it happened, the rule, what the class means, feedback controls | Dismiss, Always notify me, Never notify me, Add a note, Undo |
| Empty | Connected, no events | "No events yet." plus three real setup steps and the webhook URL | Copy webhook URL |
| Unauthorized | 401 | "The service rejected this API key." | Enter API key |
| Not configured | 503 | "The service has no API key, so it refuses every request." | Enter API key |
| Unreachable | Network error | "Can't reach {address}." and the exact CORS setting to check | Try again, Change address |
| Server error | 5xx | "The service hit an error." | Try again |
| Connection settings | Always available | Service address and API key | Save and connect |

Rules for the copy: sentence case, active voice, plain words (the service says "queued", the console says "Held back"). A button keeps its name through the whole flow: **Dismiss** produces **Dismissed.** Errors never apologise and always say what to do next.

### Feedback rules

- Actions sent to the service: `dismissed`, `always`, `never`, `undo`, `reply`.
- **Critical events never offer "Never notify me."** The platform's rule is that critical events always reach the person, and the sheet says so instead of hiding the option silently.
- After an action the sheet shows a confirmation with **Undo**, which sends `undo` for the previous action.

## Architecture

```
apps/web/src
├── main.tsx              entry
├── App.tsx               state: settings, selection, clock; composes the screen
├── styles.css            tokens and all component styles (single-class selectors)
├── hooks/useEvents.ts    loads events, refreshes every 15 s while the tab is visible,
│                         keeps the last events on screen during an outage
├── lib/
│   ├── api.ts            fetch client; maps every failure to a kind the UI can explain
│   ├── events.ts         plain-language labels, thread grouping, last-hour summary, relative time
│   └── settings.ts       service address and API key in sessionStorage
└── components/
    ├── Sidebar.tsx       views with counts, connection, language and theme (a drawer on small screens)
    ├── TopBar.tsx        menu button, search, time range, bell
    ├── NotificationBell.tsx  unread badge and popover
    ├── Pager.tsx, Chip.tsx, Appearance.tsx, Welcome.tsx, Legend.tsx
    ├── Lead.tsx          latest critical event and the last-hour sentence
    ├── Ledger.tsx        the list for one page, with merged events nested
    ├── EventSheet.tsx    detail and feedback
    ├── SettingsDialog.tsx  native <dialog>
    ├── Notices.tsx       error and empty states
    └── Flag.tsx          the signal flag
```

Rules: components render; `lib/` holds logic and has the unit tests; only `lib/api.ts` calls `fetch`; there are no runtime dependencies beyond React and the font.

## Data and security

- Reads `GET /events?limit=200` (newest first, no raw payloads) and posts to `POST /feedback`. Both need `X-API-Key`.
- The API key is entered in **Connection settings** and kept in `sessionStorage`, so closing the tab forgets it. `VITE_API_KEY` can seed it for local development only: **anything in a built bundle is public.** Do not set it in a deployed build. Real user authentication is on the roadmap.
- The service must list the console's origin in `CORS_ALLOWED_ORIGINS` (`http://localhost:3000` for the dev server and preview). Docker Compose sets it.

## Accessibility

Visible keyboard focus (3px Signal blue ring), native `<dialog>` for settings (focus trap and Escape), the detail sheet moves focus to its heading and returns it on close and closes on Escape, status text is announced (`role="status"` and `role="alert"`), state is never colour-only, reduced motion is respected, and the layout holds down to 390px wide. Below 80rem, where the sheet covers the screen (and when the phone drawer is open), the page behind it is `inert`. Hover styles apply only to devices that can hover; touch devices get press feedback instead. See [ui-architecture.md](archived/design-notes/ui-architecture.md) section 15 for the mobile checklist.

## Run, test, build

```bash
cd apps/web
npm install
npm run dev        # http://localhost:3000
npm test           # 230 unit tests (clients, helpers, demo data, translations)
npm run build      # type-check and production build
```

Point it at a service with `VITE_BACKEND_URL` (default `http://localhost:8080`). To see it with data, run the service with `API_KEY=dev-key ALLOW_UNSIGNED_WEBHOOKS=true CORS_ALLOWED_ORIGINS=http://localhost:3000`, post a few events to `/webhooks/github`, then enter `dev-key` in Connection settings.

## Known limits

- The inbox, All notifications and `GET /events` page by cursor or page number; the Traces screen has no filters or search yet.
- Live updates use server-sent events; the screens that poll refresh every 15 to 60 seconds as a fallback.
- Feedback is stored, becomes overrides, and feeds the suggestions on the Rules screen (architecture section 3).
- Component rendering is not unit-tested (no DOM test runner); behaviour is covered at the `lib/` level and was checked in a browser at desktop and mobile widths.
