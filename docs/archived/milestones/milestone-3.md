# Milestone 3: GitHub Webhook Ingestion, Normalization, Persistence

## Architecture

- Accept GitHub webhook events through a dedicated endpoint.
- Normalize the incoming payload into a generic internal event shape.
- Persist normalized events in an in-memory repository for the current milestone.

## Tasks

- Add a /webhooks/github endpoint.
- Parse X-GitHub-Event and normalize the payload.
- Persist the normalized event for later processing.

## Implementation

- The server now accepts GitHub webhook requests and stores normalized events.
- The in-memory repository is intentionally simple so the milestone stays focused.

## Tests

- Normalize a GitHub pull request payload into an internal event.
- Persist a webhook event through the handler.

## Review checklist

- [x] Webhook endpoint accepts GitHub events.
- [x] Events are normalized into a generic internal form.
- [x] Persisted events can be listed by the repository.
