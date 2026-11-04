package metrics

import (
	"strings"
	"testing"
	"time"
)

func render(r *Registry) string {
	var b strings.Builder
	r.WritePrometheus(&b)
	return b.String()
}

func TestObserveRequestBucketsAndCounts(t *testing.T) {
	r := NewRegistry()
	r.ObserveRequest("/notify", 202, 3*time.Millisecond)
	r.ObserveRequest("/notify", 202, 200*time.Millisecond)
	r.ObserveRequest("/notify", 500, 40*time.Second)

	out := render(r)
	for _, want := range []string{
		`http_requests_total{route="/notify",code="2xx"} 2`,
		`http_requests_total{route="/notify",code="5xx"} 1`,
		`http_request_duration_seconds_bucket{route="/notify",code="2xx",le="0.005"} 1`,
		`http_request_duration_seconds_bucket{route="/notify",code="2xx",le="0.25"} 2`,
		`http_request_duration_seconds_bucket{route="/notify",code="5xx",le="30"} 0`,
		`http_request_duration_seconds_bucket{route="/notify",code="5xx",le="+Inf"} 1`,
		`http_request_duration_seconds_count{route="/notify",code="2xx"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	if r.Requests() != 3 {
		t.Fatalf("Requests() = %d, want 3", r.Requests())
	}
}

func TestNotificationCounter(t *testing.T) {
	r := NewRegistry()
	r.NotificationAccepted()
	r.NotificationAccepted()
	if r.Notifications() != 2 {
		t.Fatalf("Notifications() = %d, want 2", r.Notifications())
	}
	if !strings.Contains(render(r), "notifications_accepted_total 2") {
		t.Fatal("counter missing from output")
	}
}

func TestEmptyRegistryRenders(t *testing.T) {
	out := render(NewRegistry())
	if !strings.Contains(out, "notifications_accepted_total 0") {
		t.Fatalf("unexpected output: %s", out)
	}
}
