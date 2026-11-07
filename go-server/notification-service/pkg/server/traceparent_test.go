package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/telemetry"
)

func TestEveryResponseCarriesATraceparentThatContinuesTheCallersTrace(t *testing.T) {
	s := newAuthServer(t, nil)
	const trace = "4bf92f3577b34da6a3ce929d0e0e4736"
	const span = "00f067aa0ba902b7"

	rr := call(s, http.MethodGet, "/health", "", func(r *http.Request) { r.Header.Set("traceparent", "00-"+trace+"-"+span+"-01") })
	sc, ok := telemetry.ParseTraceparent(rr.Header().Get("traceparent"))
	if !ok || !strings.Contains(rr.Header().Get("traceparent"), trace) || strings.Contains(rr.Header().Get("traceparent"), span) {
		t.Fatalf("the response must continue the caller's trace with a new span: %q", rr.Header().Get("traceparent"))
	}
	if !sc.Sampled {
		t.Fatal("the caller's sampling decision must be kept")
	}

	// Authenticated and failing requests are traced too.
	for _, c := range []struct {
		path string
		edit func(*http.Request)
	}{{"/api/rules", nil}, {"/api/rules", withKey}, {"/nope", nil}} {
		rr := call(s, http.MethodGet, c.path, "", c.edit)
		if _, ok := telemetry.ParseTraceparent(rr.Header().Get("traceparent")); !ok {
			t.Errorf("%s: no valid traceparent (%d)", c.path, rr.Code)
		}
	}

	rr = call(s, http.MethodGet, "/health", "", func(r *http.Request) { r.Header.Set("traceparent", "not-a-header") })
	if got := rr.Header().Get("traceparent"); strings.Contains(got, "not-a-header") || !strings.HasPrefix(got, "00-") {
		t.Fatalf("a broken header must start a fresh trace: %q", got)
	}
}
