# notification

Domain types and rules for notification requests.

- `NotificationRequest`, `AgentInfo` — request model.
- `NotificationService` — interface; `ErrInvalidRequest` marks caller errors (HTTP 400).
- `Limits` / `ValidatingService` — validation for untrusted callers: https-only webhook URLs on an allow-list, plain email addresses (optionally domain-restricted), single-line titles, size caps.
- `impl` — default service that hands requests to the delivery providers.
