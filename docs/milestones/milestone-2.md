# Milestone 2: Authentication, Configuration, Logging, Observability

## Architecture

- Keep authentication lightweight with a configurable API key header for internal routes.
- Validate configuration values early so bad environment settings fail fast.
- Record request and notification counts in memory for a minimal observability baseline.

## Tasks

- Add configuration validation for port and SMTP values.
- Require an API key header when configured.
- Log request lifecycle details.
- Expose basic metrics via /metrics.

## Implementation

- Config loads with early validation errors for invalid numeric settings.
- Protected routes require X-API-Key when API_KEY is set.
- Request and notification counters are incremented during normal handling.

## Tests

- Config rejects invalid port values.
- Auth middleware rejects missing API keys.
- Health endpoint remains accessible and structured.

## Documentation

- Document the new API key behavior and local environment variables.

## Review checklist

- [x] Configuration values fail fast.
- [x] Protected endpoints require API key when configured.
- [x] Basic metrics are available at /metrics.
