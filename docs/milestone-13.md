# Milestone 13: Security and Production Hardening

**Status: complete — all five steps implemented and tested.**

## Goals

1. Reject forged GitHub webhooks (HMAC signature verification).
2. Bound request size and sanitise error responses.
3. Replace `log.Printf` with structured `log/slog`.
4. Propagate `context.Context` through all I/O.
5. Close test gaps in untested packages.

Delivered in this order, one commit per step, each runnable and tested.

## Architecture Decisions

- **Signature check is middleware**, not handler logic. It reads the raw body (capped), verifies `X-Hub-Signature-256` with `hmac.Equal` (constant time), then restores the body for the handler. The handler currently decodes then re-marshals JSON; signatures are over the raw bytes, so verification must happen first.
- **Fail closed:** if `GITHUB_WEBHOOK_SECRET` is unset the webhook returns 503 unless `ALLOW_UNSIGNED_WEBHOOKS=true` (local dev only; logs a warning at startup).
- **`log/slog`** with a JSON handler, built in `main` and injected. Keys: `request_id`, `event_id`, `thread_id`, `attention_class`, `decision_status`. No payloads or recipients logged.
- **Context:** add `ctx context.Context` as first parameter to `event.Repository`, `delivery.Provider`/`Service`, and `pipeline.Handle`; use `QueryContext`/`ExecContext` and `http.NewRequestWithContext`. Request context flows from the HTTP handler. Breaking change to interfaces, so done as its own step with fakes updated.
- **Errors to clients are generic** (`"internal error"`); detail goes to logs. Currently `pipeline.Handle` errors are returned verbatim.
- No new dependencies (stdlib only).

## Steps

| # | Step | Files |
|---|---|---|
| 1 ✅ | Webhook HMAC verification + body limit (1 MiB) | `pkg/server/webhook_auth.go`, `pkg/config/config.go`, `pkg/server/server.go` |
| 2 ✅ | Generic client errors; request-id middleware | `pkg/server/server.go`, `pkg/util/util.go` |
| 3 ✅ | `log/slog` everywhere | `cmd/notification-service/main.go`, `pkg/server/server.go` |
| 4 ✅ | Context propagation | `pkg/event`, `pkg/delivery`, `pkg/pipeline`, `pkg/notification/impl`, `pkg/learning` |
| 5 ✅ | Tests for `config`, `db` (migration SQL via fake executor), `notification/impl` | `*_test.go` |

## Database Changes

None.

## APIs

| Route | Change |
|---|---|
| `POST /webhooks/github` | Requires valid `X-Hub-Signature-256` (401 on missing/invalid, 413 over limit, 503 if unconfigured) |
| All routes | Errors are `{"error": "<generic message>"}`; `X-Request-ID` header added to responses |

New env vars: `GITHUB_WEBHOOK_SECRET`, `ALLOW_UNSIGNED_WEBHOOKS` (default false), `MAX_BODY_BYTES` (default 1048576), `LOG_LEVEL` (default info).

## Folder Changes

```
pkg/server/webhook_auth.go      # signature middleware
pkg/server/webhook_auth_test.go
pkg/config/config_test.go
pkg/db/migrate_test.go
```

## Risks

- **Breaking existing webhooks:** GitHub hooks configured without a secret will start failing. Mitigation: document setting the secret in GitHub; `ALLOW_UNSIGNED_WEBHOOKS` for local dev.
- **Interface churn from adding `ctx`:** touches many signatures. Mitigation: dedicated step, compiler-driven, tests must pass before commit.
- **Body restore bug** (handler reads empty body): covered by a middleware test that asserts the handler sees the original bytes.
- **Implemented (step 1):** `pkg/server/webhook_auth.go`; `GITHUB_WEBHOOK_SECRET`, `ALLOW_UNSIGNED_WEBHOOKS`, `MAX_BODY_BYTES` in `pkg/config`; 10 tests in `webhook_auth_test.go`. `docker-compose.yml` sets a local dev secret.
- **Implemented (steps 2-5):** `requestid.go` (request ID middleware, `logFor`), generic 500s with `notification.ErrInvalidRequest` mapped to 400, `log/slog` JSON logging built in `main` (`LOG_LEVEL`), `context.Context` on `event.Repository`, `delivery.Provider/Service`, `pipeline.Handle`, `notification.NotificationService` and `learning.Summarize`, `db.Migrate/Connect`. New `pkg/mail` provides cancellable SMTP (stdlib `smtp.SendMail` cannot be cancelled). `Server.Run(ctx)` now shuts down gracefully on SIGINT/SIGTERM and sets `ReadHeaderTimeout`. Tests added for `config`, `db`, `notification/impl`, `mail`, request IDs, error mapping, shutdown and cancellation.
- **Replay attacks:** signatures don't prevent replay of a captured valid delivery. Mitigation: track `X-GitHub-Delivery` IDs in the deduper; noted as follow-up, not in this milestone.

## Tests

- Valid signature accepted; wrong secret, missing header, malformed header (`sha256=` prefix absent, bad hex) rejected with 401.
- Signature over the raw body, not re-marshalled JSON (whitespace/key-order variants).
- Oversized body → 413; handler sees the full original body on success.
- Unconfigured secret → 503; `ALLOW_UNSIGNED_WEBHOOKS=true` → accepted.
- Pipeline error returns generic message, logs the detail.
- Cancelled context aborts repository and delivery calls.
- Config rejects invalid `MAX_BODY_BYTES` / `LOG_LEVEL`.

## Acceptance Criteria

- [x] Unsigned or wrongly signed webhooks never reach the pipeline.
- [x] Verification uses constant-time comparison on the raw body.
- [x] No `log.Printf` remains; logs are JSON with stable keys.
- [x] All I/O methods take `context.Context`; cancellation is tested.
- [x] Client errors never leak internal error text.
- [x] `config`, `db`, `notification/impl` have tests.
- [x] `gofmt`, `go vet`, `go test -race` pass; CHANGELOG, README and standards doc updated (remove the **[Gap]** markers as each is closed).

## Out of scope

Rate limiting, replay protection, secret rotation, TLS termination, Redis/NATS. Each is a candidate for a later milestone.
