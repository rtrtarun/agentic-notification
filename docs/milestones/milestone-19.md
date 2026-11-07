# Milestone 19: Channels and Integrations

## Goals
Show, without opening a server shell, where critical notifications go and whether the GitHub webhook is safely connected; let an operator prove a channel works.

## Architecture decisions
- Two small read-oriented packages: `pkg/channels` (describe, masked test send) and `pkg/integrations` (GitHub state from settings and stored events). See their READMEs.
- **Channels are still configured by environment variables.** Storing webhook URLs and SMTP passwords in the database would need encryption at rest and a key story; that belongs in the production hardening milestone, not here. The screen says how to set each one.
- **Nothing sensitive is returned.** Emails are masked, Slack and Teams URLs are reduced to their host, the webhook secret is reported only as "set". Tests assert the response body never contains the secret parts.
- **Test sends are limited:** one per channel per 10 seconds (429 with `Retry-After`), each message labelled `[TEST]`, and a Slack test never emails anyone (only the chosen channel's target is put in the request). Delivery errors are logged and answered with a generic 502.
- The integration status mirrors `webhookAuthMiddleware` exactly: secret set means ready; no secret but `ALLOW_UNSIGNED_WEBHOOKS` means unsigned; otherwise blocked.
- The provider abstraction (`NotificationProvider`, Novu) is unchanged; these packages talk to `delivery.Service`.

## API
All need the API key.

| Method and path | Result |
|---|---|
| `GET /api/channels` | `{channels: [{id, name, configured, target, receives, setup}]}` |
| `POST /api/channels/{id}/test` | 200 `{ok:true}`; 404 unknown; 409 not configured; 429 too soon; 502 delivery failed |
| `GET /api/integrations` | `{integrations: [{id, name, status, webhookPath, signing, secretConfigured, eventsReceived, lastEventAt, lastEventType, eventTypes[], setup}]}` |

## Database
None.

## Frontend
`#/channels`: a card per channel with status, masked target, what it receives, set-up variable and **Send a test** (plain reasons on failure). `#/integrations`: GitHub card with status explained, copyable webhook address built from the service address, verification, event counts, latest event, event types seen, and connection steps. Demo mode shows sample values and says nothing was sent.

## Risks
- Only GitHub is listed because it is the only inbound source. More sources will add entries.
- Counts come from the newest 1000 events.
- The rate limit is per process; with several replicas each allows its own test.

## Tests
Go: `pkg/channels` (masking, unconfigured and partial email, only-chosen-channel sends, error mapping, rate limit per channel with a fake clock), `pkg/integrations` (status rules, counts, latest, empty arrays, cap), `pkg/server` (no secret leakage, status codes, generic failure body, method and path validation, auth, integration states). Web: `lib/channels.test.ts`. 159 Vitest tests; Go module passes.

## Acceptance criteria
- [x] Channel and integration state is visible and accurate; secrets are never returned.
- [x] A test message reaches only the chosen channel, and cannot be spammed.
- [x] Misconfiguration (blocked or unsigned webhook) is stated in plain language.
