# metrics

Request counters and latency histograms rendered in the Prometheus text format (no client library).

- `ObserveRequest(route, status, duration)` — route must be a fixed pattern, never the raw path.
- `WritePrometheus(w)` — served by `GET /metrics`.

Compute P99 in Prometheus: `histogram_quantile(0.99, sum by (le, route) (rate(http_request_duration_seconds_bucket[5m])))`.
