# config

`LoadConfig` reads and validates environment variables (see the service README for the full table). Defaults fail closed: no API key means no access, CORS is closed, webhooks must be signed. `WebhookHostAllowList` combines the configured allow-list with the hosts of operator-configured webhook URLs.
