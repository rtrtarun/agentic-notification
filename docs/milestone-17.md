# Milestone 17: Overview, Activity and Analytics

## Goals
Answer three questions without opening the inbox: what needs me now (Overview), what happened and who did what (Activity), and is the platform actually protecting attention (Analytics).

## Architecture decisions
- New package `go-server/notification-service/pkg/insights`. It is read-only and depends on `inbox.Service` through a small `Source` interface (`Items`, `RecentActions`), so the maths (`Analyze`, `BuildActivity`) is pure and tested without a store. See its [README](../go-server/notification-service/pkg/insights/README.md).
- **No new table.** Analytics are computed from the events and inbox state already stored, over the newest 1000 events (the existing list limit). That limit is stated on screen.
- `inbox.Repository` gained `RecentTrail(ctx, limit)` (memory and PostgreSQL) so the activity feed can include user actions across all notifications. `inbox.Service` gained `Items` and `RecentActions`.
- **Held back** is `events - delivered`: everything that did not interrupt a person. **Median time to read** uses `readAt - createdAt`. **Open critical** is critical and neither acknowledged nor resolved.
- The web client mirrors `Analyze` and `BuildActivity` in TypeScript for demo mode, with tests pinning the same figures as the Go tests.
- Charts are plain CSS: every bar and column carries its number as text or an `aria-label`, so colour is never the only signal.
- Metrics labels for `/api/<collection>/...` collapse to the collection (`routeLabel`), ready for later milestones.

## API
All need the API key.

| Method and path | Result |
|---|---|
| `GET /api/analytics?range=24h\|7d\|30d` (default `7d`) | `range, from, to, bucketSeconds, totals, attention, series[], topSources[], topRepositories[], topEvents[]`. 24h uses hourly buckets, 7d and 30d daily. Invalid range: 400. |
| `GET /api/activity?limit=1..200` (default 50) | `{entries: [{at, kind: received\|action, step, detail, eventId, source, eventType, repository, priority, outcome, reason}]}` newest first. |

## Database
None.

## Folder changes
`pkg/insights/`, `pkg/server/insights_handlers.go`; web: `lib/insights.ts`, `features/manage/{charts.tsx,useInsights.ts}`, `pages/{Overview,Activity,Analytics}Page.tsx`.

## Risks
- Computed on every request over up to 1000 events. Fine for the target scale; cache if it ever shows in metrics.
- Not yet checked visually in a browser.

## Tests
Go: `pkg/insights` (range parsing, counts, buckets summing to the total, empty history, median, top-list ordering, activity merge and limit, error propagation) and `pkg/server` handler tests (totals, defaults, validation, auth, missing service, route labels). Web: `lib/insights.test.ts` (11 tests). 145 Vitest tests; `go test ./...` passes.

## Acceptance criteria
- [x] Overview, Activity and Analytics render from a real service and in demo mode.
- [x] Numbers on screen equal the service's numbers (same rules, tested both sides).
- [x] No invented data; empty states say so.
