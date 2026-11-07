# Milestone 16: All Notifications

The nav map promised an **All Notifications** screen in milestone 16. The roadmap file names no milestones after 15, so milestones 16 to 21 follow the `plannedIn` numbers in `apps/web/src/lib/nav.ts`.

## Goals
A dense, sortable, filterable table of every notification, for people who want to audit rather than triage.

## Architecture decisions
- **No backend change.** `GET /api/notifications` already filters by `source`, `repo`, `from`, `to`, priority and outcome, and pages up to 100 rows. The screen is a second view over the same service, so the inbox and the table can never disagree.
- The table has its own filter state (`lib/allQuery.ts`); the inbox tabs are untouched.
- Dates are local days. `to` is exclusive on the service, so "to 5 June" is sent as the start of 6 June.
- CSV export covers the current page only and prefixes cells that start with `= + - @` so a spreadsheet cannot run them as formulas.
- Sorting is by column header (priority, received) and exposes `aria-sort`.
- `InboxQuery` gained `source`, `repository` and `to`; the demo client honours them.

## Database / API
None.

## Folder changes
`apps/web/src/lib/allQuery.ts`, `pages/AllPage.tsx`, `features/manage/` (shared management-screen styles and the `useLoad` data hook used by milestones 17 to 21).

## Language policy change
Screens from milestone 16 are written in English first. `i18n.test.ts` still requires full translation of everything older and now exempts the new namespaces (`manage.`, `all.`, `ov.`, `ana.`, `rules.`, `pol.`, `ch.`, `int.`, `pref.`, `help.`, `live.`); other languages show English for missing keys. Non-English catalogues are typed `Partial<Messages>`; an unknown key in any catalogue still fails the test.

## Risks
- Not yet checked visually in a browser.
- The service lists the newest 1000 events only (milestone 15 limit).

## Tests
`lib/allQuery.test.ts`: day boundaries, month rollover, invalid dates, range error, query mapping, sort cycle, CSV quoting and formula defusing. 134 Vitest tests in total; `tsc -b` and the build pass.

## Acceptance criteria
- [x] `#/all` renders a table with filters, sort, paging and a drawer.
- [x] Filters combine and clear.
- [x] Export works and is injection safe.
