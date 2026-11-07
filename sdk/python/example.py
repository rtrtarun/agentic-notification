"""A small agent that reports a run to AgentPulse.

It plans, calls a flaky tool that times out three times, retries, reflects and finishes.
With the rule from docs/milestone-31.md (attempt above 3) the fourth attempt raises an alert.

    AGENTPULSE_URL=http://localhost:8080 AGENTPULSE_API_KEY=dev-key python example.py

Then open the Traces screen in the console, or read the run back:

    curl -H "X-API-Key: dev-key" http://localhost:8080/api/traces
"""

import os
import sys

from agentpulse import AgentPulse


def flaky_search(attempt: int) -> str:
    """Stands in for a real tool: fails until the fourth attempt."""
    if attempt < 4:
        raise TimeoutError("search timed out after 30 s")
    return "three relevant results"


def main() -> int:
    url = os.environ.get("AGENTPULSE_URL", "http://localhost:8080")
    key = os.environ.get("AGENTPULSE_API_KEY")
    if not key:
        print("Set AGENTPULSE_API_KEY (and AGENTPULSE_URL if the service is not on localhost:8080).", file=sys.stderr)
        return 2

    pulse = AgentPulse(url, key, agent_name="researcher")
    with pulse.run() as run:
        print("run", run.trace_id)
        run.plan(tokens_in=620, tokens_out=140, cost_usd=0.0041, duration_ms=1200)

        for attempt in range(1, 5):
            if attempt > 1:
                run.retry(attempt, tool_name="web_search")
            try:
                with run.tool("web_search", attempt=attempt, confidence=0.62):
                    print("  attempt", attempt, "->", flaky_search(attempt))
            except TimeoutError as err:
                print("  attempt", attempt, "failed:", err)

        run.reflect(tokens_in=900, tokens_out=310, cost_usd=0.0087)

    answer = pulse.trace(run.trace_id)
    if answer:
        print("status:", answer["status"], "| steps:", answer["steps"], "| retries:", answer["retries"], "| cost: $%.4f" % answer["costUsd"])
    return 0


if __name__ == "__main__":
    sys.exit(main())
