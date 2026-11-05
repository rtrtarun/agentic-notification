# Milestone 21: Help and Support, and wrap-up of milestones 15 to 21

## Goals
Close the console build-out: the last placeholder screen becomes real, and the whole set is documented in one place.

## Help and Support (`#/help`)
- **Get started** steps linking to Integrations, Rules and Channels.
- **What the words mean:** classes, outcomes and the High level, reusing the same text the inbox legend uses.
- **Keyboard:** the command palette shortcut (shown as Ctrl K or Cmd K for the platform), Escape, list navigation, Tab.
- **Questions people ask:** why is this critical, why was I not notified, can I mute critical (no), what held means, is AI deciding (no), where is my data.
- **Diagnostics:** calls the unauthenticated `/health` endpoint (never sends the API key), shows the result, and offers a plain-text block to paste into a support request: console version, time, mode, service **origin only** (paths, credentials and queries dropped), whether a key was entered (never the key), health with round trip, live-update state, unread count, language, browser. Pure functions (`lib/diagnostics.ts`) are tested, including that the key and address path never appear.
- The console version comes from `package.json` at build time (`__APP_VERSION__`, via `vite.config.ts`).

## Decisions
- No backend change. Help needs nothing the service does not already expose.
- Help text is English first, like every screen since milestone 16.
- Every navigation entry is now a real screen; `PlannedPage` stays in the code for the next unbuilt screen.

## Tests
`lib/diagnostics.test.ts` (health outcomes, no network in demo mode, key never sent, origin-only addresses, formatted block). 185 Vitest tests in total; `tsc -b` and the build pass; `go test ./...` and `go vet ./...` pass.

## Summary of milestones 15 to 21

| # | What shipped | Doc |
|---|---|---|
| 15 | Routed shell, Inbox v2, detail drawer, dashboard, nine languages | [milestone-15.md](milestone-15.md) |
| 16 | All Notifications table, filters, CSV | [milestone-16.md](milestone-16.md) |
| 17 | Overview, Activity, Analytics | [milestone-17.md](milestone-17.md) |
| 18 | Editable rules, locked critical rules, dry run, Policies | [milestone-18.md](milestone-18.md) |
| 19 | Channels and Integrations, masked targets, safe test sends | [milestone-19.md](milestone-19.md) |
| 20 | Preferences, quiet hours, mutes, live updates over SSE | [milestone-20.md](milestone-20.md) |
| 21 | Help and Support, diagnostics, documentation | this file |

## Known limits (honest list)
- **Visual checks were partial.** Headless Edge screenshots of All, Analytics, Rules and Channels at desktop width were reviewed. They exposed a stray list indent, mid-word wrapping in card values and a wrapping stat value, which were fixed in CSS afterwards **without a second look**. Overview, Activity, Policies, Integrations, Preferences and Help, the narrow layouts and right-to-left were not reviewed visually. Nothing was tested on a physical phone or with a screen reader.
- **Translations:** screens from 16 on exist in English only; other languages fall back to English per key (a documented, tested policy). Native review is still needed for the nine-language catalogue.
- **Preferences are workspace-wide**, and quiet hours only change recorded outcomes until non-critical delivery exists.
- **Channels are configured by environment variables.** Storing credentials in the database needs encryption at rest and is left for production hardening.
- **The stream broker is in process.** Multi-replica fan-out should move to NATS.
- **Analytics, activity and the inbox read the newest 1000 events.**
- **Not built from the original plan (milestones 5 to 10 of AGENTS.md):** the Novu provider, the AI layer (classification, summaries, digests), the feedback learning engine and authentication. These need decisions (providers, models, user model) that the console work did not require.
- `go test -race` could not run on this machine (no cgo); CI runs it.

## Review checklist
- [ ] Open each of the 12 screens in demo mode and with a live service.
- [ ] Try to delete or edit a locked rule, mute `github` and send a security advisory: it must still arrive as critical and delivered.
- [ ] Send a channel test twice within ten seconds (the second is refused).
- [ ] Stop the service while the console is open: the Live indicator moves to Reconnecting and recovers on restart.
- [ ] Look at every new screen at 360, 768 and 1280 px, in a right-to-left language and in light mode.
