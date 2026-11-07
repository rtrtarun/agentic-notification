"""Tests for agentpulse.py. Standard library only; run with ``python -m unittest``."""

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from agentpulse import STEP_KINDS, AgentPulse, AgentPulseError


class FakeService:
    """A local HTTP server that records requests and answers with a chosen status."""

    def __init__(self, status=202, reply=None, raw=None):
        self.status, self.reply, self.raw, self.requests = status, reply or {"status": "accepted"}, raw, []
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def _answer(self):
                length = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(length)) if length else None
                outer.requests.append({"method": self.command, "path": self.path, "key": self.headers.get("X-API-Key"), "body": body})
                raw = outer.raw if outer.raw is not None else json.dumps(outer.reply).encode()
                self.send_response(outer.status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            do_GET = do_POST = _answer

            def log_message(self, *args):  # keep test output quiet
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = "http://127.0.0.1:%d" % self.server.server_address[1]
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()

    def bodies(self):
        return [r["body"] for r in self.requests if r["method"] == "POST"]


class ClientTests(unittest.TestCase):
    def setUp(self):
        self.svc = FakeService()
        self.addCleanup(self.svc.close)
        self.pulse = AgentPulse(self.svc.url, "secret-key", agent_name="researcher", raise_errors=True)

    def test_it_refuses_a_bad_address_or_a_missing_key(self):
        for url in ("", "example.com", "ftp://example.com"):
            with self.assertRaises(ValueError):
                AgentPulse(url, "k")
        for key in ("", "   ", None):
            with self.assertRaises(ValueError):
                AgentPulse("https://example.com", key)

    def test_steps_are_chained_with_trace_and_parent_ids(self):
        run = self.pulse.run(trace_id="run-7")
        run.plan(tokens_in=500, tokens_out=80, cost_usd=0.004)
        run.retry(2, tool_name="web_search", duration_ms=30000)
        run.reflect()
        run.complete()
        first, second, third, last = self.svc.bodies()
        self.assertEqual(first["id"], "run-7-001")
        self.assertNotIn("parentEventId", first)
        self.assertEqual(second["parentEventId"], "run-7-001")
        self.assertEqual(third["parentEventId"], "run-7-002")
        self.assertEqual(last["parentEventId"], "run-7-003")
        for body in (first, second, third, last):
            self.assertEqual(body["traceId"], "run-7")
            self.assertEqual(body["agentName"], "researcher")
            self.assertEqual(body["source"], "agent")
        self.assertEqual(first["eventType"], "agent.plan")
        self.assertEqual(first["metadata"], {"step_kind": "plan", "tokens_in": 500, "tokens_out": 80, "cost_usd": 0.004})
        self.assertEqual(second["metadata"]["attempt"], 2)
        self.assertEqual(second["metadata"]["tool_name"], "web_search")
        self.assertEqual(last["metadata"]["step_kind"], "complete")

    def test_the_api_key_travels_in_a_header_and_never_in_the_body(self):
        self.pulse.run().plan()
        self.assertEqual(self.svc.requests[0]["key"], "secret-key")
        self.assertNotIn("secret-key", json.dumps(self.svc.requests[0]["body"]))

    def test_a_generated_trace_id_is_unique_per_run(self):
        self.assertNotEqual(self.pulse.run().trace_id, self.pulse.run().trace_id)

    def test_none_metadata_is_left_out_and_an_unknown_step_kind_is_refused(self):
        run = self.pulse.run(trace_id="t")
        run.retry(3)  # tool_name is None
        self.assertNotIn("tool_name", self.svc.bodies()[0]["metadata"])
        with self.assertRaises(ValueError):
            run.event("agent.dance", "dance")
        self.assertEqual(len(self.svc.bodies()), 1, "a refused step must not be sent")
        self.assertEqual(set(STEP_KINDS), {"plan", "tool_call", "retry", "reflect", "human_approval", "complete"})

    def test_severity_repository_and_payload_are_passed_through(self):
        run = self.pulse.run(trace_id="t", repository="acme/api")
        run.event("agent.note", severity="high", payload={"detail": "x"})
        body = self.svc.bodies()[0]
        self.assertEqual((body["severity"], body["repository"], body["payload"]), ("high", "acme/api", {"detail": "x"}))

    def test_needs_approval_marks_the_run_as_waiting(self):
        self.pulse.run(trace_id="t").needs_approval("migrate production")
        body = self.svc.bodies()[0]
        self.assertEqual(body["metadata"]["step_kind"], "human_approval")
        self.assertEqual(body["payload"], {"request": "migrate production"})


class RunLifecycleTests(unittest.TestCase):
    def setUp(self):
        self.svc = FakeService()
        self.addCleanup(self.svc.close)
        self.pulse = AgentPulse(self.svc.url, "k", raise_errors=True)

    def test_a_clean_block_completes_the_run_once(self):
        with self.pulse.run(trace_id="t") as run:
            run.plan()
        types = [b["eventType"] for b in self.svc.bodies()]
        self.assertEqual(types, ["agent.plan", "agent.complete"])

    def test_completing_by_hand_is_not_repeated_on_exit(self):
        with self.pulse.run(trace_id="t") as run:
            run.complete()
        self.assertEqual([b["eventType"] for b in self.svc.bodies()], ["agent.complete"])

    def test_an_exception_reports_a_failed_run_and_is_never_swallowed(self):
        with self.assertRaises(RuntimeError):
            with self.pulse.run(trace_id="t") as run:
                run.plan()
                raise RuntimeError("model unavailable")
        last = self.svc.bodies()[-1]
        self.assertEqual(last["eventType"], "agent.failed")
        self.assertIn("RuntimeError: model unavailable", last["metadata"]["error"])

    def test_a_tool_call_is_timed_and_published_on_success(self):
        with self.pulse.run(trace_id="t") as run:
            with run.tool("read_file", attempt=1) as call:
                pass
        body = self.svc.bodies()[0]
        self.assertEqual(body["eventType"], "tool.call")
        self.assertEqual(body["metadata"]["tool_name"], "read_file")
        self.assertEqual(body["metadata"]["step_kind"], "tool_call")
        self.assertGreaterEqual(body["metadata"]["duration_ms"], 0)
        self.assertEqual(call.answer, {"status": "accepted"})

    def test_a_failing_tool_call_is_reported_and_the_error_propagates(self):
        run = self.pulse.run(trace_id="t")
        with self.assertRaises(TimeoutError):
            with run.tool("web_search", attempt=2):
                raise TimeoutError("30 s elapsed")
        body = self.svc.bodies()[0]
        self.assertEqual(body["eventType"], "tool.call.failed")
        self.assertEqual(body["metadata"]["attempt"], 2)
        self.assertIn("TimeoutError", body["metadata"]["error"])

    def test_a_long_error_is_shortened(self):
        run = self.pulse.run(trace_id="t")
        with self.assertRaises(ValueError):
            with run.tool("x"):
                raise ValueError("e" * 2000)
        self.assertLessEqual(len(self.svc.bodies()[0]["metadata"]["error"]), 300)


class FailureTests(unittest.TestCase):
    def test_by_default_an_unreachable_service_never_breaks_the_agent(self):
        pulse = AgentPulse("http://127.0.0.1:1", "k", timeout=1)
        with self.assertLogs("agentpulse", level="WARNING") as logs:
            run = pulse.run(trace_id="t")
            self.assertIsNone(run.plan())
            self.assertIsNone(run.complete())
        self.assertTrue(any("could not be reached" in line for line in logs.output))
        self.assertFalse(any("k" == part for line in logs.output for part in line.split()), "the key must not be logged")

    def test_the_chain_continues_after_a_step_that_could_not_be_sent(self):
        svc = FakeService(status=500)
        self.addCleanup(svc.close)
        pulse = AgentPulse(svc.url, "k")
        run = pulse.run(trace_id="t")
        with self.assertLogs("agentpulse", level="WARNING"):
            run.plan()
        svc.status = 202
        run.reflect()
        self.assertEqual(svc.bodies()[-1]["parentEventId"], "t-001")

    def test_raise_errors_surfaces_the_status_and_the_services_own_message(self):
        svc = FakeService(status=400, reply={"error": "invalid agent event: eventType is required"})
        self.addCleanup(svc.close)
        with self.assertRaises(AgentPulseError) as caught:
            AgentPulse(svc.url, "k", raise_errors=True).publish({"id": "x"})
        self.assertEqual(caught.exception.status, 400)
        self.assertIn("eventType is required", str(caught.exception))

    def test_a_rejected_key_and_a_server_fault_give_safe_messages(self):
        for status, expected in ((401, "API key was rejected"), (500, "had a problem")):
            svc = FakeService(status=status, reply={"error": "internal detail secret-key"})
            self.addCleanup(svc.close)
            with self.assertRaises(AgentPulseError) as caught:
                AgentPulse(svc.url, "secret-key", raise_errors=True).publish({})
            self.assertIn(expected, str(caught.exception))
            self.assertNotIn("secret-key", str(caught.exception))
            self.assertEqual(caught.exception.status, status)

    def test_an_unreachable_service_has_no_status(self):
        with self.assertRaises(AgentPulseError) as caught:
            AgentPulse("http://127.0.0.1:1", "k", timeout=1, raise_errors=True).publish({})
        self.assertIsNone(caught.exception.status)

    def test_a_non_json_answer_is_an_error(self):
        svc = FakeService(status=200, raw=b"<html>not json</html>")
        self.addCleanup(svc.close)
        with self.assertRaises(AgentPulseError) as caught:
            AgentPulse(svc.url, "k", raise_errors=True).publish({})
        self.assertIn("not JSON", str(caught.exception))



class ReadHelperTests(unittest.TestCase):
    def test_traces_trace_and_explain_use_the_documented_endpoints(self):
        svc = FakeService(status=200, reply={"traces": [{"traceId": "run-1"}], "explanation": "because"})
        self.addCleanup(svc.close)
        pulse = AgentPulse(svc.url, "k", raise_errors=True)
        self.assertEqual(pulse.traces(limit=5), [{"traceId": "run-1"}])
        pulse.trace("run/1")
        pulse.explain("evt 1")
        paths = [r["path"] for r in svc.requests]
        self.assertEqual(paths, ["/api/traces?limit=5", "/api/traces/run%2F1", "/api/explain/evt%201"])
        self.assertTrue(all(r["method"] == "GET" for r in svc.requests))

    def test_read_helpers_return_none_when_errors_are_not_raised(self):
        pulse = AgentPulse("http://127.0.0.1:1", "k", timeout=1)
        with self.assertLogs("agentpulse", level="WARNING"):
            self.assertIsNone(pulse.traces())
            self.assertIsNone(pulse.trace("x"))
            self.assertIsNone(pulse.explain("x"))


if __name__ == "__main__":
    unittest.main()
