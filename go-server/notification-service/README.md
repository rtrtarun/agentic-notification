# Go Notification Service

Run locally:

```bash
cd go-server/notification-service
API_KEY=dev-key ALLOW_UNSIGNED_WEBHOOKS=true go run ./cmd/notification-service
```

The API fails closed: with no `API_KEY` every protected route returns 503. Set `API_KEY`, or `ALLOW_UNAUTHENTICATED_API=true` for local experiments only.

## Environment variables

### Security

| Variable | Default | Purpose |
|---|---|---|
| `API_KEY` | none | Required `X-API-Key` for `/notify`, `/feedback`, `/metrics` (compared in constant time) |
| `ALLOW_UNAUTHENTICATED_API` | `false` | `true` serves protected routes with no key when `API_KEY` is unset. Local development only |
| `GITHUB_WEBHOOK_SECRET` | none | Verifies `X-Hub-Signature-256` on `POST /webhooks/github`. Without it the endpoint returns 503 |
| `ALLOW_UNSIGNED_WEBHOOKS` | `false` | Accept unsigned webhooks when no secret is set. Local development only |
| `CORS_ALLOWED_ORIGINS` | empty (CORS closed) | Comma-separated browser origins allowed to call the API. The console needs its own origin here, for example `http://localhost:3000` |
| `NOTIFY_ALLOWED_WEBHOOK_HOSTS` | Slack and Teams hosts | Hosts a caller may name in `slackWebhookUrls` / `teamsWebhookUrls` (`*.example.com` allowed). URLs must be `https`. Empty rejects all caller-supplied webhooks |
| `NOTIFY_ALLOWED_EMAIL_DOMAINS` | empty (any valid address) | Restrict `emailRecipients` to these domains |
| `MAX_BODY_BYTES` | `1048576` | Request body limit on every route (413 when exceeded) |
| `REQUEST_TIMEOUT` | `25s` | Per-request deadline covering queries and delivery |

### Storage and scaling

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | none (in-memory) | PostgreSQL connection string. Migrations run at start under an advisory lock |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | `25` / `5` | Connection pool size |
| `DB_CONN_MAX_LIFETIME` | `30m` | Recycle connections after this long |
| `EVENT_RETENTION` | `2160h` (90 days) | Events older than this are deleted hourly. `0` disables retention |
| `REDIS_URL` | none (in-process) | Share the dedup window across replicas. Start-up fails if set but unreachable; at runtime an outage fails open |
| `DEDUP_WINDOW` | `10m` | How long a repeated event counts as a duplicate |
| `RULES_PATH` | built-in rules | YAML policy rules (attention classes are validated) |

### Delivery and logging

`DEFAULT_NOTIFY_EMAIL`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_SENDER`, `SLACK_WEBHOOK_URL`, `TEAMS_WEBHOOK_URL`, `LOG_LEVEL` (`debug`/`info`/`warn`/`error`, default `info`, JSON on stdout), `PORT` (default `8080`).

Routing and SMS (milestone 22; all optional): `NOTIFY_STANDARD_CHANNELS` and `NOTIFY_BACKGROUND_CHANNELS` (comma lists of `email`, `slack`, `teams`, `sms`; empty means that class is only recorded), `SMS_GATEWAY` (`mock`, `http`, `sns`), `SMS_RECIPIENTS` (E.164), `SMS_DAILY_LIMIT` (default `50`, `0` = no cap), `SMS_HTTP_URL`, `SMS_HTTP_HEADERS` (`Name=value,...`), `SMS_HTTP_BODY` (template with `{{to}}` and `{{text}}`), `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`.

Sign-up and release (milestone 36): `AUTH_SIGNUP` (`off` default, or `approval`: people may ask for an account and an admin must approve it; a sign-up never gets a session or any data until approved), `APP_VERSION` (set by the image build from the `VERSION` file; returned by `/health`, `dev` otherwise).

Accounts and sign-in (milestone 23; all optional, the API key keeps working): `AUTH_BOOTSTRAP_EMAIL` and `AUTH_BOOTSTRAP_PASSWORD` (first admin, together), `AUTH_SESSION_TTL` (default `12h`), `AUTH_COOKIE_SECURE` (`auto`, `true`, `false`), `AUTH_COOKIE_SAMESITE` (`lax`, `strict`, `none`), `AUTH_SECRET`, `PUBLIC_URL`, `CONSOLE_URL`, `ENTRA_TENANT_ID` or `OIDC_ISSUER`, `OIDC_NAME`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_ALLOWED_DOMAINS`.

Attention Intelligence (milestone 25; all optional): `ATTENTION_CLASSIFY` (default `true`), `ATTENTION_DIGEST_EVERY` (default `0`, on request only; e.g. `24h`), `NOTIFY_DIGEST_CHANNELS` (channels a digest is delivered to; empty means stored only).
Language model (milestone 26; all optional): `ATTENTION_PROVIDER` (`heuristic` default, `ollama` or `claude`), `OLLAMA_URL`, `OLLAMA_MODEL`, `ANTHROPIC_API_KEY` (required for `claude`), `ATTENTION_MODEL` (Claude model name), `ATTENTION_TIMEOUT` (default `5s`), `ATTENTION_MAX_CALLS_PER_MIN` (default `60`). A model that fails falls back to the heuristic; see `GET /api/attention/status`.
Learning suggestions (milestone 28; optional): `LEARNING_MIN_SIGNALS` (default `5`) and `LEARNING_LOOKBACK` (default `720h`).
Email through Novu (milestone 32; optional): `NOVU_API_KEY`, `NOVU_API_URL` (default `https://api.novu.co`), `NOVU_WORKFLOW_ID` (default `attention-orchestration`); `SMTP_REQUIRE_STARTTLS` (default `false`) refuses plaintext SMTP.
Attention budget and worker (milestone 29): `ATTENTION_BUDGET_PER_HOUR` (default `10`, `0` off), `ATTENTION_ASYNC` (default `false`), `ATTENTION_WORKER_INTERVAL` (default `2s`); `REDIS_URL` shares the budget across replicas.

`OVERRIDES_ADMIN_ONLY` (default `false`) limits creating always/never notify overrides to admins.

## Endpoints

| Route | Auth | Purpose |
|---|---|---|
| `GET /health` | none | Liveness/readiness probe |
| `GET /metrics` | API key | Prometheus text format: request counts and latency histograms per route |
| `GET /events` | API key | Recent events, newest first, without raw payloads. `?limit=1..500` (default 100). Used by the [web console](../../apps/web/README.md) |
| `POST /notify` | API key | Send a notification (validated, see below) |
| `POST /feedback` | session or API key | Record feedback (`opened`, `dismissed`, `undo`, `always`, `never`, `reply`); `always`/`never` create an override. See [milestone-24](../../docs/archived/milestones/milestone-24.md) |
| `GET /api/digests`, `GET /api/digests/{id}`, `POST /api/digests/generate` (admin), `GET /api/explain/{id}` | session or API key | Digests of held notifications and plain-language explanations. See [milestone-25](../../docs/archived/milestones/milestone-25.md) |
| `GET/POST /api/overrides`, `DELETE /api/overrides/{id}` | session or API key | Always and never notify choices |
| `GET /api/auth/status`, `POST /api/auth/login`, `GET /api/auth/oidc/start` | none | Sign-in (see [milestone-23](../../docs/archived/milestones/milestone-23.md)). `GET /api/auth/me`, `POST /api/auth/logout`, `POST /api/auth/password` need a session or the API key |
| `GET/POST /api/users`, `PATCH /api/users/{id}`, `GET /api/audit` | admin | Accounts and the security log |
| `GET /api/deliveries` | API key | Delivery log and queue with counts by status (no targets or message text). `POST /api/deliveries/{id}/retry` sends a dead delivery again |
| `POST /webhooks/github` | HMAC signature | Ingest a GitHub event. Redeliveries (same `X-GitHub-Delivery`) return `200 {"status":"duplicate"}` and notify nothing |

`POST /notify`:

```json
{
  "agent": {"name":"agent1","owner":"dev","group":"ops"},
  "title":"Alert",
  "message":"Hello",
  "emailRecipients":["user@example.com"],
  "slackWebhookUrls":["https://hooks.slack.com/services/..."],
  "teamsWebhookUrls":["https://example.webhook.office.com/..."]
}
```

Rejected with 400: non-`https` or non-allow-listed webhook hosts, recipients that are not plain addresses (or outside the allowed domains), titles with line breaks or over 200 characters, messages over 20,000 characters, more than 20 recipients or 10 webhooks. Every channel is attempted even if one fails; failures are joined in the logged error.

## Testing

```bash
go test ./...                                  # unit tests
go test -race -tags integration ./...          # plus PostgreSQL integration tests
```

Integration tests need `TEST_DATABASE_URL` (they skip without it):

```bash
docker run --rm -d -p 5433:5432 -e POSTGRES_PASSWORD=test postgres:16-alpine
TEST_DATABASE_URL='postgres://postgres:test@localhost:5433/postgres?sslmode=disable' go test -tags integration ./...
```

`-race` needs cgo. On Windows without a C compiler run it in Docker: `docker run --rm -v "$PWD:/src" -w /src golang:1.26 go test -race ./...`.

CI also runs `golangci-lint` (`.golangci.yml`), `govulncheck`, and an 80% coverage gate. See [docs/runbook.md](../../docs/runbook.md) for operating the service.
