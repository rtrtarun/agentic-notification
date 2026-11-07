# HTTP contract

The files in this folder are the shared contract for the Go service and the Java service. Each service has a test that reads these files, starts itself, replays the requests and checks the answers. If a service disagrees with a file, that service's build fails, so "the two services behave the same" is checked by tests rather than by reading code.

The contract covers milestones 34 to 36: `/health`, `/webhooks/github`, `/events`, `/notify`, `/api/rules`, `/api/policies`, `/api/deliveries`, and accounts (`/api/auth/status`, `/api/auth/signup`, `/api/auth/login`, `/api/users`). It grows with each parity milestone. Not covered because the Java service does not have them yet: rule `payloadMatch` and `channels`.

## Environment both services run with

| Setting | Value |
| --- | --- |
| `API_KEY` | `contract-key` |
| `GITHUB_WEBHOOK_SECRET` | `contract-secret` |
| `RULES_PATH` | `go-server/notification-service/config/rules.yaml` (shared, one copy) |
| `ATTENTION_CLASSIFY` | `false` (no model; the contract is the deterministic layer) |
| `ATTENTION_BUDGET_PER_HOUR` | `0` |
| `AUTH_BOOTSTRAP_EMAIL` / `AUTH_BOOTSTRAP_PASSWORD` | `admin@contract.test` / `a long contract admin password` |
| `AUTH_SIGNUP` | `approval` (people may ask for an account; an admin approves) |
| Database | none: events are kept in memory |
| Channels | none: critical events are delivered to a logging provider |

Every file starts a fresh service, so files do not depend on each other. Steps inside a file run in order and share state (dedup, stored events).

## File format

```json
{
  "name": "what this file proves",
  "steps": [
    {
      "name": "what this step proves",
      "request": {
        "method": "POST",
        "path": "/webhooks/github",
        "headers": { "X-GitHub-Event": "push" },
        "body": { "any": "json" },
        "rawBody": "text sent as is (instead of body)",
        "sign": true
      },
      "expect": {
        "status": 202,
        "json": { "subset": "of the answer" },
        "absent": ["top-level keys that must not exist"],
        "present": ["top-level keys that must exist"],
        "absentInEach": { "events": ["keys no element of the array may have"] }
      },
      "save": { "variable": "topLevelKeyOfTheAnswer" }
    }
  ]
}
```

- `sign: true` adds `X-Hub-Signature-256`, the HMAC-SHA256 of the exact body bytes with `contract-secret`.
- `json` is a **subset** match: every key written must be present and equal; extra keys in the answer are fine. Arrays must have the same length, and each element is a subset match of the same position.
- `save` keeps a value of the answer by dotted path (`"rules.0.id"`; a number selects an array element); later `path` values and request bodies may use `{{variable}}`.
- Fields that vary between runs (`createdAt`, generated ids) are never compared.

## Running

- Go: `go test ./pkg/server -run TestContract` in `go-server/notification-service`.
- Java: `mvn verify` in `java-server/notification-service` (the contract test is part of the suite). Set `CONTRACT_BASE_URL` to replay the same files against an already running service instead.
