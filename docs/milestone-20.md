# Milestone 20: Preferences and real-time updates

## Goals
Let people say when not to be interrupted, and make the console update the moment something happens instead of on a timer.

## Architecture decisions
- **Preferences** (`pkg/preferences`): quiet hours (any IANA zone, may cross midnight) and muted sources and repositories (`*` wildcards). One row for the workspace (there is no per-user model until authentication has users). Stored in PostgreSQL or memory; cached in the service and swapped atomically.
- **The critical guard is structural.** `Decide` returns no adjustment for a critical event, the pipeline only calls it from the non-critical branch, and there are tests at both levels, including a run with every mute and a 24-hour quiet window set.
- **What preferences do today, honestly:** a muted event is kept as `background` with status `digest`; during quiet hours a standard event gets status `delayed` and the reason names when the window ends. Standard events are not pushed to channels yet (only critical ones are), so quiet hours changes the recorded outcome and reason now, and will gate delivery when digests (AI layer) and scheduled release arrive.
- **Live updates** (`pkg/stream`): an in-process broker with a connection cap and a never-blocking publish. The pipeline publishes after each saved event; inbox changes and preference changes publish `inbox`. Redelivered webhooks publish nothing.
- **SSE over `fetch`:** `EventSource` cannot send the `X-API-Key` header and the key must not be in a URL, so `lib/live.ts` reads the stream with `fetch` and parses it (`createSseParser`, tested with one-character chunks). It reconnects with backoff (1 s up to 30 s), and a 401 or 503 starts at the slow end so a wrong key is not hammered.
- The stream route skips the request timeout and sets its own write deadline per message; `statusRecorder` gained `Unwrap` so flushing works through the logging middleware. On shutdown every stream is closed first, otherwise graceful shutdown would wait for them.
- Polling (15 s) stays as the fallback: a dropped connection costs freshness, not correctness. Bursts of messages cause one refresh (300 ms).
- Fixed on the way: CORS now allows `PUT`, which the rules and preferences edits need from a different origin (milestone 18 only worked same-origin).

## Database (idempotent)
`preferences(id INT PRIMARY KEY CHECK (id = 1), body JSONB, updated_at)`.

## API
| Method and path | Purpose |
|---|---|
| `GET /api/preferences` | current preferences (defaults before the first save) |
| `PUT /api/preferences` | replace them; 400 with a readable message for a bad zone, time, pattern or size |
| `GET /api/stream` | `text/event-stream`: `ready`, `event`, `inbox`; 503 with `Retry-After` at the connection cap |

## Frontend
`#/preferences`: quiet hours with a validated time range, zone picker and "use my zone", chips for muted sources and repositories, unsaved-changes handling (a refresh from another console does not overwrite your edits), and a standing note that critical is exempt. A **Live** indicator in the top bar shows the stream state (hidden in demo mode and on small screens).

## Risks
- The broker is in-process: with several replicas a console only hears about events handled by the replica it is connected to; polling covers the rest. A NATS-backed broker is the production answer (milestone 21 notes).
- One workspace-wide set of preferences until users exist.
- Quiet hours have little visible effect until non-critical delivery exists (see above).

## Tests
Go: `pkg/preferences` (windows across midnight and within a day, zones, validation, tidying, critical exemption, muting, quiet hours scope, service persistence), `pkg/stream` (fan-out, close, cap, slow subscriber never blocks and keeps the newest, concurrency), `pkg/pipeline` (critical untouched, mute, quiet hours, announcements, no announce on redelivery), `pkg/server` (get/put/validation, auth, CORS PUT, live stream ready and messages, inbox changes announced, key required, connection cap, shutdown ends streams). Web: `lib/live.test.ts` (parser, backoff, connect), `lib/preferences.test.ts`. 176 Vitest tests; Go module passes. Live check against a running service: with `github` muted, a push became `background/digest`, a security advisory stayed `critical/sent`, and the stream announced both.

## Acceptance criteria
- [x] Quiet hours and mutes can be set and take effect on the next event.
- [x] Critical events are never changed by preferences.
- [x] The console updates without waiting for the poll, and recovers from a dropped connection.
