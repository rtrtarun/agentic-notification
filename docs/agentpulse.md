# AgentPulse — Project Vision & MVP Scope

> Grafana tells you if your servers are healthy. AgentPulse tells you if your agents are making good decisions.

## The problem

Infrastructure monitoring tools — Grafana, Datadog, Prometheus, Sentry, PagerDuty — were built to answer questions about machines: Is the CPU high? Did the pod restart? Did the API return 500?

An AI agent's real failure modes live one layer above that. A request can return `200 OK` in 120ms while the agent inside it made a bad plan, called the wrong tool, hallucinated a number, or quietly retried three times before giving up. None of that shows up in a request log, because these tools have no concept of a plan, a tool call, a retry, or a reflection step. They were never designed to.

That's the actual gap — not "another notification service," but the missing observability layer for a category of software that didn't exist when Grafana was designed.

## Product identity

AgentPulse sits between an agent's execution and whoever depends on that agent working correctly:

```
AI Agent / Service execution
 (plan → tool call → retry → reflect)
              │
              ▼
        AgentPulse
 turns raw execution into
  signal that means something
              │
              ▼
   whatever's hooked on it
(a Slack channel, a dashboard, a human, another agent)
```

It's a translation layer, not a channel list. SDKs, auth, integrations — all plumbing around that one job.

*(Working name: **AgentPulse**. "Agentic Notify" and "NotiAgent" both still work — worth locking in once the rest feels right.)*

## Event model

Every event needs a `trace_id` (which run this belongs to) and a `parent_event_id` (what caused it), or the trace explorer below has nothing to reconstruct. That's the one real addition worth making to the schema from the original notes:

```json
{
  "event_id": "evt_01hxyz",
  "trace_id": "trace_01habc",
  "parent_event_id": "evt_01hxya",
  "agent": "research-agent",
  "event": "tool.call.failed",
  "severity": "warning",
  "timestamp": "2026-07-02T10:03:00Z",
  "metadata": {
    "tool": "web_search",
    "attempt": 2,
    "error": "timeout"
  }
}
```

Chain enough of these by `trace_id` and the "Agent Started → Searching → Tool Failed → Retry → Success" timeline falls out for free — it doesn't need to be built separately.

## What's actually in the MVP

Between them, the two source docs specced out roughly a full company. None of it is wrong, exactly, but building all of it means shipping nothing demoable. Here's the cut that produces something genuinely working:

**In:**
- REST ingestion endpoint + one SDK (Python)
- MCP server exposing three tools: `publish_event`, `get_recent_events`, `notify_user`
- Postgres as the only datastore — one event table indexed on `trace_id`; use `LISTEN/NOTIFY` to push live updates to the dashboard instead of standing up Redis
- **Agent Trace Explorer** — a timeline view that groups events by `trace_id` and renders the chain with timing and status. Nothing else on the market shows this, so it gets the most polish
- A minimal rules engine: threshold rules only (`retry_count > 3`, `severity == "error"`) evaluated on ingest
- One real channel, end to end: Slack incoming webhook
- A single API key per project — no OAuth, JWT, or RBAC yet

**Out — real roadmap, not this build:**
- Node / Go / Rust / Java SDKs
- Discord, Telegram, SMS, email, mobile push
- Agent registry / plugin marketplace
- RBAC, OAuth, topic secrets
- Redis Streams, RabbitMQ, Kubernetes, split microservices
- Native LangGraph / CrewAI / OpenAI Agents SDK adapters (a generic REST + MCP surface already covers all three)
- Cost/token dashboards, prompt version history, event replay, a full query language

Put the "out" list in the README under **Roadmap**. It signals that the scope was a choice, not a limitation — that reads better to a reviewer than either a half-built platform or no ambition at all.

## Minimal architecture

```
      AI Agent / script
   (Python SDK, REST, or MCP)
              │
              ▼
     ┌──────────────────────┐
     │      AgentPulse        │   FastAPI, one service
     │  ingest · rules ·      │
     │     MCP server          │
     └───────────┬────────────┘
                 │
        ┌────────┴─────────┐
        ▼                  ▼
    Postgres           Slack webhook
 (events + trace_id     (first real
  index, LISTEN/NOTIFY)   channel)
        │
        ▼
  Trace Explorer UI (React)
```

One deployable service. Split it into microservices later if it turns out you actually need to — not before.

## Milestones (rough, part-time pace)

| Milestone | Delivers | Rough time |
|---|---|---|
| M1 — Core pipe | Event schema, REST + Python SDK, events landing in Postgres | 1–2 wks |
| M2 — Trace Explorer | Timeline UI reconstructing a run from `trace_id` / `parent_event_id` | 2 wks |
| M3 — Rules + Slack | Threshold rule fires a real Slack message | 1 wk |
| M4 — MCP + polish | `publish_event` / `get_recent_events` / `notify_user` callable from Claude Desktop or Cursor; README + demo GIF | 1–2 wks |

Roughly 5–7 weeks to something real, versus 14–18 weeks for the full platform in the original roadmap. For a portfolio piece, the smaller thing that actually runs beats the bigger thing that's 60% finished.

## What "done" looks like

- A hosted demo (Railway / Render / Fly free tier is enough) replaying a sample agent run
- A README GIF showing a tool failure → retry → success chain appear live in the trace explorer
- That same run tripping a rule and landing a real Slack message
- A short "why this isn't Grafana" section, reusing the positioning above
- A screenshot or clip of the MCP tools working from an actual Claude Desktop or Cursor session

## Still worth deciding together

- Final name — AgentPulse vs. the other two
- Which framework to demo against first — a raw agent loop is simplest to fake convincingly; LangGraph/CrewAI look more "real" but add setup weight
- Where to host the live demo
Implemented (milestone 31): see [milestone-31.md](milestone-31.md) for `POST /api/events`, `GET /api/traces`, the MCP server and the Traces screen. The Python SDK helper described here is milestone 33.
