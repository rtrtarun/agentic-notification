# Java notification service

The Java implementation of the Attention Orchestration core (`java-server/notification-service`). It follows the Go service milestone by milestone and must behave the same, which a shared contract checks. Milestone records: [34](milestones/milestone-34.md) and [35](milestones/milestone-35.md). Operating notes: [the service README](../java-server/notification-service/README.md).

## What it does today
- **Ingestion:** `POST /webhooks/github` (HMAC signature, size limit, event and delivery id validation; a redelivery answers 200 `duplicate`) and `POST /notify` (API key, SSRF host allow-list, one-line title, size limits, no caller-chosen SMS numbers).
- **Layer 1, deterministic, no AI:** normalization; rules from the shared `rules.yaml` (strict decoding, locked critical rules first) and from the shared `policy_rules` table (run-time reload); deduplication (memory, or Redis shared across replicas and with the Go service, failing open at run time); threading.
- **Critical events** are decided by rules alone, delivered inline before they are stored, and never reach AI, merging, muting, delay, budget or the queue. Delivery fails if any channel fails, so GitHub retries.
- **Delivery:** email (SMTP), Slack and Teams webhooks. Standard and background events go through a durable PostgreSQL queue: retry with doubling backoff, dead letters, manual retry, leases that survive a crashed worker, `FOR UPDATE SKIP LOCKED`.
- **Accounts (milestone 36):** `POST /api/auth/signup` (off unless `AUTH_SIGNUP=approval`; the account is pending until an admin approves it), `POST /api/auth/login` (`403` with `code` `pending` or `rejected` after a correct password), `POST /api/auth/logout`, `GET /api/auth/me`, `GET /api/auth/status`, and `GET`/`POST /api/users`, `PATCH /api/users/{id}` (admin; `status` approves or rejects). Sessions use an `HttpOnly` cookie and a CSRF token, work on every protected endpoint, and the API key still works. Rule changes and manual delivery retries need an admin. Passwords are argon2id in the same format as Go (BouncyCastle), so an account made by one service signs in on the other. There is no CORS, so a console on another site cannot use these endpoints in a browser.
- **API:** `GET /health` (returns `version`), `GET /events` (cursor-compatible with Go), `GET` and `POST /api/rules`, `PUT` and `DELETE /api/rules/{id}`, `POST /api/rules/reorder`, `POST /api/rules/test`, `GET /api/policies`, `GET /api/deliveries`, `POST /api/deliveries/{id}/retry`.

## What it does not do yet (Go only)
SMS, Novu, metrics, inbox, channels API, single sign-on, Attention Intelligence, feedback, learning, traces, MCP, CORS, streaming, and event retention (shown by `/api/policies`, not enforced). Rules with `payloadMatch` or per-rule `channels` are **refused** at the API and at start-up, never ignored.

## Architecture
```text
com.agentpulse.notification
  domain/           Event, Rule, DecisionState (no I/O)
  application/      Pipeline, PolicyEngine, Dedup, RulesLoader, GitHubNormalizer, Notify, RulesService,
                    Delivery, QueueOutbox, Dispatcher, Targets, and the Ports (interfaces)
  infrastructure/   PostgresEventRepository, PostgresRuleStore, PostgresDeliveryStore, RedisDeduper,
                    JedisKeyStore, MemoryStores, Providers (SMTP, Slack, Teams), Config
  interfaces/       HttpApi, NotifyApi, RulesApi, DeliveriesApi, Signature, Cursor
  Main              the only place concrete classes are built
```
- Plain JDK `HttpServer`, Jackson, HikariCP and Jedis; no Spring.
- The ports (`EventRepository`, `NotificationProvider`, `Classifier`, `Preferences`, `Budget`, `Outbox`, `Deduper`) are interfaces. Classifier, preferences and budget have no Java implementation yet, but exist so the invariant is tested against them.
- **The Go service owns the schema.** Java never creates or alters a table; it waits up to 60 seconds for `events`. `go run ./cmd/migrate` (in the Go module) creates the schema on its own.
- Redis keys are `dedup:` plus the dedup key, the same as Go, so an event seen by one service is a duplicate for the other.

## Configuration
Same names as Go: `PORT`, `API_KEY`, `ALLOW_UNAUTHENTICATED_API`, `GITHUB_WEBHOOK_SECRET`, `ALLOW_UNSIGNED_WEBHOOKS`, `DATABASE_URL`, `REDIS_URL`, `RULES_PATH`, `DEDUP_WINDOW`, `MAX_BODY_BYTES`, `DEFAULT_NOTIFY_EMAIL`, `SMTP_*`, `SMTP_REQUIRE_STARTTLS`, `SLACK_WEBHOOK_URL`, `TEAMS_WEBHOOK_URL`, `NOTIFY_ALLOWED_WEBHOOK_HOSTS`, `NOTIFY_ALLOWED_EMAIL_DOMAINS`, `NOTIFY_STANDARD_CHANNELS`, `NOTIFY_BACKGROUND_CHANNELS`, `EVENT_RETENTION`, `LOG_LEVEL`, and for accounts `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD`, `AUTH_SESSION_TTL`, `AUTH_COOKIE_SECURE`, `AUTH_COOKIE_SAMESITE`, `AUTH_SIGNUP`; `DISPATCHER_ENABLED` (default `true`; `false` leaves the delivery queue to the other service when both share a database); `APP_VERSION` is set by the image build.

No `API_KEY` means the API refuses requests. No `GITHUB_WEBHOOK_SECRET` means the webhook refuses, unless the matching `ALLOW_*` flag is set. No `DATABASE_URL` keeps events in memory.

## Run and test
```bash
docker compose up --build postgres redis go-notification-service java-notification-service
curl localhost:8081/health

# tests, no local JDK needed (from the repository root)
docker run --rm -v "$(pwd):/app" -v m2cache:/root/.m2 -w /app/java-server/notification-service \
  maven:3.9-eclipse-temurin-17 mvn clean verify
```
`mvn verify` passes with no database: the PostgreSQL classes and the Redis adapter are outside the 80 percent gate and their tests skip. Set `TEST_DATABASE_URL` (migrated by the Go `cmd/migrate`) and `TEST_REDIS_URL` to run them; CI always does.

## Tests
170 with PostgreSQL (156 without), 95.2 percent line coverage in gate scope. Nine `SacredInvariantTest` cases prove that critical events skip the classifier, dedup (memory and Redis), preferences, budget and queue, are delivered inline, and fail loudly when delivery fails. The shared HTTP contract ([contract/](contract/README.md), ten files) is replayed by `ContractTest` here and `TestContract` in Go.

## Known behaviours
- **Two services on one database must be configured alike**: both dispatch from `delivery_jobs`, and a job goes to whichever claims it first.
- The SSRF check is pre-flight (resolve, refuse private addresses, connect), not dial-time as in Go. Redirects are never followed.
- Rule patterns support `*` and `?` as `path.Match` does; character classes and escapes are rejected at load.
- Both services edit `policy_rules`; only the service that changed a rule reloads immediately.
- The SMTP provider has only had its failure path tested; Slack is tested against a local fake.
