# Frontend: the Attention console

The web app in [`apps/web`](../apps/web) is an operator console. It answers two questions for the person running the platform: **what did the system decide about each event, and why?** and **was that right?** It reads from `GET /events` and writes to `POST /feedback`.

Component-level detail, design tokens, wireframes and the skills audit: [ui-architecture.md](ui-architecture.md). Architecture context: [architecture.md](architecture.md) (sections 9, 10.6, 19). Milestone record: [milestone-14.md](milestone-14.md).

## Audience and job

- **Audience:** engineers on call who run the service and tune what interrupts people.
- **Primary job:** see recent decisions at a glance, open one to see the reason, and correct it.
- **Not the job (yet):** managing rules, preferences or digests. Those need backend work (architecture section 13).

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
- **Detail sheet:** docks beside the list on wide screens (62rem and up) and fills the screen on small ones.
- Structure is only used where it encodes something: the thread line (a thread), flags (class), the numbered steps (a real sequence: setting up a webhook).

### Motion

One thing moves: the detail sheet fades and slides in (180 ms, strong ease-out) when someone opens an event, which shows what changed. Controls scale to 0.97 while pressed (140 ms). Nothing animates on load or scroll, and closing the sheet is instant. `prefers-reduced-motion` keeps a fade and removes the movement and the press scale.

## Screens and states

| State | When | What it says | Action |
|---|---|---|---|
| Events | Service has events | Lead, filters (All, Critical, Standard, Background), list | Open an event |
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
    ├── Header.tsx        wordmark, connection status, settings link
    ├── Lead.tsx          latest critical event and the last-hour sentence
    ├── Ledger.tsx        filters and the list, with merged events nested
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

Visible keyboard focus (3px Signal blue ring), native `<dialog>` for settings (focus trap and Escape), the detail sheet moves focus to its heading and returns it on close and closes on Escape, status text is announced (`role="status"` and `role="alert"`), state is never colour-only, reduced motion is respected, and the layout holds down to 390px wide. Below 62rem, where the sheet covers the screen, the page behind it is `inert`. Hover styles apply only to devices that can hover; touch devices get press feedback instead. See [ui-architecture.md](ui-architecture.md) section 15 for the mobile checklist.

## Run, test, build

```bash
cd apps/web
npm install
npm run dev        # http://localhost:3000
npm test           # 34 unit tests (api client, language helpers, settings)
npm run build      # type-check and production build
```

Point it at a service with `VITE_BACKEND_URL` (default `http://localhost:8080`). To see it with data, run the service with `API_KEY=dev-key ALLOW_UNSIGNED_WEBHOOKS=true CORS_ALLOWED_ORIGINS=http://localhost:3000`, post a few events to `/webhooks/github`, then enter `dev-key` in Connection settings.

## Known limits

- Only the newest 200 events load; there is no pagination or search yet.
- Polling every 15 seconds, not push.
- Feedback is accepted but nothing consumes it yet (architecture section 3).
- Component rendering is not unit-tested (no DOM test runner); behaviour is covered at the `lib/` level and was checked in a browser at desktop and mobile widths.
