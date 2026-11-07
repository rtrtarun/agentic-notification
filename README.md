# Attention Orchestration Platform

[![CI](https://github.com/rtrtarun/agentic-notification/actions/workflows/ci.yml/badge.svg?branch=feature%2Fattention-models)](https://github.com/rtrtarun/agentic-notification/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=black)
![TypeScript](https://img.shields.io/badge/TypeScript-strict-3178C6?logo=typescript&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)
![Python SDK](https://img.shields.io/badge/Python%20SDK-stdlib%20only-3776AB?logo=python&logoColor=white)
![License](https://img.shields.io/badge/license-not%20chosen%20yet-lightgrey)

> **Notifications are cheap. Human attention is expensive.** This platform decides *when, where, whether and how* someone should be interrupted, and sits above delivery providers such as email, Slack, SMS and Novu.

Status: built through **milestone 33** on branch `feature/attention-models`. Several parts have so far run only against local fakes; [what is proven and what is not](docs/implementation-sync.md) is written down, not hidden.

> **Screenshots:** none are committed yet. The console has a complete **demo mode** that needs no backend: run it locally (see [Running the console](#running-the-console)) or, once GitHub Pages is enabled ([deployment guide](docs/deployment.md)), open `https://rtrtarun.github.io/agentic-notification/`.

---

## Table of contents

1. [About the project](#about-the-project)
2. [Key features](#key-features)
3. [Tech stack](#tech-stack)
4. [Project structure](#project-structure)
5. [Getting started](#getting-started)
6. [Usage examples](#usage-examples)
7. [Testing](#testing)
8. [Documentation map](#documentation-map)
9. [Roadmap and known issues](#roadmap-and-known-issues)
10. [Contributing](#contributing)
11. [License](#license)
12. [Acknowledgements and support](#acknowledgements-and-support)

---

## About the project

### What it does

Events arrive from GitHub webhooks and from AI agents. The platform classifies each one as **critical**, **standard** or **background**, decides whether anyone should be interrupted, and either delivers it, holds it for a digest, merges it into an earlier one, or records it. Every decision is stored with a reason, so "why didn't I see this?" always has an answer.

```mermaid
flowchart LR
    subgraph In["Sources"]
        GH["GitHub webhooks"]
        AG["AI agents<br/>REST, MCP, Python SDK"]
    end
    subgraph L1["Layer 1: Event and policy (deterministic, no AI)"]
        P["Rules, predicates,<br/>dedup, threading,<br/>preferences, overrides,<br/>attention budget"]
    end
    subgraph L2["Layer 2: Attention intelligence (AI only here)"]
        A["Classify, digest,<br/>explain, suggest<br/>(heuristic, Ollama or Claude)"]
    end
    subgraph L3["Layer 3: Delivery"]
        D["Queue with retries:<br/>email (SMTP or Novu),<br/>Slack, Teams, SMS"]
    end
    subgraph L4["Layer 4: Feedback"]
        F["Opened, dismissed, undo,<br/>always, never, replies"]
    end
    GH --> P
    AG --> P
    P -->|"critical: inline, never touches AI"| D
    P -->|"standard and background"| A
    A --> D
    D --> F
    F -.->|"suggestions an admin reviews"| P
```

**The one rule that never bends:** a *critical* event (security alert, outage, failed payment) never touches AI, is never merged, muted, delayed, budgeted or deferred, is delivered to every configured channel, and cannot be created by an agent describing its own event as severe. Only a rule makes an event critical. Every layer has tests that pin this.

### Why it is useful

Most notification tools are good at *sending*. Few help decide *whether* to send. As teams add automation and AI agents, the volume of "something happened" grows faster than anyone's capacity to read it. This platform is the layer that protects attention: it quiets noise, keeps what matters loud, explains itself in plain words, and learns from what people dismiss or ask to always see, without ever acting silently on that learning.

Typical uses: an on-call engineer who wants security and outage alerts at once but not every star and label change; a team running AI agents who wants to see a run as a timeline and be alerted when one keeps retrying; a platform team that wants a self-hostable alternative to wiring each tool to each channel.

### What it looks like

The console (`apps/web`) has 15 screens, five colour themes and nine languages. A few of the screens:

| Screen | What you see |
|---|---|
| **Inbox** | Notifications with read, archive, snooze and bulk actions; a detail drawer showing the rule, the decision and its timeline |
| **Digests** | What was held back, with the writer labelled "rules" or the model's name |
| **Traces** | An AI agent run as a timeline: duration bars, retries, tokens, cost and the decision for each step |
| **Rules** | Edit rules live, dry-run an example, and review suggestions made from feedback |
| **Channels** | Where messages go, test sends, and the delivery queue with dead letters |

---

## Key features

**Decide**
- Rules that match source, type, repository and **payload content** (`metadata.attempt > 3`), edited at run time, with locked critical rules and strict YAML (a misspelt key is an error, not a wider rule)
- Per-rule **channel routing**; quiet hours and mutes; **always** and **never** notify choices that are visible and reversible
- A rolling **attention budget** (default 10 standard interruptions per hour); events over it are held for the digest, never dropped
- Deduplication and threading, in process or shared through Redis

**Understand**
- Plain-language **explanations** of every decision
- **Digests** for the workspace and for each person (stored and readable; not yet delivered to the person)
- A **learning engine** that turns feedback into suggestions (adaptive, demote, promote, override) which an admin applies or dismisses; it never changes anything itself and never touches critical or locked rules
- Optional **Ollama or Claude** behind the same interfaces as the built-in heuristic, with a timeout, a rate cap, a circuit breaker, redaction of what is sent and strict validation of what comes back; any failure falls back to the heuristic

**Deliver**
- A durable queue with retries, back-off and dead letters; email through **SMTP or Novu** (one owner, SMTP as fallback), Slack, Teams, SMS (mock, HTTP or Amazon SNS); optional strict STARTTLS
- An **asynchronous attention worker** (opt in) that takes the model off the ingest path and releases events held for quiet hours

**Agents (AgentPulse)**
- `POST /api/events` for agent steps with `traceId`, `parentEventId` and telemetry; runs shown as traces
- An **MCP server** (stdio and HTTP) with `publish_event`, `get_recent_events`, `notify_user`, `explain_decision`
- A dependency-free **Python SDK**

**Operate**
- Accounts, roles, sessions, CSRF and OpenID Connect; the API key still works for services
- Prometheus metrics, structured logs, request IDs, W3C `traceparent` and span logs, retention, graceful shutdown
- Cursor pagination; Docker Compose and Kubernetes manifests; GitHub Pages and GHCR workflows

---

## Tech stack

| Area | Technology |
|---|---|
| Backend | Go 1.26 (module `go 1.21`, toolchain pinned), `net/http`, Clean Architecture |
| Frontend | React 18, TypeScript (strict), Vite, hand-written CSS with design tokens, no UI framework |
| Database | PostgreSQL 16 (idempotent migrations), in-memory stores when no database is configured |
| Cache | Redis 7 (optional): shared dedup and attention budget |
| Delivery | SMTP, Novu (optional), Slack and Teams webhooks, SMS (mock, HTTP, Amazon SNS) |
| AI (optional) | Built-in heuristic by default; Ollama or Claude through small interfaces |
| Agents | MCP over JSON-RPC 2.0, REST, Python (standard library only) |
| Tests | `go test`, Vitest, `unittest`; golangci-lint and govulncheck in CI |
| Ops | Docker, Docker Compose, Kubernetes manifests, GitHub Actions |

Dependencies are deliberately few: the Go module uses `lib/pq`, `go-redis`, `golang.org/x/crypto` and `yaml.v3`; the console ships React, React Router and one font.

---

## Project structure

```text
.
├── go-server/notification-service/   The Go service
│   ├── cmd/
│   │   ├── notification-service/     The server
│   │   └── mcp-server/               MCP over stdio for Claude Desktop and Cursor
│   ├── pkg/                          One package per concern (see below)
│   ├── config/rules.yaml             Default rules (critical ones are locked)
│   └── Dockerfile
├── apps/web/                         The console (React + TypeScript)
│   └── src/{pages,features,components,lib,design}
├── sdk/python/                       AgentPulse Python helper, example and tests
├── docs/                             All documentation (start at docs/README.md)
├── deploy/  k8s/                     Local and Kubernetes deployment files
├── .github/workflows/                CI, GitHub Pages, GHCR image
├── java-server/                      The Java implementation; core parity with the Go service built in milestone 34 (the rest is planned)
├── docker-compose.yml
└── AGENTS.md                         The design brief this project is built from
```

Inside `go-server/notification-service/pkg/`:

| Group | Packages |
|---|---|
| Domain and policy | `event`, `policy`, `rules`, `dedup`, `preferences`, `overrides`, `budget`, `pagination` |
| Pipeline | `pipeline`, `agentevents`, `traces` |
| Attention (Layer 2) | `attention`, `attention/llm`, `claude`, `ollama`, `learning` |
| Delivery (Layer 3) | `delivery`, `notification`, `mail`, `sms`, `channels`, `integrations` |
| Feedback (Layer 4) | `feedback`, `inbox`, `insights` |
| Platform | `auth`, `db`, `config`, `metrics`, `telemetry`, `stream`, `mcp`, `util`, `server` |

Handlers in `server` only decode, call one service and encode; there is no SQL outside repository implementations; dependencies are injected in `server.NewServer`.

---

## Getting started

### Prerequisites

| Need | For | Version |
|---|---|---|
| Go | Running or testing the service without Docker | 1.21+ (the toolchain pinned in `go.mod` downloads itself) |
| Node.js and npm | The console | Node 20+ |
| Docker and Docker Compose | The full stack with PostgreSQL and Redis | Recent |
| Python | The SDK and its example | 3.8+ (tested on 3.12 and 3.13) |

No API keys are needed to try it. Claude, Novu, SMTP and SMS settings are optional.

### Install

```bash
git clone git@github.com:rtrtarun/agentic-notification.git
cd agentic-notification
cd apps/web && npm install && cd ../..
```

The Go service has nothing to install; `go run` fetches modules.

### Configure

The service reads **environment variables** (there is no `.env` loader, so export them or pass them inline; Docker Compose sets them for you). The ones you will use first:

| Variable | Purpose |
|---|---|
| `API_KEY` | Key for services and for the console's "connect" box. Without it protected routes answer 503 (the service fails closed) |
| `GITHUB_WEBHOOK_SECRET` | Verifies `X-Hub-Signature-256` on webhooks. `ALLOW_UNSIGNED_WEBHOOKS=true` is for local use only |
| `DATABASE_URL` | PostgreSQL. Without it everything lives in memory and is lost on restart |
| `REDIS_URL` | Optional: shares dedup and the attention budget between replicas |
| `CORS_ALLOWED_ORIGINS` | The console's origin, for example `http://localhost:3000` |
| `AUTH_BOOTSTRAP_EMAIL`, `AUTH_BOOTSTRAP_PASSWORD` | Creates the first admin once, only when no account exists |
| `NOTIFY_STANDARD_CHANNELS`, `NOTIFY_DIGEST_CHANNELS` | Which channels standard events and digests go to (for example `slack,email`) |
| `SLACK_WEBHOOK_URL`, `SMTP_HOST`/`SMTP_SENDER`, `NOVU_API_KEY`, `SMS_GATEWAY` | Delivery channels |
| `ATTENTION_PROVIDER` | `heuristic` (default), `ollama` or `claude` (with `ANTHROPIC_API_KEY`) |
| `ATTENTION_BUDGET_PER_HOUR` | Default `10`; `0` turns the budget off |

Every variable, with defaults, is documented in [the service README](go-server/notification-service/README.md); production values are in the [deployment guide](docs/deployment.md#production-reference).

### Running the service

**Fastest, no database** (data is in memory):

```bash
cd go-server/notification-service
API_KEY=dev-key ALLOW_UNSIGNED_WEBHOOKS=true CORS_ALLOWED_ORIGINS=http://localhost:3000 \
  go run ./cmd/notification-service
curl http://localhost:8080/health
```

**With PostgreSQL and Redis** (migrations run automatically at start-up, under a lock, and every statement is idempotent):

```bash
docker compose up --build postgres redis go-notification-service
```

The Java service runs on the same database (the Go service creates the schema, so it starts first): `docker compose up --build postgres redis go-notification-service java-notification-service`, then `curl localhost:8081/health`.

The API is then on `http://localhost:8080` with the key `local-dev-key` (set in the Compose file; change it for anything but local use).

### Running the console

```bash
cd apps/web
npm run dev          # http://localhost:3000
```

Choose **Try the demo** for sample data with no backend, or **Connect your service**, enter `http://localhost:8080` and your `API_KEY`. To sign in with an account instead, start the service with `AUTH_BOOTSTRAP_EMAIL` and `AUTH_BOOTSTRAP_PASSWORD`.

---

## Usage examples

All examples use the key `dev-key` against `http://localhost:8080`.

**Send a GitHub-style webhook** (a critical event is delivered at once; a star is held for the digest):

```bash
curl -X POST http://localhost:8080/webhooks/github \
  -H "X-GitHub-Event: security_advisory" -H "X-GitHub-Delivery: demo-1" \
  -H "Content-Type: application/json" \
  -d '{"repository":{"full_name":"acme/billing"}}'
```

**Publish one step of an AI agent run:**

```bash
curl -X POST http://localhost:8080/api/events \
  -H "X-API-Key: dev-key" -H "Content-Type: application/json" \
  -d '{"id":"run-1-002","traceId":"run-1","parentEventId":"run-1-001","agentName":"researcher",
       "eventType":"tool.call.failed","metadata":{"step_kind":"tool_call","tool_name":"search","attempt":1,"duration_ms":30000}}'
```

**Read runs and ask why:**

```bash
curl -H "X-API-Key: dev-key" http://localhost:8080/api/traces
curl -H "X-API-Key: dev-key" http://localhost:8080/api/traces/run-1
curl -H "X-API-Key: dev-key" http://localhost:8080/api/explain/run-1-002
```

**Page through events with a cursor** (stable while new events arrive):

```bash
curl -H "X-API-Key: dev-key" "http://localhost:8080/events?limit=20"
curl -H "X-API-Key: dev-key" "http://localhost:8080/events?limit=20&cursor=<nextCursor from the previous page>"
```

**A rule that alerts when an agent keeps retrying** (`config/rules.yaml`, or `POST /api/rules`):

```yaml
- name: agent-retries
  when:
    source: agent
    payloadMatch: [{field: metadata.attempt, op: gt, value: 3}]
  then: {attentionClass: standard, reason: an agent keeps retrying, channels: [slack]}
```

**Python SDK** (no install; see [sdk/python](sdk/python/README.md)):

```python
from agentpulse import AgentPulse

pulse = AgentPulse("http://localhost:8080", api_key="dev-key", agent_name="researcher")
with pulse.run() as run:
    run.plan(tokens_in=500, tokens_out=80, cost_usd=0.004)
    with run.tool("web_search"):          # timed; an exception becomes a failed call
        results = search(query)
```

**Claude Desktop or Cursor** (build once with `go build -o bin/mcp-server ./cmd/mcp-server`):

```json
{ "mcpServers": { "agentpulse": {
    "command": "/path/to/go-server/notification-service/bin/mcp-server",
    "env": { "AGENTPULSE_URL": "http://localhost:8080", "AGENTPULSE_API_KEY": "dev-key" } } } }
```

**Use a language model** for digests and explanations (falls back to the heuristic if it fails):

```bash
ATTENTION_PROVIDER=ollama OLLAMA_URL=http://localhost:11434 OLLAMA_MODEL=llama3.2 go run ./cmd/notification-service
curl -H "X-API-Key: dev-key" http://localhost:8080/api/attention/status
```

---

## Testing

```bash
# Go: format, vet, tests with coverage
cd go-server/notification-service
gofmt -s -l .                 # prints nothing when clean
go vet ./... && go vet -tags integration ./...
go test -cover ./...

# Go: the checks CI also runs (need a C compiler, PostgreSQL and Redis)
TEST_DATABASE_URL=postgres://attention:attention@localhost:5432/attention?sslmode=disable \
TEST_REDIS_URL=redis://localhost:6379/0 \
  go test -race -tags integration ./...

# Console
cd apps/web && npm test -- --run && npm run build

# Python SDK
cd sdk/python && python -m unittest
```

What to expect (counts from the last local run): about 680 Go test functions across 33 packages and two commands, 230 Vitest tests, 21 SDK tests. Integration tests skip themselves when `TEST_DATABASE_URL` or `TEST_REDIS_URL` is unset.

Status: locally (Windows, no C compiler, no Docker) everything above except `-race` and the integration tests passes. Those two run in [CI](.github/workflows/ci.yml), together with golangci-lint, govulncheck and an 80 percent coverage gate. **On commit `367d735` (run 42356002884) the Go, web and Python SDK jobs all passed**, including the PostgreSQL 16 and Redis 7 integration tests and `-race`; the `java-service` job fails and is unrelated to this work. The first CI runs found about 25 lint findings and two real defects that in-memory tests could not see (overrides could not be saved in PostgreSQL), which is why the integration tests exist. Some packages are below 80 percent when run without integration tests (their PostgreSQL code is covered only in CI); the table is in [implementation-sync.md](docs/implementation-sync.md). The CI badge above shows the current result.

---

## Documentation map

Everything lives in [`docs/`](docs/), grouped by what you want to do. **[docs/README.md](docs/README.md) is the full index**; the short version:

| I want to… | Read |
|---|---|
| Understand what this is and how it works | [Architecture](docs/architecture.md), [Roadmap](docs/roadmap.md) |
| Run it, deploy it, or run it in production | [Deployment](docs/deployment.md), [Runbook](docs/runbook.md) |
| Know what is built and what is proven | [Implementation status](docs/implementation-sync.md), [Changelog](docs/CHANGELOG.md) |
| See how a feature was designed and tested | The milestone record: [current](docs/milestones/README.md) (34, 35) or [archived](docs/archived/milestones/) (1 to 33) |
| Work on the console | [Frontend](docs/frontend.md) |
| Work on the Java service | [Java service](docs/java-service.md), [HTTP contract](docs/contract/README.md) |
| Build an AI agent integration | [AgentPulse](docs/agentpulse.md), [Python SDK](sdk/python/README.md), [Milestone 31](docs/archived/milestones/milestone-31.md) |
| Contribute code | [CONTRIBUTING](CONTRIBUTING.md), [Coding standards](docs/rules/coding-standards.md) |

---

## Roadmap and known issues

**Next** (details in [roadmap.md](docs/roadmap.md)):

1. **Prove it against real services**: PostgreSQL, Redis, Ollama, Claude, Novu, SNS, an OpenID provider and a real MCP client; get CI green end to end.
2. **Java parity, continued**: milestones 34 and 35 built the core, `/notify`, the rules API, the durable delivery queue and Redis dedup in Java, with a nine-file shared contract in `docs/contract`; the rest of the Go service (accounts, Attention Intelligence, feedback, learning, traces, MCP) follows milestone by milestone.
3. **SDKs for other languages** (documented, not started): TypeScript/JavaScript, Go and Java first, modelled on the Python SDK; see [roadmap.md](docs/roadmap.md).
4. **Per-person delivery**: resolve a person's address at send time and deliver personal digests, schedules and budgets.
5. **Console completeness**: screens for rule predicates, per-rule channels and personal preferences; translations for the newer screens.
6. **Scale**: a real queue instead of polling if needed; a shared circuit breaker; an OpenTelemetry exporter.
7. **Product**: event replay, an agent registry, multi-tenancy and billing for a hosted offering.

**Known issues**

- The Java service covers the core, `/notify`, the rules API, the delivery queue and Redis dedup (milestones 34 and 35), not accounts, AI, feedback, learning, traces or MCP; two services sharing one database must be configured alike because both dispatch queued jobs; its CI job passes on GitHub (run 42368107261 on commit b562715, read from the public API). See [milestone 34](docs/milestones/milestone-34.md).
- NATS is part of the intended stack but is not used; the worker and delivery poll PostgreSQL.
- Personal digests are stored and readable but not delivered.
- Parts that have run only against fakes: PostgreSQL stores (CI only), the Redis budget script (CI only), Ollama, Claude, Novu, SNS, OpenID Connect, the MCP server.
- Spans are structured log lines, not an OpenTelemetry export.
- The default attention budget (10 per hour) quiets busy workspaces; set `ATTENTION_BUDGET_PER_HOUR=0` to turn it off.

---

## Contributing

Contributions are welcome. The short version, with the full rules in [CONTRIBUTING.md](CONTRIBUTING.md):

1. Fork, and branch from `main` (`feature/<topic>` or `fix/<topic>`).
2. Work in **small, vertical milestones**: a change should leave a runnable system.
3. Keep Layer 1 deterministic. No AI call outside Layer 2, and add a test whenever you touch the critical path.
4. Add tests, run the gates under [Testing](#testing), and update `docs/CHANGELOG.md` and the relevant milestone or package doc **in the same commit**.
5. Open a pull request describing what changed, what you ran, and anything you could not verify.

Commit messages are short and imperative, and carry no `Co-Authored-By` trailer.

---

## License

**No license has been chosen yet**, so by default all rights are reserved and nobody else may legally use, copy or modify the code. The design brief calls this an open-core project that stays self-hostable; before accepting outside contributions, add a `LICENSE` file (MIT or Apache 2.0 are the usual choices for that model) and update the badge above.

---

## Acknowledgements and support

**Built on** the Go standard library, `lib/pq`, `go-redis`, `golang.org/x/crypto`, `yaml.v3`, React, React Router, Vite and Vitest. **Ideas from** Horvitz et al. on intelligent notification platforms, Apple's notification summaries, the Novu design guide on digests and multi-channel delivery, and MagicBell's notification UI patterns; AgentPulse follows the product notes in [docs/agentpulse.md](docs/agentpulse.md).

**Help**

- Questions and bugs: [open an issue](https://github.com/rtrtarun/agentic-notification/issues).
- Something unclear in the docs: [docs/README.md](docs/README.md) is the map, and [docs/runbook.md](docs/runbook.md) covers operating the service.
- Security problems: please open a private security advisory on the repository rather than a public issue.
