# Milestone 30: Strict rules, payload predicates, per-rule channels and per-person digests

**Status: both parts are done, with part 2 built as the proposal below (approved by the project owner with "sure do everything"). Per-person email delivery and per-person schedules are deliberately not included.**

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
- [x] Per-person preferences and digests (built as filtered views; no per-person delivery or schedule).

## Part 2: per-person preferences and digests

Events, rules and delivery targets are workspace-wide and jobs store no address, so "a person's digest" needed a definition. The model built:

- **Per-user preferences with a workspace fallback.** Table `user_preferences` (user ID, JSON body). `GET /api/me/preferences` returns the person's own, or the workspace's flagged `personal:false`; `PUT` saves their own after the same validation as the workspace's. They shape only that person's digest; the pipeline and everyone else are unaffected.
- **A personal digest is the workspace digest filtered for that person.** `POST /api/digests/generate?scope=me` (any signed-in person) covers what was held since *that person's* previous digest, minus events matched by their muted sources and repositories and by the "never notify" choices they made themselves (`overrides.NeverFor`, which only looks at their own choices). `GET /api/digests?scope=me` lists their own. Digests carry `userId`; the workspace list never includes personal ones, and the workspace digest is unchanged, still admin only and still delivered.
- **Not delivered.** A personal digest is stored and readable in the app only; a message to one person would need their address resolved at send time, which is a separate change to delivery.
- **On request only for now.** There is no per-person schedule: each person would need their own time zone and quiet-hours handling in a scheduler.
- **Critical events are never in a digest** (held events exclude them), so a personal filter cannot hide one, and a mute never applies to one (`Preferences.Muted` returns false for critical). A service holding only the API key is not a person: personal views answer 400 and say to sign in.
- Budgets keep the `workspace` scope; the `attention:budget:<scope>` key is ready for `user:<id>` when per-person delivery exists.

Database: new table `user_preferences`. Stores: `DigestStore.ListFor`, `preferences.PersonalStore`, each with a contract test on memory and PostgreSQL. Tests: personal preferences fall back and never leak to others, validation, a digest omits what a person does not want, windows are independent per person and for the workspace, personal digests are not delivered, one person's "never" does not change another's digest, a critical event is never hidden or included, and the access and validation table.

## Known limits
- The console has no screens yet for predicates, channels, personal preferences or "my digest"; they are API-only. The Rules screen does not yet have fields for predicates or channels; they can be set through the API and the YAML file, and they show in the rule JSON.
- Predicates read the stored payload as received, so agent telemetry gets first-class fields only in Milestone 31.
