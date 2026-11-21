# Local supporting services

`deploy/docker-compose.local.yml` starts only the supporting services, for running a service from source on your machine:

- PostgreSQL 16 (`attention` / `attention` on port 5432), for events, rules, the delivery queue and accounts
- Ollama, for the optional local language model behind Attention Intelligence (`ATTENTION_PROVIDER=ollama`, Go service only)

```bash
docker compose -f deploy/docker-compose.local.yml up -d
```

Then run the Go service (`go run ./cmd/notification-service` in `go-server/notification-service`; it migrates the schema) and, if you want, the Java service and the console.

To run everything in containers instead (PostgreSQL, Redis, the Go service and the Java service), use the root `docker-compose.yml`: `docker compose up --build`. Going live, secrets and releases are in [docs/deployment.md](../docs/deployment.md).
