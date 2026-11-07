# delivery

Layer 3: sends notifications. Nothing here decides *whether* to interrupt someone.

## Providers (send one request)
- `Provider` — interface (`Send`, `Name`). Swap in another vendor behind it.
- `SMTPProvider` — email. Recipients are parsed (no CR/LF), subject and sender are flattened to one line.
- `WebhookProvider` — Slack and Teams POST. `NewWebhookProvider(true)` refuses loopback, private and link-local addresses at dial time (SSRF and DNS-rebinding defence) and never follows redirects.
- `SMSProvider` — short text through an `sms.Gateway` (see `pkg/sms`). Bad numbers are a permanent error.
- `MultiProvider` — runs providers concurrently and joins their errors, so one failing channel never blocks a critical alert on another. Used for critical events, inline.
- `Service` — thin wrapper used by the pipeline.

## Queue (standard and background events)
- `Outbox` — turns a processed event into one `Job` per routed, configured channel. Routes are plain configuration. Critical events are never queued; `RecordDirect` only logs them. Idempotent per event and channel.
- `Store` — the queue. `MemoryStore` and `PostgresStore` (table `delivery_jobs`) share one contract test. Jobs are leased, so several instances can share a queue and a crashed worker's job is picked up again.
- `Dispatcher` — sends due jobs. Retries after 30 s doubling to 1 h, dead-letters after 5 attempts or at once on `ErrPermanent`, applies the daily cap (`WithDailyCap`, SMS only by default, never to critical).
- `Targets` — where each channel delivers. Jobs hold the text, never the target: addresses and URLs are read from configuration at send time.
- `RedactError` — URLs reduced to their host and a bounded length, used for every stored or logged delivery error.

Tests: provider behaviour, the store contract (memory; PostgreSQL under `-tags integration` with `TEST_DATABASE_URL`), outbox routing and idempotency, backoff, dead-letter, cap, redaction.
