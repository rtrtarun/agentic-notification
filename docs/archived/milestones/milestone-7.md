# Milestone 7: Concrete Delivery Provider

## Architecture

- Keep the core delivery boundary provider-agnostic.
- Add a concrete SMTP provider as the first real channel implementation.
- Preserve the possibility of adding Slack, SMS, or Novu providers later without changing the service interface.

## Tasks

- Add an SMTP-backed delivery provider.
- Wire the server to use the SMTP provider when configured.
- Keep the stub provider as a fallback for local or unconfigured environments.

## Implementation

- The delivery package now exposes a concrete SMTP provider that satisfies the shared Provider interface.
- The server chooses the SMTP provider when SMTP configuration is available; otherwise it uses the stub provider.

## Tests

- Verify the SMTP provider reports an error when not configured.
- Keep the existing delivery service tests passing.

## Review checklist

- [x] A concrete delivery provider exists.
- [x] The abstraction remains provider-agnostic.
- [x] Backend tests pass.
