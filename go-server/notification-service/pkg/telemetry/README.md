# telemetry

W3C `traceparent` propagation, timed spans and an OTLP exporter, with no tracing SDK.

- A request keeps the trace it arrived with (and its sampled flag) or starts a new sampled one, and gets a server span named `<method> <route>`; `/health` is not traced. The pipeline records `pipeline.Handle`, `policy.Evaluate`, `dedup.Check`, `attention.Classify` and `delivery.Send` as its children. Every span is a debug log record with `trace_id`, `span_id`, `parent_span_id` and `duration_ms`. A nil `*Tracer` does nothing.
- A span carries only allow-listed attributes (`http.request.method`, `http.route`, `http.response.status_code`, `event.source`, `event.type`, `attention.class`, `decision.status`, `rule.name`, `agent.trace_id`), each at most 200 characters; anything else is ignored. A failed span's exported status message is always `failed`, never the error's text.
- `OTLPExporter` (milestone 39c) sends sampled spans as OTLP/HTTP JSON to `<endpoint>/v1/traces`: lower-case hex ids, nanosecond times as strings, `Content-Type: application/json`. `Export` never blocks: a bounded buffer drops and counts spans when full; a background loop sends batches every two seconds or when a batch fills; a request is retried once, only after a network error, 429 or 5xx; `Shutdown` flushes within a deadline and logs `sent`, `dropped` and `failed`. The Java service's `OtlpExporter` writes the same JSON.

See [docs/milestones/milestone-39.md](../../../../docs/milestones/milestone-39.md) and the archived [milestone 32](../../../../docs/archived/milestones/milestone-32.md).
