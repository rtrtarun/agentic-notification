# Milestone 25: Attention Intelligence (classification, summaries, digests, explanations)

## Goals
- Layer 2 exists: a place where *judgement* about a notification, as opposed to a rule, lives, and nowhere else.
- It classifies standard and background events, summarises groups, writes digests, schedules them and explains decisions in plain language.
- The service works completely without a model. The default is a deterministic, explainable heuristic. Claude API or Ollama can be added later behind the same interfaces.

## Architecture decisions
- **Interfaces first.** `Classifier`, `Summarizer` and `Explainer` in `pkg/attention`; `Heuristic` implements all three. A model adapter later only has to implement them.
- **The three promises are enforced in code, not by convention.**
  1. *A critical event never reaches it:* `Guarded` refuses critical events before the wrapped classifier is called, the pipeline never classifies one, the digest generator excludes them, and the summariser skips them. A critical rule cannot be marked adaptive (the rules API rejects it and the engine ignores the flag).
  2. *It can only move an event between standard and background:* `Guarded` rejects any answer that is not one of those two, or whose score or confidence is out of range; the pipeline only ever lowers standard to background.
  3. *The service works without it:* `ATTENTION_CLASSIFY=false` turns classification off and rules alone decide.
- **Rules opt in.** A rule can carry `adaptive: true` ("let Attention refine this"). An explicit rule without it is never second-guessed; an event no rule decided is adaptive. The built-in `default-github` rule is adaptive. Locked and critical rules cannot be.
- **A person's choice outranks judgement.** If an always/never override covers an event, the classifier is not asked.
- **The heuristic is arithmetic with reasons.** Base 50; social activity (star, watch, fork) −35, routine changes (labelled, synchronised, assigned) −20, automated sender −25, draft pull request −20; a review request +25, requested changes +20, a failed check +30, something opened +10, a person commenting +10. Below 35 is background. Every decision records the model (`heuristic-v1`), the confidence and the reasons ("default routing; attention scored 15: social activity"), so the Why panel and the audit trail show it.
- **Background means the digest, unless something is routed.** A background event with no channel configured for background is held for the digest (outcome `digest`); with `NOTIFY_BACKGROUND_CHANNELS` set it is delivered as before.
- **Digests** cover everything with outcome `digest` or `delayed` since the previous digest (never critical, never merged repeats), grouped by repository and source, with up to three "look at first" highlights chosen by the classifier's score. At most 500 events per digest; the rest go in the next. A digest is stored, optionally queued for delivery through the same outbox and dispatcher as everything else (`NOTIFY_DIGEST_CHANNELS`), and made on a schedule (`ATTENTION_DIGEST_EVERY`) or on request by an admin.
- **Explanations** are templated sentences built from the recorded decision; they say honestly when Attention Intelligence was *not* involved.

## Database changes
New table `digests` (id, window end, JSON body). Rules gain an `adaptive` field in their stored JSON (no migration needed).

## APIs
| Route | Purpose |
|---|---|
| `GET /api/digests?limit=` | Newest first, and how often one is made on its own |
| `GET /api/digests/{id}` | One digest |
| `POST /api/digests/generate` | Admin only. Makes a digest now; `generated:false` when nothing was held back |
| `GET /api/explain/{eventId}` | Plain-language reason for one event's handling |
| rules API | `then.adaptive` is accepted; a critical rule with it is a 400 |

Configuration: `ATTENTION_CLASSIFY` (default `true`), `ATTENTION_DIGEST_EVERY` (default `0`, only on request; for example `24h`), `NOTIFY_DIGEST_CHANNELS` (comma list of channels; empty means stored only).

## Folder changes
New `pkg/attention` (`attention.go`, `heuristic.go`, `digest.go`, `postgres.go`); `pkg/policy` and `pkg/rules` gain `adaptive`; `pkg/pipeline` gains `WithClassifier`; `pkg/server/attention_handlers.go`. Web: Digests screen, an "Explain in words" button in the notification drawer, an "adaptive" switch in the rule form.

## Risks and how they are handled
| Risk | Handling |
|---|---|
| A model (later) hides something important | Guard, adaptive opt-in per rule, critical immunity, overrides outrank it, digest keeps everything readable |
| Behaviour change for existing deployments | Only events matched by an adaptive rule or by no rule are refined; set `ATTENTION_CLASSIFY=false` to turn it off. Background events with no route now show as "digest" instead of "queued" |
| Digest too big or too slow | 500-event cap; reads the newest 1000 events |
| Noisy heuristics | Every point has a reason; rules and overrides always win; weights are one small table in `heuristic.go` |

## Tests
`pkg/attention` (classification table, determinism, critical refusal, guard against escalation and nonsense, summaries, explanations including honesty about AI, digest selection, windows, bounds, delivery failure tolerated, highlights, scheduler); `pkg/pipeline` (adaptive rule, non-adaptive rule, unmatched event, **critical never reaches the classifier**, escalation attempts, overrides outrank it, background routing); `pkg/server` (events held then digested and delivered, access rules, validation, noise moved with the reason, switch off, explain, critical rule cannot be adaptive). Web: `lib/digests.test.ts`. 211 Vitest tests.

## Acceptance criteria
- [x] With no model configured, digests are generated and explanations work.
- [x] A critical event never reaches any Layer 2 interface (asserted with a spy that fails the test if called).
- [x] A model that tries to escalate, or returns nonsense, changes nothing.
- [x] Explicit rules and people's overrides are never second-guessed.

## Known limits (honest list)
- **No language model exists yet.** "Attention Intelligence" today is the heuristic. It is deterministic and honest about that, but it does not understand text. Claude API and Ollama adapters arrived in milestone 26 behind the same interfaces.
- **The heuristic is tuned for GitHub events.** Other sources get the base score unless their payload has a `sender` and `action`.
- **Digests read the newest 1000 events** and are workspace-wide.
- **Timing is an interval, not a time of day:** `ATTENTION_DIGEST_EVERY=24h` runs 24 hours after start, not at 08:00. A time-of-day schedule and per-person digests need per-person delivery.
- **The PostgreSQL digest store was not run against a real database.**
- Quiet-hours "delayed" events are now picked up by the next digest instead of being released when quiet hours end.
- New text is English only.

## Review checklist
- [ ] Send a few stars and a bot-authored pull request, then open Digests and make one; send a review request and a security advisory and confirm the advisory is not in it.
- [ ] Open a held notification and press "Explain in words".
- [ ] Edit a rule: the "Let Attention refine this" switch is absent for critical rules.
- [ ] Set `ATTENTION_CLASSIFY=false`: the same bot pull request stays standard.
- [ ] Set `NOTIFY_DIGEST_CHANNELS=sms` (mock gateway) and check the digest appears under Channels > Deliveries.
