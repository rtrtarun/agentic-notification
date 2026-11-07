# Milestone 32: Novu provider, cursor pagination, tracing and strict STARTTLS

## Goals
- Deliver email through Novu behind the existing delivery abstraction, with SMTP as the fallback, without ever sending a recipient the same message twice.
- Page through events and notifications with a cursor that does not shift when new items arrive, keeping the numbered pages working.
- Make each request traceable end to end with W3C `traceparent` and timed spans, without pulling in a tracing SDK.
- Let production deployments refuse to send mail over a plaintext SMTP connection.

## Architecture decisions
- **Novu is one more `delivery.Provider`** (`pkg/delivery/novu.go`). `NOVU_API_KEY` turns it on; `NOVU_API_URL` defaults to Novu's cloud; `NOVU_WORKFLOW_ID` names the workflow (default `attention-orchestration`). It triggers that workflow once per email recipient (`POST /v1/events/trigger`, header `Authorization: ApiKey ...`, subscriber and email set to the recipient), with title, message, agent and group as the payload. Slack, Teams and SMS keep their own providers: Novu owns email only. Nothing outside `pkg/delivery` and the one wiring function knows Novu exists, so replacing it is writing another provider.
- **Exactly one provider owns email.** `emailProvider(cfg)` returns SMTP alone, Novu alone, or `FallbackProvider(novu, smtp)` when both are configured. A fallback tries SMTP only after Novu fails, and reports both errors if both fail. Putting Novu and SMTP side by side in the existing fan-out provider would have emailed every recipient twice. The channels screen and the email routing treat Novu as a configured email channel.
- **Failures are classified for the retry queue.** A 4xx from Novu (bad key, unknown workflow, bad subscriber) is `ErrPermanent`, so it dead-letters and falls back immediately rather than retrying; 429 and 5xx are transient. Errors carry the status only, never the key. Redirects are not followed, so a redirect cannot carry the key to another host.
- **Cursor pagination** (`pkg/pagination`). A cursor is the position of the last item received, `(created_at, id)`, encoded opaquely; the next page is everything strictly older, ties broken by ID. `GET /events` and `GET /api/notifications` accept `?cursor=` and return `nextCursor` whenever more remains. Without a cursor behaviour is unchanged: `GET /api/notifications` still honours `page` and `pageSize`, and a cursor from a numbered page continues exactly where it stopped. The inbox cursor needs the default newest-first sort and answers 400 for another sort or a malformed cursor. A test pages through a list while new events arrive and sees each older event exactly once.
- **Tracing** (`pkg/telemetry`). A request keeps the trace it arrived with (a valid `traceparent`; an invalid or all-zero one is ignored, never trusted) or starts a new one, and the response carries the request span's `traceparent`. The pipeline records spans `pipeline.Handle`, `policy.Evaluate`, `dedup.Check`, `attention.Classify` and `delivery.Send`, each with its own ID, parent, duration and error, written as structured debug log records with `trace_id` and `span_id`. A critical event produces no attention span. This is deliberately a small slice of OpenTelemetry (context propagation and timing); it does not export to a collector, and a `*Tracer` that is nil does nothing.
- **Strict STARTTLS.** `SMTP_REQUIRE_STARTTLS=true` makes `mail.SendWith` return `ErrSTARTTLSRequired` if the server does not offer STARTTLS, before any credentials or message are sent. The default stays permissive so local development against a plain test server keeps working.

## Database changes
None.

## APIs
`GET /events` and `GET /api/notifications` gain `cursor` and return `nextCursor`. Every response gains a `traceparent` header. New settings: `NOVU_API_KEY`, `NOVU_API_URL`, `NOVU_WORKFLOW_ID`, `SMTP_REQUIRE_STARTTLS`.

## Risks
| Risk | Handling |
|---|---|
| Duplicate email when both Novu and SMTP are configured | One owning provider; SMTP runs only after Novu fails (tested) |
| A bad Novu key silently drops email | A 4xx is permanent and falls back to SMTP at once, and both errors reach the delivery log; with no SMTP the job dead-letters visibly |
| Cursors change ordering for existing clients | Only the tie order of events created in the same instant (now by ID); `page` and `pageSize` are untouched |
| Trace data leaks request contents | Spans carry names, IDs, durations and errors only |
| Strict STARTTLS breaks a deployment | It is off by default and the failure names the cause |

## Tests
`pkg/delivery`: Novu validation and defaults, one trigger per recipient with the right body and header, no-op without email recipients, status classification (400, 401, 403, 404, 422 permanent; 429, 500, 503 transient), a failing recipient does not stop the others, redirects not followed, unreachable gives a generic error, the key never appears in an error; fallback only on failure, both errors reported, no retry after cancellation. `pkg/server`: one provider owns email in every combination, the channels screen counts Novu as configured email. `pkg/mail`: strict mode refuses a server without STARTTLS and sends nothing, never sends credentials in plaintext, the default still delivers. `pkg/pagination` (100 percent): round trip, 6 malformed shapes, ordering and tie rule. `pkg/server`: five events in pages of two with new events arriving mid-paging, legacy calls unchanged, bad cursors refused, numbered pages still work, cursor with another sort refused. `pkg/telemetry` (98.5 percent): header parsing (13 malformed cases), parent and child spans, duration, errors logged once, nil safety, the middleware continuing or replacing a caller's trace. `pkg/pipeline`: all five spans join the caller's trace, critical delivery is traced and never classified, a redelivery is not a failed span. `pkg/server`: `traceparent` on authenticated, failing and unknown routes. `pkg/config`.

## Acceptance criteria
- [x] `NovuProvider` behind `delivery.Provider`, configured with `NOVU_API_KEY` and `NOVU_API_URL`, with fallback to SMTP (and the webhook and SMS providers unchanged).
- [x] Cursor pagination on `GET /events` and `GET /api/notifications`, compatible when `cursor` is omitted.
- [x] `traceparent` propagation and span timing across the pipeline, policy, dedup, attention and delivery.
- [x] `SMTP_REQUIRE_STARTTLS` rejects plaintext SMTP.
- [x] `gofmt`, `go vet` (also `-tags integration`), `go test -cover ./...` pass locally.
- [ ] Verified against the real Novu service or a real STARTTLS server: **not done.** Novu was exercised against a local server following its documented trigger API; STARTTLS refusal against a fake without the extension. The accepting path with TLS has no automated test.
- [ ] Spans exported to an OpenTelemetry collector: **not built**, by design.

## Known limits
- Novu's request shape was written from its public API documentation and has not been run against Novu; confirm the workflow identifier and that subscribers are created on trigger in your Novu environment.
- Inbox cursors only work with the newest-first sort; the priority, oldest and updated sorts still use numbered pages.
- `GET /events` reads the newest 1000 events, so a cursor cannot reach further back than that.
- Spans are log lines at debug level; turning on debug logging in production is a deliberate choice.
