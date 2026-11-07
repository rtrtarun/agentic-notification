"""AgentPulse helper for Python agents.

It publishes the steps of an agent run to an AgentPulse service and chains them with
``trace_id`` and ``parent_event_id``, so the Traces screen can draw the run as a timeline
(plan, tool call, retry, reflect, complete) with durations, tokens and cost.

Only the standard library is used. Nothing needs installing: copy this file next to your
agent, or add ``sdk/python`` to ``PYTHONPATH``.

    from agentpulse import AgentPulse

    pulse = AgentPulse("https://agentpulse.example.com", api_key="...", agent_name="researcher")
    with pulse.run() as run:
        run.plan(tokens_in=500, tokens_out=80, cost_usd=0.004)
        with run.tool("web_search") as call:
            results = search(query)          # an exception here is reported as a failed call
        run.complete()

Reporting must never break the agent it observes. By default a failure to reach the
service is logged and the call returns ``None``; pass ``raise_errors=True`` to see them.

An agent's ``severity`` is only data for the platform's rules. It never makes an event
critical: rules decide that.
"""

from __future__ import annotations

import json
import logging
import time
import urllib.error
import urllib.request
import uuid
from typing import Any, Dict, Optional

__all__ = ["AgentPulse", "Run", "AgentPulseError", "STEP_KINDS"]
__version__ = "1.0.0"

log = logging.getLogger("agentpulse")

#: The step kinds the platform understands.
STEP_KINDS = ("plan", "tool_call", "retry", "reflect", "human_approval", "complete")

_DEFAULT_TIMEOUT = 10.0
_MAX_ERROR_TEXT = 300


class AgentPulseError(Exception):
    """The service refused a request or could not be reached.

    ``status`` is the HTTP status, or ``None`` when the service could not be reached.
    """

    def __init__(self, message: str, status: Optional[int] = None) -> None:
        super().__init__(message)
        self.status = status


class AgentPulse:
    """A connection to an AgentPulse service.

    :param base_url: The service address, for example ``https://agentpulse.example.com``.
    :param api_key: The API key. Sent as ``X-API-Key``; never logged.
    :param agent_name: Reported with every event.
    :param source: Where the events come from. Defaults to ``agent``.
    :param timeout: Seconds to wait for each request.
    :param raise_errors: Raise :class:`AgentPulseError` instead of logging and returning ``None``.
    """

    def __init__(
        self,
        base_url: str,
        api_key: str,
        agent_name: str = "",
        source: str = "agent",
        timeout: float = _DEFAULT_TIMEOUT,
        raise_errors: bool = False,
    ) -> None:
        base_url = (base_url or "").strip().rstrip("/")
        if not base_url.startswith(("http://", "https://")):
            raise ValueError("base_url must start with http:// or https://")
        if not api_key or not api_key.strip():
            raise ValueError("api_key is required")
        self.base_url = base_url
        self._api_key = api_key
        self.agent_name = agent_name
        self.source = source
        self.timeout = timeout
        self.raise_errors = raise_errors

    def run(self, trace_id: Optional[str] = None, repository: str = "") -> "Run":
        """Start a run. Use it as a context manager to finish it automatically."""
        return Run(self, trace_id or "run-" + uuid.uuid4().hex[:12], repository)

    def traces(self, limit: int = 30) -> Optional[list]:
        """Recent runs with their totals and status."""
        body = self._request("GET", "/api/traces?limit=%d" % int(limit))
        return None if body is None else body.get("traces", [])

    def trace(self, trace_id: str) -> Optional[dict]:
        """One run with its ordered steps and the decision made for each."""
        return self._request("GET", "/api/traces/" + urllib.request.quote(trace_id, safe=""))

    def explain(self, event_id: str) -> Optional[dict]:
        """Why an event was handled the way it was."""
        return self._request("GET", "/api/explain/" + urllib.request.quote(event_id, safe=""))

    def publish(self, event: Dict[str, Any]) -> Optional[dict]:
        """Send one event as it is. :meth:`Run.event` is usually what you want."""
        return self._request("POST", "/api/events", event)

    def _request(self, method: str, path: str, body: Optional[dict] = None) -> Optional[dict]:
        data = None if body is None else json.dumps(body).encode("utf-8")
        request = urllib.request.Request(self.base_url + path, data=data, method=method)
        request.add_header("X-API-Key", self._api_key)
        if data is not None:
            request.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                raw = response.read()
        except urllib.error.HTTPError as err:
            return self._fail(AgentPulseError(_describe(err), err.code))
        except (urllib.error.URLError, OSError, TimeoutError):
            return self._fail(AgentPulseError("AgentPulse could not be reached"))
        if not raw:
            return {}
        try:
            return json.loads(raw)
        except ValueError:
            return self._fail(AgentPulseError("AgentPulse answered with something that is not JSON"))

    def _fail(self, error: AgentPulseError) -> None:
        if self.raise_errors:
            raise error
        log.warning("agentpulse: %s", error)
        return None


def _describe(err: urllib.error.HTTPError) -> str:
    """The service's own message for a refusal; a bare status for anything else."""
    if err.code == 401:
        return "the API key was rejected"
    if err.code >= 500:
        return "AgentPulse had a problem (status %d)" % err.code
    try:
        text = json.loads(err.read()).get("error", "")
    except (ValueError, AttributeError):
        text = ""
    return (text or "request refused")[:_MAX_ERROR_TEXT] + " (status %d)" % err.code


class Run:
    """One run of an agent. Steps are chained: each names the previous one as its parent."""

    def __init__(self, client: AgentPulse, trace_id: str, repository: str = "") -> None:
        self.client = client
        self.trace_id = trace_id
        self.repository = repository
        self.last_event_id: Optional[str] = None
        self._sequence = 0
        self._finished = False

    # -- context manager -------------------------------------------------------------

    def __enter__(self) -> "Run":
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        """Finish the run: ``agent.complete`` normally, ``agent.failed`` if it raised.

        The exception is never swallowed.
        """
        if not self._finished:
            if exc_type is None:
                self.complete()
            else:
                self.event("agent.failed", "tool_call", error=_short(exc))
        return False

    # -- the generic step ------------------------------------------------------------

    def event(
        self,
        event_type: str,
        step_kind: Optional[str] = None,
        severity: str = "",
        payload: Optional[Dict[str, Any]] = None,
        **metadata: Any,
    ) -> Optional[dict]:
        """Publish one step, chained to the previous one.

        ``metadata`` keys are the telemetry the platform reads: ``tool_name``, ``attempt``,
        ``duration_ms``, ``tokens_in``, ``tokens_out``, ``cost_usd``, ``confidence`` (0 to 1).
        Returns the service's answer (with the attention class and decision), or ``None`` if
        it could not be published and errors are not being raised.
        """
        if step_kind is not None and step_kind not in STEP_KINDS:
            raise ValueError("step_kind must be one of: " + ", ".join(STEP_KINDS))
        if step_kind is not None:
            metadata["step_kind"] = step_kind
        self._sequence += 1
        event_id = "%s-%03d" % (self.trace_id, self._sequence)
        body: Dict[str, Any] = {
            "id": event_id,
            "source": self.client.source,
            "eventType": event_type,
            "traceId": self.trace_id,
            "agentName": self.client.agent_name,
            "metadata": {k: v for k, v in metadata.items() if v is not None},
        }
        if self.last_event_id:
            body["parentEventId"] = self.last_event_id
        if self.repository:
            body["repository"] = self.repository
        if severity:
            body["severity"] = severity
        if payload:
            body["payload"] = payload
        answer = self.client.publish(body)
        # The chain continues even if one step could not be sent, so later steps still link up.
        self.last_event_id = event_id
        return answer

    # -- named steps -----------------------------------------------------------------

    def plan(self, **metadata: Any) -> Optional[dict]:
        """The agent decided what to do."""
        return self.event("agent.plan", "plan", **metadata)

    def retry(self, attempt: int, tool_name: Optional[str] = None, **metadata: Any) -> Optional[dict]:
        """The agent is trying again. ``attempt`` is the number of this attempt."""
        return self.event("agent.retry", "retry", attempt=attempt, tool_name=tool_name, **metadata)

    def reflect(self, **metadata: Any) -> Optional[dict]:
        """The agent reconsidered."""
        return self.event("agent.reflect", "reflect", **metadata)

    def needs_approval(self, what: str, **metadata: Any) -> Optional[dict]:
        """The run is paused until a person approves. The Traces screen shows it as waiting."""
        return self.event("agent.approval.requested", "human_approval", payload={"request": what}, **metadata)

    def complete(self, **metadata: Any) -> Optional[dict]:
        """The run finished. Calling it again does nothing."""
        if self._finished:
            return None
        self._finished = True
        return self.event("agent.complete", "complete", **metadata)

    # -- timed tool calls ------------------------------------------------------------

    def tool(self, tool_name: str, attempt: int = 1, **metadata: Any) -> "_ToolCall":
        """Time a tool call. It is published when the block ends: ``tool.call`` on success,
        ``tool.call.failed`` (with the error) if the block raises. The exception is re-raised."""
        return _ToolCall(self, tool_name, attempt, metadata)


class _ToolCall:
    def __init__(self, run: Run, tool_name: str, attempt: int, metadata: Dict[str, Any]) -> None:
        self.run, self.tool_name, self.attempt, self.metadata = run, tool_name, attempt, metadata
        self._started = 0.0
        #: The service's answer once the call has been published.
        self.answer: Optional[dict] = None

    def __enter__(self) -> "_ToolCall":
        self._started = time.monotonic()
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        elapsed = int((time.monotonic() - self._started) * 1000)
        meta = dict(self.metadata, tool_name=self.tool_name, attempt=self.attempt, duration_ms=elapsed)
        if exc_type is None:
            self.answer = self.run.event("tool.call", "tool_call", **meta)
        else:
            self.answer = self.run.event("tool.call.failed", "tool_call", error=_short(exc), **meta)
        return False


def _short(exc: BaseException) -> str:
    text = "%s: %s" % (type(exc).__name__, exc)
    return text[:_MAX_ERROR_TEXT]
