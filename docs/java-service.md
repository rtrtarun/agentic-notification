# Java notification service

The Java implementation of the Attention Orchestration platform (`java-server/notification-service`). It follows the Go service milestone by milestone and must behave the same; a shared HTTP contract ([contract/](contract/README.md), 18 files) is replayed against both, so "the same" is checked by tests. Current milestone: [milestones/](milestones/README.md); finished milestone records (34 to 38): [archived/milestones/](archived/milestones/). Operating notes: [the service README](../java-server/notification-service/README.md).

## What it does today
Each item works as in Go; the milestone and contract file that prove it are in brackets.

- **Ingestion** (34, 35; files 02, 05, 07): `POST /webhooks/github` (HMAC signature, size limit, event and delivery id validation; a redelivery answers 200 `duplicate`) and `POST /notify` (API key, SSRF host allow-list, one-line title, size limits, no caller-chosen SMS numbers).
- **Layer 1, deterministic, no AI** (34, 35, 37a; files 03, 04, 08, 11): normalization; rules from the shared `rules.yaml` (strict decoding, locked critical rules first) and the shared `policy_rules` table (run-time reload); rule `payloadMatch` (up to five predicates: eq, ne, gt, gte, lt, lte, exists over a dotted path, with `branch`, `author` and `severity` aliases) and per-rule `channels` (a critical rule cannot choose channels); deduplication (memory, or Redis shared across replicas and with Go, failing open at run time); threading.
- **Critical events** are decided by rules alone, delivered inline before they are stored, and never reach AI, merging, muting, delay, budget or the queue. Delivery fails if any channel fails, so the sender retries. Sixteen `SacredInvariantTest` cases prove it.
- **Delivery** (35; file 09): email (SMTP), Slack and Teams webhooks. Standard and background events go through a durable PostgreSQL queue: retry with doubling backoff, dead letters, manual retry, leases that survive a crashed worker, `FOR UPDATE SKIP LOCKED`.
- **Accounts** (36; file 10): sign-up with admin approval (`AUTH_SIGNUP=approval`, default off), sign-in (`403` with `code` `pending` or `rejected`), sign-out, `GET /api/auth/me`, `GET /api/auth/status`, and admin `GET`/`POST /api/users`, `PATCH /api/users/{id}`. `HttpOnly` session cookie plus CSRF token; the API key still works. Passwords are argon2id in Go's format, so an account made by one service signs in on the other.
- **Feedback and overrides** (37b; file 12): `POST /feedback` (opened, dismissed, undo, always, never, reply) and `/api/overrides`. An override outranks quiet hours, judgement and the budget for non-critical events; "never" on a critical event is refused; `OVERRIDES_ADMIN_ONLY=true` makes a member's choice only a signal.
- **Attention Intelligence, heuristic** (37c; file 13): a deterministic classifier that can only move an adaptive standard event to background and never sees a critical one (`GuardedClassifier`); explanations (`GET /api/explain/{eventId}`); digests (`GET /api/digests`, `GET /api/digests/{id}`, admin `POST /api/digests/generate`, scheduled by `ATTENTION_DIGEST_EVERY`); the attention budget (Redis with Go's key and script, so both services count one budget, or memory), which holds over-budget events for the next digest and never drops them; `GET /api/attention/status`.
- **Learning** (38a, 38c; files 14, 15): suggestions (`GET /api/learning/suggestions`, admin apply and dismiss, the same suggestion id as Go) and offline score tuning (admin `POST /api/learning/runs`, `GET /api/learning/runs`, `GET /api/learning/adjustments?status=`, admin approve, reject and retire). **Nothing changes until an admin approves**; the heuristic reads only an in-memory snapshot; critical events are never counted and a scope a critical or locked rule covers is never proposed; an explicit override still wins. The arithmetic and ids match Go's, and the tables are shared with one running run across both services.
- **Agent steps and traces** (39a; file 16): `POST /api/events` (one step of an agent run: `traceId`, `parentEventId`, `agentName`, `severity`, telemetry in `metadata`) through the same pipeline as a webhook; an agent's `severity` is only data for rules and never sets the class. `GET /api/events?traceId=` (and `/events`) narrows the list to one run; `GET /api/traces` and `GET /api/traces/{id}` group steps into runs with depth, totals, worst class and status, ties broken exactly as in Go.
- **MCP server** (39b; file 17): `POST /api/mcp`, one JSON-RPC 2.0 message per request, protocol `2024-11-05` whatever the client proposes, tools `publish_event`, `get_recent_events`, `notify_user` and `explain_decision`, acting only as the REST API does; a notification gets 202 with no body. Go's stdio `cmd/mcp-server` pointed at this service (`AGENTPULSE_URL`) gives a stdio MCP server too.
- **Tracing and span export** (39c; file 18): every response except `/health` carries a W3C `traceparent` that continues a valid incoming one and keeps its sampled flag (only bit 0 means sampled); requests, the pipeline and its steps are spans with Go's names and attributes, the span context passed explicitly as a parameter (no `ThreadLocal`). With `OTEL_EXPORTER_OTLP_ENDPOINT` set, sampled spans go to a collector as OTLP/HTTP JSON through a bounded, never-blocking buffer; retries only after a network error, 429 or 5xx; no payload or error text is ever exported.
- **Other API:** `GET /health` (open; returns `version`), `GET /events` (cursor-compatible with Go), the rules API (`/api/rules`, `/reorder`, `/test`), `GET /api/policies`, `GET /api/deliveries`, `POST /api/deliveries/{id}/retry`.

## What it does not do yet (Go only)
SMS and Novu, the channels API, metrics and analytics, the inbox, event retention (shown by `/api/policies`, not enforced), single sign-on, CORS (a console on another site cannot use the API in a browser), streaming, a language model for Attention Intelligence (`ATTENTION_PROVIDER` other than `heuristic` is refused at start), quiet-hours preferences, personal digests (`?scope=me` is refused) and delivering digests (they are stored and readable), and per-person budgets and schedules. The order these arrive in is [roadmap.md](roadmap.md) (milestones 40 to 42).

## Architecture
```text
com.agentpulse.notification
  domain/           Event, Rule, Predicate, DecisionState, Attention, Status (no I/O)
  application/      Pipeline, PolicyEngine, Predicates, RulesLoader, RulesService, GitHubNormalizer, Dedup,
                    Notify, Delivery, QueueOutbox, Dispatcher, Targets, Accounts, Overrides, Feedback,
                    Heuristic, GuardedClassifier, Digests, Budgets, Learning, Tuning, AgentEvents, Traces,
                    Mcp, RawJson, Telemetry, and the Ports (interfaces)
  infrastructure/   Config, Database, the PostgreSQL stores (events, rules, deliveries, accounts, feedback,
                    digests, learning, tuning), RedisDeduper, JedisKeyStore, the memory stores, Providers
                    (SMTP, Slack, Teams), OtlpExporter
  interfaces/       HttpApi (routing), ApiSupport (auth, bodies), NotifyApi, RulesApi, DeliveriesApi, AuthApi,
                    FeedbackApi, AttentionApi, LearningApi, TuningApi, AgentApi, McpApi, Routes, Signature, Cursor, Query
  Main              the only place concrete classes are built
```
- Plain JDK `HttpServer`, Jackson, HikariCP, Jedis and BouncyCastle (argon2id); no Spring.
- The ports (`EventRepository`, `NotificationProvider`, `Classifier`, `Preferences`, `Budget`, `Outbox`, `Deduper`) are interfaces. `Preferences` has no Java implementation yet; the invariant is still tested against it.
- **The Go service owns the schema.** Java never creates or alters a table; it waits up to 60 seconds for `events`. `go run ./cmd/migrate` (in the Go module) creates the schema on its own, and the Go service migrates on start.
- Redis keys are Go's (`dedup:` plus the dedup key; the budget key and script), so an event seen by one service is a duplicate for the other and both count one budget.
- Where Go's encoding rules leak into answers, Java copies them: JSON keys match without regard to case, whole-number floats are written without `.0`, ids from Go's `json.RawMessage` are echoed byte for byte, and lists break ties as Go's do.

## Configuration
Same names as Go: `PORT`, `API_KEY`, `ALLOW_UNAUTHENTICATED_API`, `GITHUB_WEBHOOK_SECRET`, `ALLOW_UNSIGNED_WEBHOOKS`, `DATABASE_URL`, `REDIS_URL`, `RULES_PATH`, `DEDUP_WINDOW`, `MAX_BODY_BYTES`, `DEFAULT_NOTIFY_EMAIL`, `SMTP_*`, `SMTP_REQUIRE_STARTTLS`, `SLACK_WEBHOOK_URL`, `TEAMS_WEBHOOK_URL`, `NOTIFY_ALLOWED_WEBHOOK_HOSTS`, `NOTIFY_ALLOWED_EMAIL_DOMAINS`, `NOTIFY_STANDARD_CHANNELS`, `NOTIFY_BACKGROUND_CHANNELS`, `EVENT_RETENTION`, `LOG_LEVEL`; accounts: `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD`, `AUTH_SESSION_TTL`, `AUTH_COOKIE_SECURE`, `AUTH_COOKIE_SAMESITE`, `AUTH_SIGNUP`; attention: `ATTENTION_CLASSIFY` (default `true`), `ATTENTION_BUDGET_PER_HOUR` (default `10`, `0` turns it off), `ATTENTION_DIGEST_EVERY` (default `0`: only on request); `OVERRIDES_ADMIN_ONLY`; learning: `LEARNING_MIN_SIGNALS` (5), `LEARNING_LOOKBACK` (30 days), `LEARNING_MIN_PEOPLE` (2), `LEARNING_MAX_ADJUSTMENT` (20, at most 50), `LEARNING_HALF_LIFE` (14 days), `LEARNING_TRAIN_EVERY` (default `0`: only when an admin starts a run); `DISPATCHER_ENABLED` (default `true`); span export: `OTEL_EXPORTER_OTLP_ENDPOINT` (unset: off), `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_SERVICE_NAME` (default `notification-service-java`), `OTEL_EXPORT_BATCH` (100), `OTEL_EXPORT_BUFFER` (2048); `APP_VERSION` is set by the image build.

No `API_KEY` means the API refuses requests. No `GITHUB_WEBHOOK_SECRET` means the webhook refuses, unless the matching `ALLOW_*` flag is set. No `DATABASE_URL` keeps everything in memory.

**Two services on one database:** run one queue dispatcher (`DISPATCHER_ENABLED=false` on the other), one digest schedule (`ATTENTION_DIGEST_EVERY=0` on the other) and one learning schedule (`LEARNING_TRAIN_EVERY=0` on the other).

## Run and test
```bash
docker compose up --build postgres redis go-notification-service java-notification-service
curl localhost:8081/health

# tests, no local JDK needed (from the repository root; Git Bash needs MSYS_NO_PATHCONV=1)
docker run --rm -v "$(pwd):/app" -v m2cache:/root/.m2 -w /app/java-server/notification-service \
  maven:3.9-eclipse-temurin-17 mvn clean verify
```
`mvn verify` passes with no database: the PostgreSQL classes and the Redis adapter are outside the 80 percent gate and their tests skip. Set `TEST_DATABASE_URL` (a database migrated by the Go `cmd/migrate`) and `TEST_REDIS_URL` to run them; CI always does.

## Tests
- Offline (milestone 39c): **395 tests, 5 skipped** (the PostgreSQL and Redis ones), 90.7 percent of lines.
- With PostgreSQL 16 and Redis 7 (milestone 39c): **424 tests, none skipped**, 95.0 percent of lines.
- `SacredInvariantTest` (17 cases): a collector that never answers does not delay a critical event; critical events skip the classifier, dedup (memory and Redis), overrides, preferences, budget, learning and the queue, are delivered inline, and fail loudly when delivery fails; an agent cannot make its own step critical.
- `ContractTest` replays all 18 shared contract files, as `TestContract` does in Go.
- CI: the `java-service` job is green on PR #20 (39a) and PR #21 (39b).

## Known behaviours
- The SSRF check is pre-flight (resolve, refuse private addresses, connect), not dial-time as in Go (finding F6, milestone 44). Redirects are never followed.
- Rule patterns support `*` and `?` as `path.Match` does; character classes and escapes are rejected at load.
- Both services edit `policy_rules`; only the service that changed a rule reloads immediately. The overrides cache is per process, so a change made elsewhere reaches this one on restart.
- Traces, MCP reads and explanations look at the newest 1000 events, as in Go (finding F10, milestone 42).
- Java refuses invalid UTF-8 in a request body as invalid JSON, where Go accepts it and replaces the bad bytes.
- The SMTP provider has only had its failure path tested; Slack is tested against a local fake.
