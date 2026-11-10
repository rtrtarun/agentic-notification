# Milestone 37: Java parity part 3

Rule `payloadMatch` and per-rule `channels`, feedback with always/never overrides, and heuristic Attention Intelligence (classifier, explanations, digests, attention budget) in the Java service. Plan: [milestone-37-plan.md](milestone-37-plan.md). Built in three slices, each committed with its own shared contract file that was written and run against the Go service first.

## 1. Goals (met)
- 37a: Java accepts, stores and enforces `payloadMatch` and `channels`; it no longer refuses rules Go accepts.
- 37b: `POST /feedback` and `/api/overrides` work as in Go; an override outranks judgement, quiet hours and the budget for non-critical events.
- 37c: the built-in heuristic classifies, explains and summarises; digests are made and stored; the budget holds over-budget standard events for the next digest.
- Critical events are untouched: never classified, never counted, never muted, never in a digest, never narrowed to chosen channels.

## 2. Architecture decisions
- Plain JDK, Jackson, HikariCP, Jedis. No Spring, no JSONPath library (a different path language would make Java match differently from Go).
- Each class mirrors a Go package: `Predicates` (`policy/predicate.go`), `Overrides` and `Feedback` (`pkg/overrides`, `pkg/feedback`), `Heuristic`, `Digests`, `Budgets` (`pkg/attention`, `pkg/budget`), with Go's messages, JSON and Redis key and script.
- A rule's channels travel on the event (`routeChannels`, never stored) so the queue routes by them; a critical decision never carries any.
- `GuardedClassifier` enforces "only standard or background, never critical" in code for any classifier.
- Digests are stored and readable; they are not handed to the delivery queue (decided: store only). Personal digests and language models are refused loudly, not ignored.
- New settings: `ATTENTION_CLASSIFY` (default true), `ATTENTION_BUDGET_PER_HOUR` (default 10, 0 = off), `ATTENTION_DIGEST_EVERY` (default 0), `OVERRIDES_ADMIN_ONLY`, and a start-up refusal for `ATTENTION_PROVIDER` other than `heuristic`. `DISPATCHER_ENABLED` is separate (branch `feature/dispatcher-flag`).

## 3. Database changes
None. Tables `overrides`, `feedback`, `digests` and the rule and queue tables are Go-owned and migrated by Go.

## 4. APIs
`/api/rules` (with `payloadMatch`, `channels`; the dry run takes a sample `payload` and returns `channels`), `POST /feedback`, `GET`/`POST /api/overrides`, `DELETE /api/overrides/{id}`, `GET /api/digests`, `GET /api/digests/{id}`, `POST /api/digests/generate` (admin), `GET /api/explain/{eventId}`, `GET /api/attention/status` (admin).

## 5. Folder changes
Java: `Predicate`, `Predicates`, `Overrides`, `Feedback`, `Heuristic`, `GuardedClassifier`, `Budgets`, `Digests`, `FeedbackStores`, `DigestStores`, `FeedbackApi`, `AttentionApi`; changes to `Rule`, `PolicyEngine`, `Event`, `Pipeline`, `QueueOutbox`, `RulesLoader`, `RulesService`, `RulesApi`, `Config`, `JedisKeyStore`, `HttpApi`, `Main`. Contract files 11, 12 and 13.

## 6. Risks and how they were handled
- Rule widening: shared fixtures, Go's validation messages, misspelt keys still refused.
- Feedback or an override suppressing a critical event: `never` on one is refused, `Overrides.decide` never answers for one, invariant tests cover both.
- Heuristic drift: contract file 13 pins score, reason, group order, highlights and explanation text to Go's output.
- Two services on one database: they share overrides, feedback, digests and (through Redis) the budget. Not shared: the overrides cache (per process, reaches the other service on restart). Only one should schedule digests (`ATTENTION_DIGEST_EVERY=0` on the other).
- Defaults changed for Java: judgement and a budget of 10 per hour are now on, as in Go. Set `ATTENTION_CLASSIFY=false` or `ATTENTION_BUDGET_PER_HOUR=0` to restore the earlier Java behaviour.

## 7. Tests
- Contract: files 11, 12 and 13 pass in Go (`TestContract`) and Java (`ContractTest`); 13 files in all.
- Java unit and API tests added: `PredicatesTest`, `FeedbackTest`, `FeedbackApiTest`, `HeuristicTest`, `DigestsTest`, `AttentionPipelineTest`, `AttentionApiTest`, queue, pipeline, loader and config tests; Postgres tests `PostgresFeedbackStoresTest`, `PostgresDigestStoresTest` (including a digest written the way Go writes one); Redis tests for the sliding window and a budget shared by two replicas.
- Invariant tests: 9 before this milestone, 12 now (a critical rule never narrows channels, an override never touches critical, feedback cannot mute critical, the real heuristic never judges critical and a digest never contains it).
- Totals: 251 tests offline (5 skipped), 272 with PostgreSQL and Redis, none skipped; offline line coverage about 92 percent (gate 80). Go: `TestContract` passes; no Go code changed in this milestone.
- Real stack (PostgreSQL 16, Redis 7, the Go binary and the Java jar on one database): overrides made by one service are listed and applied by the other with the same reason; Go undid an override Java made; Go and Java counted one budget (events 1 and 2 queued across both, 3 and 4 held); a digest made by Java was listed by Go and Go then made none; Java explained a Go-made event; a critical event stayed critical and was sent.

## 8. Acceptance criteria
- [x] A rule with `payloadMatch` and `channels` created through Java matches the same events as in Go.
- [x] Feedback and overrides made through one service are honoured by the other.
- [x] The same event gets the same heuristic score, reason, digest and explanation in both services.
- [x] All contract files pass in Go and Java; `mvn verify` passes offline and with PostgreSQL and Redis.
- [x] No critical-path regression.
- [ ] CI green on the branch (not yet seen).

## Not in Java yet
SMS, Novu, single sign-on, language-model Attention Intelligence, personal digests and preferences, digest delivery, learning, traces, MCP, metrics, inbox, channels API, event retention.

## Review checklist
- Read `Overrides.decide`, `Pipeline.applyPreferences` and `GuardedClassifier` for the critical-event guarantees.
- Run `docs/contract` against both services and the invariant tests.
- Decide whether to merge `feature/dispatcher-flag` first (separate branch).
