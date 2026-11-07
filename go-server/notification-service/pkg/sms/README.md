# sms

The one place the service talks to a carrier. The delivery layer depends only on `Gateway`, so a vendor can be swapped without touching notification or attention logic.

- `Gateway` — `Name()` and `Send(ctx, to, text)`.
- `Mock` — local development. Sends nothing, logs a masked number, keeps messages for tests.
- `HTTPGateway` — posts JSON to any SMS API. URL, headers (credentials) and a body template with `{{to}}` and `{{text}}` come from configuration. Redirects are not followed; errors never echo the response body or the URL.
- `SNSGateway` — Amazon SNS Publish using Signature V4 implemented here (no AWS SDK). Access keys or a session token; role-based credentials are not supported.
- `ValidNumber` / `MaskNumber` — E.164 check and masking (`+14*******00`).

Configured in `pkg/server/delivery_setup.go` from `SMS_GATEWAY` (`mock`, `http`, `sns`). Adding a vendor means one new type implementing `Gateway`.

Tests: validation, masking, mock, template escaping, error hygiene, and the SigV4 signer against the AWS `get-vanilla` test vector.
