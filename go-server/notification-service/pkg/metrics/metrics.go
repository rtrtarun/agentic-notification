// Package metrics exposes request counters and latency histograms in the
// Prometheus text format without pulling in a client library.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// latencyBuckets are upper bounds in seconds, chosen to resolve the P99 of a
// service whose requests mostly finish in milliseconds but may take seconds.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// Registry counts requests and records latencies per route and status class.
type Registry struct {
	notifications atomic.Uint64

	mu     sync.Mutex
	routes map[routeKey]*routeStats
}

type routeKey struct {
	route string
	class string // "2xx", "4xx", ...
}

type routeStats struct {
	count   uint64
	sumSecs float64
	buckets []uint64 // one per latencyBuckets entry, non-cumulative
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{routes: map[routeKey]*routeStats{}}
}

// ObserveRequest records one finished request. route must be a fixed route
// pattern, never the raw URL path, so label cardinality stays bounded.
func (r *Registry) ObserveRequest(route string, status int, d time.Duration) {
	if r == nil {
		return
	}
	key := routeKey{route: route, class: fmt.Sprintf("%dxx", status/100)}
	secs := d.Seconds()

	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.routes[key]
	if st == nil {
		st = &routeStats{buckets: make([]uint64, len(latencyBuckets))}
		r.routes[key] = st
	}
	st.count++
	st.sumSecs += secs
	for i, le := range latencyBuckets {
		if secs <= le {
			st.buckets[i]++
			break
		}
	}
}

// NotificationAccepted counts one accepted /notify request.
func (r *Registry) NotificationAccepted() {
	if r != nil {
		r.notifications.Add(1)
	}
}

// Requests returns the total number of observed requests.
func (r *Registry) Requests() uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var total uint64
	for _, st := range r.routes {
		total += st.count
	}
	return total
}

// Notifications returns the number of accepted notifications.
func (r *Registry) Notifications() uint64 {
	if r == nil {
		return 0
	}
	return r.notifications.Load()
}

// WritePrometheus renders every metric in the Prometheus text exposition format.
func (r *Registry) WritePrometheus(w io.Writer) {
	if r == nil {
		r = NewRegistry()
	}
	r.mu.Lock()
	keys := make([]routeKey, 0, len(r.routes))
	snapshot := make(map[routeKey]routeStats, len(r.routes))
	for k, st := range r.routes {
		keys = append(keys, k)
		snapshot[k] = routeStats{count: st.count, sumSecs: st.sumSecs, buckets: append([]uint64(nil), st.buckets...)}
	}
	r.mu.Unlock()
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		return keys[i].class < keys[j].class
	})

	fmt.Fprintln(w, "# HELP notifications_accepted_total Notifications accepted by /notify.")
	fmt.Fprintln(w, "# TYPE notifications_accepted_total counter")
	fmt.Fprintf(w, "notifications_accepted_total %d\n", r.notifications.Load())

	fmt.Fprintln(w, "# HELP http_requests_total HTTP requests by route and status class.")
	fmt.Fprintln(w, "# TYPE http_requests_total counter")
	for _, k := range keys {
		fmt.Fprintf(w, "http_requests_total{route=%q,code=%q} %d\n", k.route, k.class, snapshot[k].count)
	}

	fmt.Fprintln(w, "# HELP http_request_duration_seconds HTTP request latency.")
	fmt.Fprintln(w, "# TYPE http_request_duration_seconds histogram")
	for _, k := range keys {
		st := snapshot[k]
		var cumulative uint64
		for i, le := range latencyBuckets {
			cumulative += st.buckets[i]
			fmt.Fprintf(w, "http_request_duration_seconds_bucket{route=%q,code=%q,le=%q} %d\n",
				k.route, k.class, trimFloat(le), cumulative)
		}
		fmt.Fprintf(w, "http_request_duration_seconds_bucket{route=%q,code=%q,le=\"+Inf\"} %d\n", k.route, k.class, st.count)
		fmt.Fprintf(w, "http_request_duration_seconds_sum{route=%q,code=%q} %g\n", k.route, k.class, st.sumSecs)
		fmt.Fprintf(w, "http_request_duration_seconds_count{route=%q,code=%q} %d\n", k.route, k.class, st.count)
	}
}

func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", f), "0"), ".")
}
