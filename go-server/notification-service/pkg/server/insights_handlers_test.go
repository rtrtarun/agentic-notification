package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/notification-service/pkg/insights"
)

func newInsightsServer(t *testing.T) *Server {
	t.Helper()
	s := newInboxServer(t)
	s.insights = insights.NewService(s.inbox)
	return s
}

func TestAnalyticsReturnsTotalsForTheRange(t *testing.T) {
	s := newInsightsServer(t)
	rr := inboxCall(s, http.MethodGet, "/api/analytics?range=24h", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[insights.Analytics](t, rr)
	if got.Range != insights.Range24h || got.Totals.Events != 3 || got.Totals.Critical != 1 || got.Totals.Delivered != 1 {
		t.Fatalf("analytics = %+v", got.Totals)
	}
	if got.Attention.HeldBack != 2 || len(got.Series) != 24 {
		t.Fatalf("attention = %+v series=%d", got.Attention, len(got.Series))
	}
}

func TestAnalyticsDefaultsAndValidates(t *testing.T) {
	s := newInsightsServer(t)
	if rr := inboxCall(s, http.MethodGet, "/api/analytics", ""); rr.Code != http.StatusOK || decode[insights.Analytics](t, rr).Range != insights.Range7d {
		t.Fatalf("default range failed: %d %s", rr.Code, rr.Body.String())
	}
	if rr := inboxCall(s, http.MethodGet, "/api/analytics?range=1y", ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad range status = %d", rr.Code)
	}
	if rr := inboxCall(s, http.MethodPost, "/api/analytics", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", rr.Code)
	}
}

func TestActivityMergesArrivalsAndActions(t *testing.T) {
	s := newInsightsServer(t)
	if rr := inboxCall(s, http.MethodPost, "/api/notifications/std/acknowledge", ""); rr.Code != http.StatusOK && rr.Code != http.StatusNoContent {
		t.Fatalf("acknowledge status = %d: %s", rr.Code, rr.Body.String())
	}
	rr := inboxCall(s, http.MethodGet, "/api/activity?limit=10", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	entries := decode[struct {
		Entries []insights.Entry `json:"entries"`
	}](t, rr).Entries
	var received, actions int
	for _, e := range entries {
		switch e.Kind {
		case insights.KindReceived:
			received++
		case insights.KindAction:
			actions++
		}
	}
	if received != 3 || actions == 0 {
		t.Fatalf("received=%d actions=%d entries=%+v", received, actions, entries)
	}
}

func TestActivityValidatesLimit(t *testing.T) {
	s := newInsightsServer(t)
	for _, q := range []string{"limit=0x", "limit=-1", "limit=500"} {
		if rr := inboxCall(s, http.MethodGet, "/api/activity?"+q, ""); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", q, rr.Code)
		}
	}
}

func TestInsightsRequireAPIKeyAndService(t *testing.T) {
	s := newInsightsServer(t)
	for _, path := range []string{"/api/analytics", "/api/activity"} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s without key = %d", path, rr.Code)
		}
	}
	s.insights = nil
	if rr := inboxCall(s, http.MethodGet, "/api/analytics", ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing service status = %d", rr.Code)
	}
}

func TestRouteLabelCollapsesCollections(t *testing.T) {
	cases := map[string]string{
		"/api/rules/abc":       "/api/rules",
		"/api/analytics":       "/api/analytics",
		"/api/unknown/thing":   "other",
		"/api/notifications/x": "/api/notifications",
	}
	for path, want := range cases {
		if got := routeLabel(path); got != want {
			t.Errorf("routeLabel(%q) = %q, want %q", path, got, want)
		}
	}
}
