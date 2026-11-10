# Java notification service

The Java implementation of the Attention Orchestration core. It is being brought to parity with the Go service milestone by milestone; milestone 34 delivered the deterministic core, 35 the rules API and durable queue, and 36 the accounts. See [docs/java-service.md](../../docs/java-service.md) and [docs/milestones/milestone-36.md](../../docs/milestones/milestone-36.md).

## What it does
`POST /webhooks/github` verifies the signature, normalises the event, evaluates the rules, deduplicates, delivers critical events inline, and stores everything in the PostgreSQL schema the Go service migrates. `GET /events` lists what was stored. `GET /health` is open.

**Critical events** are decided by rules alone. They never reach AI, are never merged, muted, delayed or budgeted, and are delivered inline before they are stored. Five tests prove each guarantee (`SacredInvariantTest`).

Also: `POST /notify` (agents ask the service to notify people, with the Go service's validation), the rules API (`/api/rules`, `/api/policies`, stored in the shared `policy_rules` table and reloaded at run time), the durable delivery queue (`/api/deliveries`; retry with backoff, dead letters, manual retry) and Redis deduplication shared across replicas and with the Go service.

Accounts (milestone 36): `POST /api/auth/signup` (off unless `AUTH_SIGNUP=approval`; the account is pending until an admin approves it), `/api/auth/login`, `logout`, `me`, `status`, and `/api/users` (admin). Sessions use an `HttpOnly` cookie and a CSRF token, and the API key still works; settings `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD`, `AUTH_SESSION_TTL`, `AUTH_COOKIE_SECURE`, `AUTH_COOKIE_SAMESITE`, `AUTH_SIGNUP`; `GET /health` returns `version` (`APP_VERSION`). No CORS and no single sign-on.

Not here yet (Go only): metrics, inbox, channels API, SMS, Novu, single sign-on, language-model Attention Intelligence, learning, traces, MCP, event retention. Rule `payloadMatch` and `channels` work (milestone 37a).

**Two services on one database must be configured alike**: both run a dispatcher against `delivery_jobs`, and a queued job goes to whichever claims it first.

## Layout
```
com.agentpulse.notification
  domain/           Event, Rule, DecisionState (no I/O)
  application/      Pipeline, PolicyEngine, Dedup, RulesLoader, GitHubNormalizer, Ports (interfaces)
  infrastructure/   PostgresEventRepository, Database, Providers (SMTP, Slack), Config
  interfaces/       HttpApi, Signature, Cursor
  Main              the only place concrete classes are built
```

## Run
With the rest of the stack (from the repository root; the Go service creates the schema, the Java service waits for it):

```bash
docker compose up --build postgres redis go-notification-service java-notification-service
curl localhost:8081/health
```

Settings use the Go service's names: `PORT`, `API_KEY`, `GITHUB_WEBHOOK_SECRET`, `DATABASE_URL`, `RULES_PATH`, `DEDUP_WINDOW`, `DEFAULT_NOTIFY_EMAIL`, `SMTP_*`, `SLACK_WEBHOOK_URL`, `LOG_LEVEL`. With no `API_KEY` the API refuses requests and with no `GITHUB_WEBHOOK_SECRET` the webhook does, unless `ALLOW_UNAUTHENTICATED_API=true` or `ALLOW_UNSIGNED_WEBHOOKS=true`. With no `DATABASE_URL` events are kept in memory.

## Test
No local JDK is needed; use the Maven container from the repository root:

```bash
docker run --rm -v "$(pwd):/app" -v m2cache:/root/.m2 -w /app/java-server/notification-service \
  maven:3.9-eclipse-temurin-17 mvn clean verify
```

`mvn verify` passes with no database: the PostgreSQL classes and the Redis adapter are outside the 80 percent line-coverage gate and their tests are skipped. Set `TEST_DATABASE_URL` (a PostgreSQL the Go migrations have run on: `go run ./cmd/migrate` in the Go module) and `TEST_REDIS_URL` to run them; CI always does. The shared HTTP contract in [docs/contract](../../docs/contract/README.md) runs as part of the suite.
