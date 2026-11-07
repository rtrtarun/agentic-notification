# Milestone 35: Java parity, part 2 (`/notify`, rules API, durable delivery, Redis dedup)

The Java service now covers the generic agent entry point, run-time rules, the durable delivery queue and cross-replica deduplication, on the same database and Redis as the Go service. Accounts, Attention Intelligence, feedback, learning, traces and MCP are still Go only (milestone 36 onward).

Scope and decisions are the owner's, recorded in [implementation-sync.md](../implementation-sync.md) section 10.

## 1. Goals
1. `POST /notify` with the Go service's validation: API key, SSRF host allow-list, one-line title, size limits, no caller-chosen SMS numbers.
2. Rules API (`GET`/`POST`/`PUT`/`DELETE /api/rules`, `reorder`, dry run `test`) and `GET /api/policies`, stored in the `policy_rules` table the Go service also uses, reloadable at run time.
3. A durable PostgreSQL delivery queue for standard and background events: retry with exponential backoff, dead letters, manual retry, leases that survive a crashed worker, several workers without double sends.
4. `RedisDeduper` so replicas (and the two services) share the dedup window, failing open when Redis is down.
5. The shared contract grows to cover all of it; both services pass the same files.
6. The sacred invariant still holds, with new tests for the queue, Redis and `/notify`.
7. Local `mvn verify` passes with no database (owner decision): PostgreSQL classes leave the coverage gate and are verified by integration tests in CI.

## 2. Architecture decisions
- **Same layers.** The queue, outbox and dispatcher are in `application` (they know `DeliveryStore`, not SQL); the stores and Redis adapter are in `infrastructure`; handlers in `interfaces` only decode, call one service and encode.
- **One provider shape.** `NotificationProvider.send(Notification)` carries its own targets. `/notify` supplies the caller's validated targets, critical events and the queue supply the configured ones. Email and webhook providers each do nothing when the notification names no target for their channel.
- **A critical event still never touches the queue.** It is delivered inline before it is stored. The only thing it does with the outbox is `recordDirect`, a log row next to the queued ones (as in Go); the log row is never claimable and its failure never fails the event.
- **Redis is a `KeyStore`.** `SET NX PX`, `GET`, and a delete-if-equal script, the same keys (`dedup:` + the dedup key) as Go, so an event seen by one service is a duplicate for the other. Check failures raise; the pipeline treats the event as unique and still gives it its key and thread (so a stored event is threaded even when Redis is down). Start-up pings Redis and refuses to start if it is unreachable, as Go does.
- **Rules.** Validation messages are Go's. Locked rules always run first and cannot be changed. IDs are random. `payloadMatch` and per-rule `channels` are refused (400 on the API; a loud start-up error if a stored rule already uses them), never dropped, because dropping a condition would widen the rule.
- **The same table, two writers.** Both services seed and edit `policy_rules` in one transaction. Only the service that changed the rules reloads its own engine immediately (as with Go replicas).

## 3. Database changes
None. Java uses `policy_rules` and `delivery_jobs` as the Go migrations define them.

## 4. APIs
New on Java, identical to Go: `POST /notify`, `GET`/`POST /api/rules`, `POST /api/rules/reorder`, `POST /api/rules/test`, `PUT`/`DELETE /api/rules/{id}`, `GET /api/policies`, `GET /api/deliveries`, `POST /api/deliveries/{id}/retry`. New settings: `REDIS_URL`, `NOTIFY_ALLOWED_WEBHOOK_HOSTS`, `NOTIFY_ALLOWED_EMAIL_DOMAINS`, `NOTIFY_STANDARD_CHANNELS`, `NOTIFY_BACKGROUND_CHANNELS`, `TEAMS_WEBHOOK_URL`, `EVENT_RETENTION` (shown by `/api/policies` only).

## 5. Folder changes
`application/` gains `Notify`, `RulesService`, `PolicySummary`, `Delivery`, `QueueOutbox`, `Dispatcher`, `Targets`; `infrastructure/` gains `PostgresRuleStore`, `PostgresDeliveryStore`, `MemoryStores`, `RedisDeduper`, `JedisKeyStore`; `interfaces/` gains `ApiSupport`, `NotifyApi`, `RulesApi`, `DeliveriesApi`. `docs/contract/` gains `07-notify`, `08-rules`, `09-deliveries`. CI's `java-service` job also starts Redis. Compose gives the Java service `REDIS_URL`.

## 6. Risks and behaviours to know
- **Two services on one database must be configured alike.** Both run a dispatcher against `delivery_jobs`, and a job goes to whichever claims it first. Found in the end-to-end check: a standard event Java routed to Slack was claimed by the Go service, which had no Slack channel, and dead-lettered it ("channel is not configured"). Run one implementation per database, or give both the same delivery settings.
- **A critical delivery now fails if any channel fails** (the failures are joined), as in Go. Milestone 34 only failed when every channel failed; that was a deviation and is corrected, so a partly delivered security alert is retried instead of recorded as sent.
- **SSRF: pre-flight, not dial-time.** Java resolves the webhook host and refuses private, loopback and link-local addresses just before connecting; Go checks at dial time, which also closes a DNS answer that changes in between. The host allow-list (checked when the request is validated) is the first defence in both. Redirects are never followed.
- **Email address syntax** is a plain-address pattern (no display names, quoting or line breaks) approximating Go's `net/mail` check.
- **`/notify` naming email recipients is accepted but not emailed when SMTP is not configured** (the provider list has no email provider), the same as Go.
- **Retention is displayed, not enforced.** `/api/policies` reports `EVENT_RETENTION`, but the Java service does not purge old events yet; the Go service does.
- **Not ported:** SMS, Novu, the SMS daily cap, per-rule channels, payload predicates, accounts and admin roles (the API key is the only gate), CORS, metrics.

## 7. Tests
132 tests with PostgreSQL and Redis (121 without, 3 classes skipped), line coverage 95.8 percent in the gate's scope.
- **Sacred invariant, nine tests** (`SacredInvariantTest`): the five from milestone 34 plus: a critical event is never merged by Redis dedup and never enters the durable queue (only its log row exists, and the worker can never claim it); a standard event still queues and merges through the same real parts; with Redis down a critical event is still delivered and threaded and a standard one is treated as unique; a critical notification sent through `/notify` is delivered inline and touches no queue, dedup, classifier, budget or preferences.
- `NotifyTest` (9), `RulesServiceTest` (12), `DeliveryTest` (11: idempotent enqueue, doubling backoff to a dead letter, permanent failures, manual retry only when dead, expired leases, redaction), `ApiEndpointsTest` (7), `InfrastructureTest` (17, including Redis dedup across two "replicas" and fail-open).
- Integration, skipped without `TEST_DATABASE_URL` / `TEST_REDIS_URL`, run by CI: `PostgresStoresTest` (rules round trip in Go's JSON shape, a stored rule using an unsupported field is refused, idempotent enqueue, claim, retry, requeue, four workers never share a job) and `JedisKeyStoreTest` (two clients sharing a real Redis, expiry, forget, unreachable).
- **Contract:** `07-notify`, `08-rules` and `09-deliveries` pass on Go (`TestContract`) and on Java (`ContractTest`); the Go suite still passes (35 packages).

## 8. Acceptance criteria and result
| Criterion | Result |
| --- | --- |
| `mvn verify` passes with no database | **Yes**: 121 tests, coverage gate met (95.8 percent), PostgreSQL classes excluded from the gate. |
| `mvn verify` passes with PostgreSQL and Redis | **Yes**: 132 tests, 0 skipped. |
| Contract passes on Go and Java, 9 files | **Yes**. |
| Critical via `/notify` and via webhook bypasses queue, Redis dedup, AI and budget | **Yes**, tests above. |
| Real stack: cross-service dedup | **Yes**: the same pull request sent to Go, then to Java, was merged by Java into Go's event (`duplicate of ...`). |
| Real stack: shared rules | **Yes**: a rule created through Java appeared in Go's `GET /api/rules`. |
| Real stack: durable queue | **Yes**: Java queued a standard event to an unreachable Slack host; its dispatcher left it pending after attempt 1 with a 30 s backoff; the webhook path was in no stored error and no API answer. |
| GitHub CI green | See the changelog entry for the run. |

## 9. Review checklist
- [ ] Are two services on one database acceptable with the "configure alike" rule, or should only one dispatch (a setting to turn the dispatcher off)?
- [ ] Java does not purge old events yet; port retention in a later milestone?
- [ ] Milestone 36: accounts, Attention Intelligence, feedback.
