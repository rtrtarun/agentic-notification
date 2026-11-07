# Milestone 34: Java foundation (core parity)

The Java service (`java-server/notification-service`) now runs the same deterministic core as the Go service, on the same database, and a shared HTTP contract checks that the two behave the same. The rest of the Go service (accounts, Attention Intelligence, feedback, learning, traces, MCP) is not in Java yet; it comes in milestones 35 and later.

Approved scope and decisions are in [implementation-sync.md](../implementation-sync.md) section 10.

## 1. Goals
1. Fix the build: `mvn verify` passes, the Compose build folder is correct, duplicate classes are gone.
2. Layer 1 in Java: ingestion and normalization of GitHub webhooks, rules and criticality, deduplication and threading, PostgreSQL persistence, critical delivery inline.
3. The sacred invariant holds in Java, with five independent tests.
4. One database, one schema: Java connects to the schema the Go migrations create.
5. A shared contract in `docs/contract/` that both services must pass.

## 2. Architecture decisions
- **Plain JDK and Jackson, no Spring.** `com.sun.net.httpserver.HttpServer` routes requests; Jackson reads JSON and the YAML rules; HikariCP pools PostgreSQL connections. The service boots in well under a second and the jar is small.
- **Same layers as Go.** `domain` (event, rule, decision: no I/O), `application` (pipeline, policy engine, dedup, rules loader, normalizer, and the ports the pipeline depends on), `infrastructure` (PostgreSQL, SMTP, Slack, settings), `interfaces` (HTTP). `Main` is the only place that builds concrete classes. No SQL outside `PostgresEventRepository`; no decisions in the HTTP layer.
- **Ports instead of concrete classes.** `EventRepository`, `NotificationProvider`, `Classifier`, `Preferences`, `Budget`, `Outbox` and `Deduper` are interfaces. The classifier, preferences, budget and outbox are not implemented in Java yet; they exist as ports so the invariant can be tested against them now and the later milestones only add implementations.
- **The Go service owns the schema.** Java never creates or alters a table (two migrators on one schema drift). At start it waits up to 60 seconds for the `events` table and fails with a clear message if it never appears. `go run ./cmd/migrate` (new, in the Go module) creates the schema on its own; the Go service still migrates at start.
- **One rules file.** Java reads the same `rules.yaml`. Decoding is strict, as in Go. `payloadMatch` and per-rule `channels` (milestone 30 in Go) are refused with an error rather than ignored, so a rule can never silently widen; they come in a later milestone.
- **Shared contract instead of reading code.** `docs/contract/*.json` are replayed by `ContractTest` (Java) and `TestContract` (Go).

## 3. Database changes
None. Java reads and writes the existing `events` table: `INSERT ... ON CONFLICT (id) DO NOTHING`, `SELECT 1 ... WHERE id = ?`, and a newest-first list. Rows Java writes are read by Go and the other way round (checked by hand, see section 8).

## 4. APIs
Identical to Go for: `GET /health`, `POST /webhooks/github` (HMAC signature, size limit, event and delivery id validation, redelivery answers 200 `duplicate`), `GET /events` (API key, `limit` 1 to 500, opaque `cursor` compatible between the two services, no payload in the list). Same settings names: `PORT`, `API_KEY`, `ALLOW_UNAUTHENTICATED_API`, `GITHUB_WEBHOOK_SECRET`, `ALLOW_UNSIGNED_WEBHOOKS`, `DATABASE_URL`, `RULES_PATH`, `DEDUP_WINDOW`, `MAX_BODY_BYTES`, `DEFAULT_NOTIFY_EMAIL`, `SMTP_*`, `SMTP_REQUIRE_STARTTLS`, `SLACK_WEBHOOK_URL`, `LOG_LEVEL`. Without a key or secret the API and webhook refuse requests (fail closed).

Not in Java yet: `/notify`, `/metrics`, rules and policies APIs, inbox, channels, deliveries, accounts, digests, feedback, overrides, learning, traces, MCP, CORS, streaming.

## 5. Folder changes
- `java-server/notification-service/`: rewritten (`pom.xml`, `Dockerfile`, `README.md`, `src/main`, `src/test`). Removed the duplicate `AgentInfo`, `NotificationRequest`, `App` and `Application` classes, the unused `JavaMailUtils`/`HttpUtils` helpers, the nested `.github/workflows/build.yml` (GitHub only reads the root `.github`) and the folder's own `docker-compose.yml` (the root file runs everything).
- `docs/contract/` (new): six contract files and a README.
- `go-server/notification-service/cmd/migrate/` (new) and `pkg/server/contract_test.go` (new).
- `docker-compose.yml`: Java build context fixed; Postgres health check; Go and Java wait for it; `sslmode=disable` on both database URLs (the Go driver otherwise demands TLS, which the Compose Postgres does not have, so the Go service could not start against the database).
- `.github/workflows/ci.yml`: the `java-service` job starts PostgreSQL, runs the Go migration and the full `mvn verify`.

## 6. Risks
- **Behaviour drift.** Mitigated by the contract, but the contract covers only what is written in it. It grows with every parity milestone.
- **Rules features not in Java yet** (`payloadMatch`, `channels`): a shared `rules.yaml` that uses them fails to load in Java. Deliberate; documented above.
- **Pattern syntax.** Rule patterns support `*` and `?` as in Go's `path.Match`; character classes and escapes are rejected in Java at load time.
- **Dedup is per process** (memory), as Go without Redis. Several Java replicas would not share it. Redis comes later.
- **The Java email provider has never talked to a real SMTP server** (only the failure path is tested); Slack is tested against a local fake.

## 7. Tests
69 JUnit tests, line coverage 94.8 percent (gate: 80 percent, `Main` excluded as pure wiring).
- **Sacred invariant, five independent tests** (`SacredInvariantTest`). Each arms everything that could change a critical event (a classifier that demotes everything, preferences that mute everything, a budget that denies everything, a queue, a deduper) and proves: it never reaches the classifier; it is never merged by dedup; it is never muted or delayed by preferences; it never touches the budget; it is delivered inline before it is stored, never queued, and a failed delivery raises instead of being stored as sent.
- `PipelineTest` (17): standard and background routing, merge, redelivery, classifier may only demote and never escalate, failures keep the rule's decision, preferences, budget, queue failure, dedup released after a failed save or delivery, a broken deduper fails open, live rule replacement.
- `RulesTest`, `DedupAndNormalizerTest`, `InfrastructureTest`, `HttpApiTest`, `PostgresEventRepositoryTest` (needs `TEST_DATABASE_URL`; skipped without it), and `ContractTest` (the six shared files).
- Go: `TestContract` replays the same six files against the real Go service; the whole Go suite still passes (35 packages).

Run them as the approval required (Docker, no local JDK needed):

```bash
# Postgres with the Go schema, for the database test (optional but CI always has it)
docker run -d --name pg -e POSTGRES_USER=attention -e POSTGRES_PASSWORD=attention -e POSTGRES_DB=attention -p 55432:5432 postgres:16-alpine
DATABASE_URL="postgres://attention:attention@localhost:55432/attention?sslmode=disable" go run ./cmd/migrate   # in go-server/notification-service

# from the repository root
docker run --rm -v "$(pwd):/app" -v m2cache:/root/.m2 \
  -e TEST_DATABASE_URL="postgres://attention:attention@host.docker.internal:55432/attention?sslmode=disable" \
  -w /app/java-server/notification-service maven:3.9-eclipse-temurin-17 mvn clean verify
```

## 8. Acceptance criteria and result
| Criterion | Result |
| --- | --- |
| `mvn verify` passes | **Yes, locally** in the Maven container: 69 tests, coverage gate met. The CI run on GitHub has not been seen yet. |
| Compose builds and starts the Java service healthy | **Yes**: `docker compose up --build postgres redis go-notification-service java-notification-service`; both `/health` answered. |
| Contract passes on both services | **Yes**: `TestContract` (Go) and `ContractTest` (Java), six files each. |
| Same schema, both directions | **Yes, by hand**: the same pull request sent to each service produced identical rows (class, status, rule, thread id) in one `events` table, and each service listed the other's rows. |
| Five invariant tests | **Yes**, `SacredInvariantTest`. |
| Critical delivery failure | Same in both: with the Compose SMTP host unreachable, a security advisory returned 500 in Go and in Java and was not stored, so GitHub's retry is processed. The success path (critical sent) is proved by the contract and unit tests, not by a real channel. |

## 9. Review checklist
- [ ] `java-service` CI job is green on GitHub (not yet seen).
- [ ] Java `rules.yaml` limits (no `payloadMatch` or `channels` yet) are acceptable for milestone 34.
- [ ] Keep `com.example` as the Java package name, or rename (mechanical).
- [ ] Next milestone: which Go features first (suggested: `/notify`, rules and policies API, then durable delivery and Redis dedup).
