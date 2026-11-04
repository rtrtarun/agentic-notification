# server

HTTP interface: routing, middleware and handlers. No business logic here.

Middleware order: request ID, CORS, logging/metrics, auth, request timeout. `/health` is unauthenticated; `/notify`, `/feedback`, `/metrics` need `X-API-Key` (fails closed); `/webhooks/github` needs a valid HMAC signature. Every body is size-limited. Logs use `http_status` (not `status`) to avoid clashing with log-backend reserved fields.
