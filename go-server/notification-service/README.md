# Go Notification Service

Run locally:

```bash
cd go-server/notification-service/cmd/notification-service
go run main.go
```

Environment variables:

- `PORT`
- `API_KEY` - when set, the service requires the `X-API-Key` header for protected routes
- `DATABASE_URL` - PostgreSQL connection string; omit to use in-memory storage
- `RULES_PATH` - path to YAML policy rules file; omit to use built-in defaults
- `GITHUB_WEBHOOK_SECRET` - secret used to verify `X-Hub-Signature-256` on `POST /webhooks/github`; set the same value in the GitHub webhook settings. Without it the endpoint returns 503
- `ALLOW_UNSIGNED_WEBHOOKS` - `true` accepts unsigned webhooks when no secret is set (local development only; default `false`)
- `MAX_BODY_BYTES` - maximum webhook request body size (default `1048576`)
- `DEFAULT_NOTIFY_EMAIL` - comma-separated email recipients for critical alerts
- `SMTP_HOST`
- `SMTP_PORT`
- `SMTP_USERNAME`
- `SMTP_PASSWORD`
- `SMTP_SENDER`
- `SLACK_WEBHOOK_URL`
- `TEAMS_WEBHOOK_URL`

Health check:

```bash
curl http://localhost:8080/health
```

POST `/notify` with JSON:

```json
{
  "agent": {"name":"agent1","owner":"dev","group":"ops"},
  "title":"Alert",
  "message":"Hello",
  "emailRecipients":["user@example.com"],
  "slackWebhookUrls":["https://hooks.slack.com/..."],
  "teamsWebhookUrls":["https://outlook.office.com/webhook/..."]
}
```
