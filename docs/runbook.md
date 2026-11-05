# Runbook: notification service

For whoever is on call. Each section is a symptom, what it means, and what to do.

## Quick facts

- Logs are JSON on stdout; every request has a `request_id` (also the `X-Request-ID` response header).
- `GET /health` needs no key. `GET /metrics` (Prometheus text) needs `X-API-Key`.
- State: PostgreSQL (events), optional Redis (dedup window). Everything else is stateless.
- Kill switch: scale the Deployment to 0, or unset `GITHUB_WEBHOOK_SECRET` (webhooks return 503 and GitHub retries later).

## Alerts to set up

Page on burn rate, not raw thresholds:

- Error ratio: `sum(rate(http_requests_total{code="5xx",route="/webhooks/github"}[5m])) / sum(rate(http_requests_total{route="/webhooks/github"}[5m]))` above 2% for 10 minutes.
- Latency: P99 `histogram_quantile(0.99, sum by (le, route) (rate(http_request_duration_seconds_bucket[5m])))` above 8s on `/webhooks/github`. GitHub gives up at 10s and redelivers.
- Target SLOs (adjust to your contract): webhook availability 99.5%, P99 under 5s.

## The console

The web console (`apps/web`) reads `GET /events` and posts to `/feedback`. Its own error screens say what to do; the matching service-side causes:

| Console says | Cause | Fix |
|---|---|---|
| "The service rejected this API key." | `X-API-Key` does not match `API_KEY` | Enter the current key in Connection settings |
| "The service has no API key, so it refuses every request." | `API_KEY` is unset on the service | Set `API_KEY` and restart |
| "Can't reach http://..." | Service down, wrong address, or the browser blocked the call (CORS) | Start the service, or add the console's origin to `CORS_ALLOWED_ORIGINS` |
| "No events yet." | Healthy service, nothing ingested | Add the GitHub webhook as the screen describes |
| Events missing that you expect | The list shows only the newest 200 (the service returns at most 500, and keeps the newest 1,000 in memory per call) | Check the database directly; pagination is on the roadmap |

The API key is entered in the browser and kept per tab. Rotate it by changing `API_KEY` on the service and entering the new value; no console deploy is needed.

## Symptoms

### Webhooks return 401
Signature mismatch. Check `GITHUB_WEBHOOK_SECRET` matches the secret in the GitHub webhook settings. Logs show `rejected webhook with invalid signature`.

### Webhooks return 503 `webhook secret not configured`
`GITHUB_WEBHOOK_SECRET` is unset. Set it (preferred). `ALLOW_UNSIGNED_WEBHOOKS=true` is for local development only.

### API calls return 503 `api key not configured`
`API_KEY` is unset. Set it from the Secret. `ALLOW_UNAUTHENTICATED_API=true` exists for local development only.

### Webhooks return 500
Look up the `request_id` in the logs. `deliver critical event` means every channel failed (the error lists each one); GitHub will redeliver and the retry runs again. `persist event` means PostgreSQL is failing; check the database. A failed attempt does not mark the event as seen, so the retry is processed, not dropped.

### Critical alerts are not arriving
- Startup log `no delivery channel configured`: set `SMTP_*` plus `DEFAULT_NOTIFY_EMAIL`, or `SLACK_WEBHOOK_URL` / `TEAMS_WEBHOOK_URL`.
- Webhook error `webhook target resolves to a blocked address`: the URL points at a private or loopback address. Only public hosts are allowed.
- Each channel is tried independently; one failing channel does not stop the others, so check every error in the logged message.

### `/notify` returns 400 `webhook host not allowed` or `email domain not allowed`
The request names a host or domain outside `NOTIFY_ALLOWED_WEBHOOK_HOSTS` / `NOTIFY_ALLOWED_EMAIL_DOMAINS`. Add it if legitimate. Hosts of `SLACK_WEBHOOK_URL` and `TEAMS_WEBHOOK_URL` are always allowed.

### Duplicate notifications
- Same event delivered twice with the same `X-GitHub-Delivery`: should not happen; it is acknowledged as `duplicate`. Check that the event table is reachable.
- Without `REDIS_URL`, each replica dedups on its own. Set `REDIS_URL` when running more than one replica.
- If a critical event was delivered but saving failed, the redelivery notifies again. This is deliberate (at-least-once): a duplicate is safer than a lost critical alert.

### Redis is down
Start-up fails if `REDIS_URL` is set and unreachable. At runtime the service keeps working and treats events as unique (`dedup check failed; treating event as unique`), so duplicates may get through but nothing is suppressed.

### Database is full or slow
Retention deletes events older than `EVENT_RETENTION` (default 90 days) every hour. Lower it, or investigate with `pg_stat_activity`. Queries are bounded by `REQUEST_TIMEOUT`; the pool is `DB_MAX_OPEN_CONNS`.

### Requests return 413
Body over `MAX_BODY_BYTES` (default 1 MiB). GitHub payloads are capped at 25 MB by GitHub; raise the limit only if you receive such events.

### Rules edits do not seem to apply
Rules are stored in the `policy_rules` table and edited in the console (Rules and Workflows). `RULES_PATH` or the built-in defaults only **seed an empty table**; after the first start the stored rules win, so editing `rules.yaml` alone changes nothing. The one exception: a `locked: true` rule in the file that is missing from the table is added back at start-up. To start over, `DELETE FROM policy_rules;` and restart.

### Live updates show "Reconnecting"
The console reads `GET /api/stream` (server-sent events) and falls back to polling every 15 seconds, so nothing is lost while it reconnects. Behind a reverse proxy the stream needs response buffering off and a read timeout above 60 seconds (the service already sends `X-Accel-Buffering: no` and a heartbeat every 25 seconds). A wrong API key or a `503` makes the console retry slowly on purpose. The broker is in process: with several replicas a console hears about events handled by the replica it is connected to, and polling covers the rest. At most 100 streams are accepted per replica (`503` with `Retry-After` beyond that).

### A muted or quiet-hours event looks wrong
Preferences (Preferences screen, `GET`/`PUT /api/preferences`) apply to standard and background events only. A muted source or repository becomes `background` with status `digest` and the reason "muted in your preferences"; quiet hours give a standard event status `delayed` and a reason naming when the window ends. Critical events are never changed. Preferences are one row in the `preferences` table.

### Channel test returns 429, 409 or 502
`POST /api/channels/{id}/test`: 429 means a test was sent in the last 10 seconds; 409 means the channel is not configured (see the variable named on the Channels screen); 502 means delivery failed, and the reason is in the service log, not in the response.

### Standard events are not being delivered
Check `NOTIFY_STANDARD_CHANNELS` (empty means standard events are only recorded) and that the named channel is configured on Channels. Muted, held, quiet-hours and merged events are not delivered by design. Open Channels > Deliveries: *Waiting* means the dispatcher has not reached the job or it is backing off; *Gave up* shows the redacted error and a Retry button. A job is dead at once if its number is invalid or its channel was un-configured after queuing.

### SMS
`SMS_GATEWAY=mock` only logs (the Channels screen shows "Mock"). For production set `SMS_GATEWAY=sns` with `AWS_REGION`, `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` (SNS must be out of the SMS sandbox for the account), or `http` with `SMS_HTTP_URL`, `SMS_HTTP_HEADERS` and `SMS_HTTP_BODY`. Numbers are E.164 and come only from `SMS_RECIPIENTS`. `SMS_DAILY_LIMIT` (default 50) caps queued texts per UTC day; critical events are never capped.

## Deploying

1. Create the Secret (`k8s/secret.example.yaml` shows the keys). Never commit real values.
2. Apply the ConfigMap and Deployment. Rolling updates are safe: migrations are idempotent and run under an advisory lock.
3. Verify: `/health` is 200, then send a signed test webhook.

## Backup and recovery

Events are in PostgreSQL; use the managed database's backups and point-in-time recovery. The service holds no other durable state (Redis only caches the dedup window; losing it just allows one window of duplicates). Test a restore into a scratch database before relying on it.

Targets (proposed, confirm with stakeholders): RPO 5 minutes, RTO 30 minutes.
