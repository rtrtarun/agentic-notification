# Milestone 30: Strict rules, payload predicates and per-rule channels (part 1 of 2)

**Status: part 1 is done. Part 2 (per-person preferences, overrides and digests) is not started and is waiting on a design decision; see "Not done" below.**

## Goals
- A misspelt key in a rules file must fail at load time instead of quietly widening a rule.
- Rules can match on the contents of an event, not just source, type and repository: exact matches and numeric thresholds such as `metadata.attempt > 3` or `metadata.confidence < 0.7`.
- A rule can send its events to dedicated channels instead of the class route.

## Architecture decisions
- **Strict decoding.** `ParseRules` decodes with `KnownFields(true)`, so `eventTpye`, `attentionClas`, `channel` and any other unknown key is an error that names the key and line. The shipped `config/rules.yaml` is loaded by a test under strict decoding.
- **Predicates** (`pkg/policy/predicate.go`). `RuleCondition.PayloadMatch` is a list of `{field, op, value}`, all of which must hold, up to 5 per rule. Operators: `eq` (default), `ne`, `gt`, `gte`, `lt`, `lte`, `exists` (value `true` or `false`). A field is a dotted path into the payload (`metadata.attempt`, `pull_request.draft`) or one of three normalised names that look where each source keeps them: `branch` (`branch`, `pull_request.head.ref`, or `ref` without `refs/heads/`), `author` (`author`, `sender.login`, `pusher.name`, `metadata.author`) and `severity` (`severity`, `metadata.severity`, `alert.severity`, `security_advisory.severity`). String comparison ignores case; numeric operators also accept numeric strings, because many senders put numbers in strings. **A field the event lacks never matches**, except `exists` with `false`, so a missing value can never accidentally fire a rule.
- **Validated when saved**, in the rules API and in the file loader: field names, operator names, numeric thresholds, value types and lengths, and the 5-predicate cap. A typo fails when the rule is saved, never silently at match time.
- **Per-rule channels.** `then.channels` (`email`, `slack`, `teams`, `sms`) replaces the class route for events that rule matches. The outbox uses it for queueing, and `RoutedFor` counts it as a route (so a rule's own channel makes an event "routed" even when its class has no route, and the attention budget treats it as an interruption). A channel with no configured target is skipped. `policy.KnownChannels` mirrors the delivery channels because policy cannot import delivery, and a test fails if the two lists ever differ.
- **Critical events cannot be narrowed.** A critical rule may not carry `channels` (the API and the loader refuse it), the engine drops the field for a critical rule even if it is set directly, and critical events are delivered inline to every configured channel and never queued. Tests cover each.
- **The worker honours rules too.** `ProcessPending` and `ReleaseDelayed` read the current rule (`ruleNow`) for adaptiveness and channels, so a deferred event is routed exactly as an inline one would be.
- **Dry runs can take a payload.** `POST /api/rules/test` accepts an optional `payload` and returns the winning rule's `channels`, so predicates can be tried before they matter.

## Database changes
None. Rules are stored as JSON, so the new fields travel in the rule body.

## APIs
`POST /api/rules` and `PUT /api/rules/{id}` accept `when.payloadMatch` and `then.channels` and answer 400 with a plain message for an invalid one. `POST /api/rules/test` accepts `payload` and returns `channels`. Existing rules are unaffected.

## Folder changes
`pkg/policy` (`predicate.go`, loader, engine, model), `pkg/rules`, `pkg/delivery` (`Message.Channels`, `RoutedFor`), `pkg/event` (transient `RouteChannels`), `pkg/pipeline`.

## Risks
| Risk | Handling |
|---|---|
| Strict YAML breaks an existing rules file that had a stray key | That is the point; the error names the key. The shipped file is tested. Operators with custom files should run the service once in a test environment before upgrading |
| A predicate matching on a field one source never sends | A missing field never matches, so the rule is simply inert for that source |
| A rule used to redirect critical alerts away from channels | Impossible: critical rules cannot carry channels, and the engine ignores them if they somehow do |

## Tests
`pkg/policy` (92.4 percent): every operator including missing fields, numeric strings, non-numbers and unknown operators; the three normalised field names and their fallbacks; predicates combined with the other fields; validation (14 invalid shapes) and channel validation; channels returned except for critical rules and not shareable with callers; strict YAML (six misspelt-key cases), bad predicates and channels name the rule, empty files, the shipped rules file. `pkg/rules`: tidied on save and effective at once, invalid ones refused, dry run with a payload. `pkg/delivery`: override replaces the route, unconfigured channel skipped, critical never queued, `RoutedFor`, policy and delivery channel lists identical. `pkg/pipeline` (95.7 percent): matching event goes only to its rule's channel and others use the class route, a critical rule cannot narrow delivery, the worker routes the same way, a rule's channel counts as a route. `pkg/server`: create, reorder and deliver over HTTP, dry run with a payload, refusals.

## Acceptance criteria
- [x] `ParseRules` uses strict decoding and rejects misspelt keys.
- [x] `RuleCondition.PayloadMatch` with exact and numeric predicates on payload and `metadata.*` fields.
- [x] Per-rule `channels` override that cannot apply to critical rules.
- [x] `gofmt`, `go vet`, `go test ./...` pass locally.
- [ ] **Per-person preferences, overrides and digests: not done.**

## Not done (and why)
Part 2 asked for preferences, overrides and digests scoped to `user_id`, with per-person digests on a schedule or on request. It was not started because it rests on a decision the spec does not make:

- **Events have no recipient.** Every event, rule, override and delivery target is workspace-wide. "A person's digest" needs a definition: is it the workspace digest filtered by that person's own mutes and overrides? Or events addressed to them? The answer changes the data model.
- **Delivery targets are configuration, not users.** Jobs deliberately store no address. Sending a digest to a person means resolving their email at send time, which is a change to the delivery design and to the rule that no target reaches the database.
- **Scheduling per person** needs each person's time zone and quiet hours.

A proposal for the design side: (1) per-user preferences stored by `user_id` with workspace fallback; (2) a per-person digest is the workspace digest filtered by that person's mutes and "never" choices, stored and readable in the app, generated on request first and on a schedule later; (3) per-person email delivery is a separate milestone that adds recipient resolution to delivery. This is recorded as an open question in `docs/implementation-sync.md`.

## Known limits
- The Rules screen in the console does not yet have fields for predicates or channels; they can be set through the API and the YAML file, and they show in the rule JSON.
- Predicates read the stored payload as received, so agent telemetry gets first-class fields only in Milestone 31.
