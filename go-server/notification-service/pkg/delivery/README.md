# delivery

Layer 3: sends notifications through `Provider` implementations.

- `Provider` — interface (`Send`, `Name`). Swap in Novu or others behind it.
- `SMTPProvider` — email. Recipients are parsed (no CR/LF), subject and sender are flattened to one line.
- `WebhookProvider` — Slack/Teams POST. `NewWebhookProvider(true)` refuses loopback, private and link-local addresses at dial time (SSRF and DNS-rebinding defence) and never follows redirects.
- `MultiProvider` — runs providers concurrently and joins their errors, so one failing channel never blocks a critical alert on another.
- `Service` — thin wrapper used by the pipeline.
