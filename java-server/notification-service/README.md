# Java notification service

The Java implementation of the Attention Orchestration core. It is being brought to parity with the Go service milestone by milestone; milestone 34 delivered the deterministic core. See [docs/milestones/milestone-34.md](../../docs/milestones/milestone-34.md).

## What it does
`POST /webhooks/github` verifies the signature, normalises the event, evaluates the rules, deduplicates, delivers critical events inline, and stores everything in the PostgreSQL schema the Go service migrates. `GET /events` lists what was stored. `GET /health` is open.

**Critical events** are decided by rules alone. They never reach AI, are never merged, muted, delayed or budgeted, and are delivered inline before they are stored. Five tests prove each guarantee (`SacredInvariantTest`).

Not here yet (Go only): `/notify`, metrics, rules and policies API, inbox, channels, durable delivery, accounts, Attention Intelligence, feedback, learning, traces, MCP.

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

Set `TEST_DATABASE_URL` (a PostgreSQL the Go migrations have run on: `go run ./cmd/migrate` in the Go module) to include the database test. **The 80 percent line-coverage gate assumes that test runs** (CI always provides PostgreSQL); without it the 3 database tests are skipped and `mvn verify` fails the gate, while `mvn test` still passes. The shared HTTP contract in [docs/contract](../../docs/contract/README.md) runs as part of the suite.
