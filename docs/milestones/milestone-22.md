# Milestone 22: Built-in delivery (email, Slack, Teams, SMS)

## Goals
- Standard and background events are **actually delivered**, according to a route table you set, instead of only being recorded.
- Email, Slack, Teams and SMS are first-party channels. No vendor SDK is in the core; the only external hop is the carrier for SMS, behind an interface.
- Delivery is queued, retried with growing pauses, and dead-lettered visibly. Every attempt can be seen and a dead delivery retried.
- Critical events are untouched: still delivered inline to every configured channel, never queued, never capped, never muted.

## Architecture decisions
- **Layer 3 only.** Nothing here decides *whether* to interrupt (that is Layers 1 and 2). The route table is plain configuration (`NOTIFY_STANDARD_CHANNELS`, `NOTIFY_BACKGROUND_CHANNELS`); there is no model in this path.
- **Only events still `queued` are routed.** An event that quiet hours, a mute or dedup already turned into `digest`, `delayed` or `merged` is not delivered now.
- **Targets never reach the database or the console.** A job stores the channel and the message text. The address, webhook URL or phone number is read from configuration when the job is sent. API output omits the text too, and stored errors have URLs reduced to their host.
- **Idempotent.** `UNIQUE (event_id, channel)`: a redelivered webhook or a retry queues nothing twice.
- **Queue semantics.** Workers lease jobs (`FOR UPDATE SKIP LOCKED` in PostgreSQL), so several instances can share the queue and a crashed worker's job becomes claimable again when its lease expires. Failure retries after 30 s, 60 s, 120 s and so on (cap 1 h); after 5 attempts, or at once for a permanent error (bad number, channel no longer configured), the job is dead and waits for a person.
- **Critical is logged, not queued.** `Outbox.RecordDirect` writes one row per critical delivery (`direct: true`, channel = the providers used, e.g. `webhook+sms`) so the log shows everything. A logging failure never fails the event.
- **SMS behind `sms.Gateway`.** Adapters: `mock` (local; logs a masked number and keeps messages), `http` (any JSON API: URL, headers and a body template with `{{to}}`/`{{text}}`), and `sns` (Amazon SNS Publish, Signature V4 implemented in-package, no AWS SDK). The signer is verified against AWS's published test vector.
- **SMS is capped** at `SMS_DAILY_LIMIT` (default 50) queued messages per UTC day. The cap never applies to critical events. Numbers must be E.164 and come only from `SMS_RECIPIENTS`; the public `/notify` API rejects caller-supplied numbers.
- **Teams is unchanged.** It still rides on `WebhookProvider` with `TEAMS_WEBHOOK_URL`, and now also has routing and a delivery log like the others.

## Database changes
New table `delivery_jobs` (idempotent migration in `pkg/db/migrate.go`): id, event_id, channel, class, title, message, grp, status, attempts, next_attempt_at, lease_until, last_error, direct, created_at, updated_at; `UNIQUE (event_id, channel)`; indexes on `(status, next_attempt_at)` and `created_at`.

## APIs
| Route | Purpose |
|---|---|
| `GET /api/deliveries?limit=` | Newest first (default 50, max 200) with counts by status. No targets, no message text. |
| `POST /api/deliveries/{id}/retry` | Send a dead delivery again (200; 404 unknown; 409 not dead). |
| `GET /api/channels` | Now lists SMS, shows which classes each channel receives, and flags `mode: "mock"`. |
| `POST /api/channels/sms/test` | Labelled test text to the configured numbers (rate limited like the others). |

Configuration (all optional; with none set, behaviour is exactly as before): `NOTIFY_STANDARD_CHANNELS`, `NOTIFY_BACKGROUND_CHANNELS` (comma lists of `email`, `slack`, `teams`, `sms`), `SMS_GATEWAY` (`mock`, `http`, `sns`), `SMS_RECIPIENTS`, `SMS_DAILY_LIMIT`, `SMS_HTTP_URL`, `SMS_HTTP_HEADERS` (`Name=value,...`), `SMS_HTTP_BODY`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`. Bad values stop start-up with a clear message.

## Folder changes
- New `pkg/sms` (gateway interface, mock, HTTP, SNS).
- `pkg/delivery`: `sms_provider.go`, `queue.go` (model, `Store`, memory store), `pgqueue.go`, `outbox.go` (targets, routes), `dispatcher.go` (retries, backoff, cap, redaction).
- `pkg/pipeline`: `WithOutbox`; `pkg/server`: `delivery_setup.go`, `deliveries_handlers.go`; `pkg/channels`: SMS channel and routing; `pkg/config`: new settings.
- Web: `lib/deliveries.ts`, `features/manage/DeliveriesPanel.tsx`, Channels screen shows SMS, the Mock flag and the Deliveries panel.

## Risks and how they are handled
| Risk | Handling |
|---|---|
| Sending twice on retry or webhook redelivery | Unique `(event_id, channel)`; tested |
| SMS cost or abuse | E.164 only, server-side numbers only, daily cap, mock by default for local use |
| Secrets in the database, API or errors | Targets resolved at send time; message text hidden from the API; URLs redacted in stored errors; SMS errors never echo the gateway body or URL |
| A queue outage losing events | The event is still saved; the failure is logged. Critical delivery does not depend on the queue |
| Worker crash mid-send | Lease expiry makes the job claimable again (at-least-once; a gateway could deliver twice if it crashed after sending) |
| Muted or held events being delivered | Only `queued` events are routed; tested |

## Tests
- `pkg/sms`: number validation and masking, mock, HTTP template escaping and error hygiene, SNS request shape, **SigV4 against the AWS test vector**.
- `pkg/delivery`: store contract (memory; the same contract runs against PostgreSQL under the `integration` tag), outbox idempotency and routing, backoff and dead-letter, permanent errors, daily cap, redaction, SMS provider.
- `pkg/pipeline`: routed, unrouted, held, merged and critical paths, and outbox failure never failing an event.
- `pkg/server`: webhook to queue to mock gateway, critical inline and logged, no secrets in responses, retry endpoint, start-up validation, SMS test.
- Web: `lib/deliveries.test.ts`; 190 Vitest tests in total.

## Acceptance criteria
- [x] With `SMS_GATEWAY=mock`, a standard event is queued and sent by the background loop; a critical one is sent at once and logged (checked on a running service).
- [x] A failing channel retries, then dead-letters, and can be retried from the console.
- [x] Critical events are never queued, capped, or delayed.
- [x] No phone number, webhook URL or message text appears in `/api/deliveries` or `/api/channels`.

## Known limits (honest list)
- **PostgreSQL queue not run against a real database.** Docker was not running on the build machine. The SQL compiles and shares a contract test with the memory store (`go test -tags integration` with `TEST_DATABASE_URL`), but that test has not been run. Please run it before relying on it.
- **SNS has not talked to real AWS.** The signer matches AWS's test vector and the request shape is tested against a fake endpoint, but no message was sent. Role-based credentials (ECS/EC2) are not supported; use access keys or a session token.
- **Email is plain text over the existing SMTP client;** no HTML, no threading. **Slack uses incoming webhooks;** no bot token and no threads yet.
- **Targets are workspace-wide.** Per-user targets arrive with authentication (milestone 23) and feedback (24).
- **Queued SMS cap is per UTC day and counts sent jobs,** so a burst can overshoot by a few before the count updates.
- Logging is the only observability added; a Prometheus counter per channel is a good next step.

## Review checklist
- [ ] Set `SMS_GATEWAY=mock`, `SMS_RECIPIENTS` and `NOTIFY_STANDARD_CHANNELS=sms`, post a GitHub webhook, and watch Channels > Deliveries.
- [ ] Mute `github` in Preferences, post again: nothing is queued; a security advisory still arrives at once.
- [ ] Break a channel (wrong SMTP host): the delivery retries, then shows "Gave up" with a Retry button, and the error shows no URL.
- [ ] Run `go test -tags integration ./pkg/delivery` against a real PostgreSQL.
- [ ] Send one real text with `SMS_GATEWAY=sns` or `http` to your own number.
