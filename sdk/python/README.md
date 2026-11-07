# AgentPulse Python helper

Reports the steps of an AI agent's run to AgentPulse, chained with `trace_id` and `parent_event_id`, so the Traces screen can draw the run as a timeline with durations, tokens and cost. Standard library only: copy `agentpulse.py` next to your agent, or add this folder to `PYTHONPATH`. Written for Python 3.8 or later; tested on 3.13 locally and 3.12 in CI.

```python
from agentpulse import AgentPulse

pulse = AgentPulse("https://agentpulse.example.com", api_key="...", agent_name="researcher")
with pulse.run() as run:
    run.plan(tokens_in=500, tokens_out=80, cost_usd=0.004)
    with run.tool("web_search") as call:      # timed; a raised error is reported as a failed call
        results = search(query)
    run.reflect()
# leaving the block completes the run, or reports agent.failed if it raised
```

| Call | Publishes |
|---|---|
| `run.plan(...)`, `run.reflect(...)` | A plan or reflect step |
| `with run.tool(name, attempt=1)` | `tool.call`, or `tool.call.failed` with the error and the duration |
| `run.retry(attempt, tool_name=...)` | A retry step |
| `run.needs_approval(what)` | A human-approval step; the Traces screen shows the run as waiting |
| `run.complete()` | The final step (once) |
| `run.event(type, step_kind, severity=, payload=, **telemetry)` | Any step |
| `pulse.traces()`, `pulse.trace(id)`, `pulse.explain(event_id)` | Read a run back, or ask why an event was handled as it was |

Telemetry keys the platform reads: `tool_name`, `attempt`, `duration_ms`, `tokens_in`, `tokens_out`, `cost_usd`, `confidence` (0 to 1).

**Reporting never breaks your agent.** By default a failure to reach the service is logged (logger `agentpulse`) and the call returns `None`; pass `raise_errors=True` to get an `AgentPulseError` with the HTTP status. The chain continues after a step that could not be sent. The API key is sent only as the `X-API-Key` header and is never logged or included in an error.

**Severity is data, not a verdict.** `severity="critical"` does not make an event critical; only the platform's rules do.

Try it against a local service:

```bash
AGENTPULSE_URL=http://localhost:8080 AGENTPULSE_API_KEY=dev-key python example.py
python -m unittest        # the tests; no service needed
```

See [docs/milestone-31.md](../../docs/milestone-31.md) for the API this uses and [docs/milestone-33.md](../../docs/milestone-33.md) for what was verified.
