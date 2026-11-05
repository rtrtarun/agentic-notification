# integrations

Read-only state of inbound integrations. `GitHub(settings, events)` reports `ready` (secret set, signature required), `unsigned` (`ALLOW_UNSIGNED_WEBHOOKS`, development only) or `blocked` (no secret, requests get 503, matching the webhook middleware), with counts, the latest event and the event types seen. It reports whether a secret is set, never the secret.
