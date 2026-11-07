# telemetry

W3C `traceparent` propagation and timed spans, with no tracing SDK. A request keeps the trace it arrived with or starts a new one; the pipeline records `pipeline.Handle`, `policy.Evaluate`, `dedup.Check`, `attention.Classify` and `delivery.Send` as structured debug log records with `trace_id`, `span_id`, `parent_span_id` and `duration_ms`. A nil `*Tracer` does nothing. It does not export to a collector. See [docs/milestones/milestone-32.md](../../../../docs/archived/milestones/milestone-32.md).
