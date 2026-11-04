# Comparison: Azure Notification Hubs

[Azure Notification Hubs](https://azure.microsoft.com/en-us/products/notification-hubs) is a push engine. This platform decides whether a notification should be sent at all. They overlap only at delivery, and Notification Hubs could sit behind our `NotificationProvider` interface as one more provider.

Facts about Notification Hubs below come from Microsoft's public product page and REST reference (fetched 2026-11-04). They describe the service as documented, not as observed.

## How they differ

| | Azure Notification Hubs | Attention platform |
|---|---|---|
| Job | Fan one message out to many devices on APNs, FCM, WNS, ADM and other push services | Decide whether, when, where and how a person should be interrupted |
| Targeting | Device registrations or installations, tags and templates | Policy rules, attention class, deduplication and threading |
| Decision logic | None. It sends what the back end tells it to | Layer 1 is deterministic rules; Layer 2 (AI) is not built yet |
| Critical events | No concept | Always sent, never merged, never seen by AI |
| Delivery | Native mobile push at scale | Provider interface with webhook, SMTP and Teams today; Novu is planned |
| Feedback | Per-message telemetry and PNS feedback (Standard tier) | Opened, dismissed, always, never, undo and replies, kept as training signals |
| Where it sits | Layer 3 (delivery) | Layers 1, 2 and 4, above any delivery provider |

## Notification Hubs request details

Every call carries an `Authorization` header (a shared access signature token) and `x-ms-version`.

| Operation | Request | Key headers and body | Responses |
|---|---|---|---|
| Create or overwrite an installation | `PUT https://{namespace}.servicebus.windows.net/{hub}/installations/{id}?api-version=2015-01` | JSON: `installationId`, `platform` (APNS, WNS, MPNS, ADM or GCM), `pushChannel`, optional `userID`, `tags`, `templates` | 200 queued, 400 malformed, 401 bad key, 403 too many installations |
| Send an APNS notification | `POST .../{hub}/messages/?api-version=2015-01` | `ServiceBusNotification-Format: apple`, optional `ServiceBusNotification-Tags` and `ServiceBusNotification-Apns-Expiry`; body is Apple's `{"aps":{"alert":"..."}}` | 200 sent, 400, 401, 403 quota or size, 413 over 64 KB. `Location` (message id) on the Standard tier only |
| Direct send to one device | `POST .../{hub}/messages/?direct&api-version=2015-04` | `ServiceBusNotification-DeviceHandle` carries the device token; format is one of `windows`, `apple`, `gcm`, `windowsphone`, `adm`, `nokiax`, `baidu` | 201 sent, same errors |

Microsoft notes that Notification Hubs reached Android through Google's legacy FCM protocol, which Google said it would stop supporting on 2024-06-20, with migration to FCM v1 in progress at the time of the page.

## Our equivalent requests

| Purpose | Request | Auth |
|---|---|---|
| Ingest a GitHub event | `POST /webhooks/github` with `X-GitHub-Event`, `X-GitHub-Delivery` and `X-Hub-Signature-256` | HMAC signature |
| Send a notification directly | `POST /notify` | `X-API-Key` |
| Read what was decided | `GET /events?limit=N` | `X-API-Key` |
| Give feedback | `POST /feedback` | `X-API-Key` |
| Metrics, health | `GET /metrics`, `GET /health` | key, none |

Full contracts: [architecture.md](architecture.md) section 11.

## Ideas worth borrowing

| Idea | Their version | Our status |
|---|---|---|
| Idempotent writes keyed by id | Installations overwrite by id | Done: delivery-id idempotency and `ON CONFLICT DO NOTHING` |
| Per-message telemetry | A message id and a status lookup | Not built. Would give delivery visibility |
| A hard message size cap | 64 KB | Not set for `/notify` payloads; body limit is 1 MiB overall |
| Using it as a provider | n/a | A `NotificationHubsProvider` would implement `NotificationProvider`; not built |
