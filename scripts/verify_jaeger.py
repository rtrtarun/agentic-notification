"""Checks that both services' spans arrive in Jaeger and are read back correctly.

A collector can answer 200 to an OTLP export and still drop fields it cannot parse, so this
script asks Jaeger's query API what it actually stored. Start the stack with export on:

    OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318 docker compose --profile observability up -d
    python scripts/verify_jaeger.py --go http://localhost:8080 --java http://localhost:8081

It sends one agent step to each service under its own W3C trace, then one step to each service
under the same trace, and checks for every trace: the service names and version, the span tree
(server span, pipeline.Handle, then policy.Evaluate, dedup.Check, attention.Classify), parents,
durations, span kinds, typed attributes, the attribute allow-list, and that a secret placed in
the payload is nowhere in Jaeger. Standard library only. Exits 1 on the first failed check.
"""

import argparse
import json
import secrets
import sys
import time
import urllib.error
import urllib.request

# The nine attribute keys a span may carry (pkg/telemetry allowedAttributes, Telemetry.ALLOWED).
ALLOWED = {
    "http.request.method", "http.route", "http.response.status_code",
    "event.source", "event.type", "agent.trace_id",
    "attention.class", "decision.status", "rule.name",
}
# Tags Jaeger derives from the OTLP span itself (kind, status, scope), not from attributes.
JAEGER_DERIVED = {"span.kind", "otel.scope.name", "otel.scope.version", "otel.status_code",
                  "otel.status_description", "error", "internal.span.format"}
PIPELINE_CHILDREN = {"policy.Evaluate", "dedup.Check", "attention.Classify"}
SECRET = "sk-verify-" + secrets.token_hex(8)


class CheckFailed(Exception):
    """One check did not hold; the trace may still be arriving."""


def fail(message):
    raise CheckFailed(message)


def request(url, body=None, headers=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, headers=headers or {}, method="POST" if data else "GET")
    if data:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status, dict(resp.headers), json.loads(resp.read() or b"null")
    except urllib.error.HTTPError as err:
        return err.code, dict(err.headers), err.read().decode(errors="replace")


def publish(base, key, trace_id, caller_span, run_id):
    """Sends one agent step with a sampled traceparent and returns the response's traceparent."""
    event_id = "%s-%s" % (run_id, secrets.token_hex(3))
    status, headers, body = request(
        base + "/api/events",
        {"id": event_id, "source": "verify", "eventType": "tool_call", "traceId": run_id,
         "agentName": "verifier", "metadata": {"step_kind": "tool_call", "tool_name": "search"},
         "payload": {"apiKey": SECRET, "note": SECRET}},
        {"X-API-Key": key, "traceparent": "00-%s-%s-01" % (trace_id, caller_span)},
    )
    if status != 202:
        fail("%s/api/events answered %s %s" % (base, status, body))
    answer = headers.get("traceparent") or headers.get("Traceparent") or ""
    if answer.split("-")[1:2] != [trace_id]:
        fail("%s kept no trace id: sent %s, got %r" % (base, trace_id, answer))
    return answer


def checked_trace(jaeger, trace_id, expected, wait_s):
    """Polls Jaeger until every (service, caller span, run) in expected checks out, or the wait ends.

    Exporters send every two seconds and a trace's spans can arrive in more than one batch, so a
    failed check is retried until the deadline and only the last failure is reported.
    """
    deadline = time.time() + wait_s
    problem = "Jaeger has no trace %s" % trace_id
    while time.time() < deadline:
        status, _, body = request("%s/api/traces/%s" % (jaeger, trace_id))
        if status == 200 and isinstance(body, dict) and body.get("data"):
            trace = body["data"][0]
            try:
                total = sum(check_service_tree(trace, *e) for e in expected)
                if SECRET in json.dumps(trace):
                    fail("the payload secret reached Jaeger in trace %s" % trace_id)
                return trace, total
            except CheckFailed as err:
                problem = str(err)
        time.sleep(1)
    fail("trace %s: %s" % (trace_id, problem))


def tags(span):
    return {t["key"]: t for t in span.get("tags", [])}


def check_service_tree(trace, service, caller_span, run_id):
    """Checks one service's spans inside a Jaeger trace; returns the number of spans checked."""
    processes = {pid: p for pid, p in trace["processes"].items() if p["serviceName"] == service}
    if not processes:
        fail("no spans from %s; services seen: %s" % (service, sorted(p["serviceName"] for p in trace["processes"].values())))
    for p in processes.values():
        version = {t["key"]: t["value"] for t in p.get("tags", [])}.get("service.version")
        if not version:
            fail("%s has no service.version resource attribute" % service)
    spans = [s for s in trace["spans"] if s["processID"] in processes]
    by_id = {s["spanID"]: s for s in spans}

    def parent(span):
        refs = [r for r in span.get("references", []) if r["refType"] == "CHILD_OF"]
        if len(refs) != 1:
            fail("%s span %s has %d parents" % (service, span["operationName"], len(refs)))
        if refs[0]["traceID"] != trace["traceID"]:
            fail("%s span %s points at another trace" % (service, span["operationName"]))
        return refs[0]["spanID"]

    servers = [s for s in spans if s["operationName"] == "POST /api/events"]
    if len(servers) != 1:
        fail("%s: want one POST /api/events span, got %d" % (service, len(servers)))
    server = servers[0]
    if parent(server) != caller_span:
        fail("%s server span's parent is %s, want the caller's %s" % (service, parent(server), caller_span))
    st = tags(server)
    expect = {"span.kind": "server", "http.request.method": "POST", "http.route": "/api/events"}
    for k, v in expect.items():
        if st.get(k, {}).get("value") != v:
            fail("%s server span %s = %r, want %r" % (service, k, st.get(k, {}).get("value"), v))
    code = st.get("http.response.status_code")
    if not code or code["type"] != "int64" or code["value"] != 202:
        fail("%s status code tag should be int64 202, got %r" % (service, code))

    handles = [s for s in spans if s["operationName"] == "pipeline.Handle"]
    if len(handles) != 1 or parent(handles[0]) != server["spanID"]:
        fail("%s: pipeline.Handle must be the one child of the server span" % service)
    handle = handles[0]
    ht = {k: t["value"] for k, t in tags(handle).items()}
    if ht.get("span.kind") != "internal":
        fail("%s pipeline.Handle kind %r, want internal" % (service, ht.get("span.kind")))
    for k in ("event.source", "event.type", "attention.class", "decision.status"):
        if not ht.get(k):
            fail("%s pipeline.Handle lacks %s (tags: %s)" % (service, k, sorted(ht)))
    if ht.get("agent.trace_id") != run_id:
        fail("%s pipeline.Handle agent.trace_id %r, want %r" % (service, ht.get("agent.trace_id"), run_id))

    children = {s["operationName"] for s in spans if parent(s) == handle["spanID"]}
    want = PIPELINE_CHILDREN if ht["decision.status"] != "merged" else PIPELINE_CHILDREN - {"attention.Classify"}
    if not want <= children:
        fail("%s pipeline.Handle children %s, want at least %s" % (service, sorted(children), sorted(PIPELINE_CHILDREN)))

    for s in spans:
        if s["spanID"] != server["spanID"] and parent(s) not in by_id:
            fail("%s span %s has a parent outside the service's tree" % (service, s["operationName"]))
        if s["duration"] < 0 or s["startTime"] <= 0:
            fail("%s span %s has a bad time: start %s, duration %s" % (service, s["operationName"], s["startTime"], s["duration"]))
        if s["startTime"] < server["startTime"] or s["startTime"] + s["duration"] > server["startTime"] + server["duration"] + 1:
            fail("%s span %s lies outside its server span" % (service, s["operationName"]))
        extra = set(tags(s)) - ALLOWED - JAEGER_DERIVED
        if extra:
            fail("%s span %s carries keys outside the allow-list: %s" % (service, s["operationName"], sorted(extra)))
        if tags(s).get("error"):
            fail("%s span %s is marked failed" % (service, s["operationName"]))
    return len(spans)


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--go", default="http://localhost:8080")
    parser.add_argument("--java", default="http://localhost:8081")
    parser.add_argument("--jaeger", default="http://localhost:16686")
    parser.add_argument("--api-key", default="local-dev-key")
    parser.add_argument("--go-service", default="notification-service")
    parser.add_argument("--java-service", default="notification-service-java")
    parser.add_argument("--wait", type=float, default=20, help="seconds to wait for spans to arrive")
    args = parser.parse_args()
    services = [(args.go, args.go_service), (args.java, args.java_service)]

    # 1. One trace per service.
    for base, service in services:
        trace_id, caller = secrets.token_hex(16), secrets.token_hex(8)
        run_id = "verify-" + secrets.token_hex(4)
        publish(base, args.api_key, trace_id, caller, run_id)
        _, n = checked_trace(args.jaeger, trace_id, [(service, caller, run_id)], args.wait)
        print("ok  %-26s trace %s: %d spans, tree, kinds, typed attributes, allow-list" % (service, trace_id, n))

    # 2. One trace through both services (the same caller trace, two caller spans).
    trace_id, expected = secrets.token_hex(16), []
    for base, service in services:
        caller, run_id = secrets.token_hex(8), "verify-" + secrets.token_hex(4)
        publish(base, args.api_key, trace_id, caller, run_id)
        expected.append((service, caller, run_id))
    trace, total = checked_trace(args.jaeger, trace_id, expected, args.wait)
    names = sorted({p["serviceName"] for p in trace["processes"].values()})
    print("ok  shared trace %s: %d spans from %s" % (trace_id, total, ", ".join(names)))
    print("all Jaeger checks passed")


if __name__ == "__main__":
    try:
        main()
    except CheckFailed as err:
        print("FAIL:", err)
        sys.exit(1)
