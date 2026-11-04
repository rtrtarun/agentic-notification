package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/inbox"
)

func newInboxServer(t *testing.T) *Server {
	t.Helper()
	repo := event.NewInMemoryRepository()
	seed := []event.Event{
		{ID: "crit", Source: "github", EventType: "security_advisory", Repository: "acme/billing",
			AttentionClass: event.AttentionCritical, DecisionState: &event.DecisionState{Status: event.DecisionSent, Reason: "security"}},
		{ID: "std", Source: "github", EventType: "pull_request", Repository: "acme/web",
			AttentionClass: event.AttentionStandard, DecisionState: &event.DecisionState{Status: event.DecisionQueued, Reason: "held"}},
		{ID: "bg", Source: "github", EventType: "star", Repository: "acme/web",
			AttentionClass: event.AttentionBackground, DecisionState: &event.DecisionState{Status: event.DecisionDigest, Reason: "social"}},
	}
	for _, e := range seed {
		if err := repo.Save(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	s := newAPIServer(config.Config{MaxBodyBytes: 4096}, &countingNotifier{})
	s.eventRepo = repo
	s.inbox = inbox.NewService(repo, inbox.NewMemoryRepository())
	return s
}

func inboxCall(s *Server, method, path, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, apiRequest(method, path, body))
	return rr
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return v
}

func TestInboxRequiresAPIKey(t *testing.T) {
	s := newInboxServer(t)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestInboxListFiltersAndCounts(t *testing.T) {
	s := newInboxServer(t)
	rr := inboxCall(s, http.MethodGet, "/api/notifications?priority=low,normal&sort=priority", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	page := decode[inbox.Page](t, rr)
	if page.Total != 2 || page.Items[0].ID != "std" || page.Items[1].ID != "bg" {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.Counts.All != 3 || page.Counts.Critical != 1 {
		t.Fatalf("counts = %+v", page.Counts)
	}
}

func TestInboxListRejectsBadQuery(t *testing.T) {
	s := newInboxServer(t)
	for _, q := range []string{"sort=sideways", "priority=urgent", "outcome=lost", "read=maybe", "from=yesterday", "page=0", "pageSize=1000"} {
		if rr := inboxCall(s, http.MethodGet, "/api/notifications?"+q, ""); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rr.Code)
		}
	}
}

func TestInboxDetailAndNotFound(t *testing.T) {
	s := newInboxServer(t)
	rr := inboxCall(s, http.MethodGet, "/api/notifications/std", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	d := decode[inbox.Detail](t, rr)
	if d.Item.ID != "std" || len(d.Trail) == 0 || d.Trail[0].Reconstructed != true {
		t.Fatalf("detail = %+v", d)
	}
	if rr := inboxCall(s, http.MethodGet, "/api/notifications/ghost", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestInboxPatchPersistsReadAndArchive(t *testing.T) {
	s := newInboxServer(t)
	rr := inboxCall(s, http.MethodPatch, "/api/notifications/std", `{"read":true,"archived":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	d := decode[inbox.Detail](t, rr)
	if !d.Item.Read || d.Item.ArchivedAt == nil {
		t.Fatalf("item = %+v", d.Item)
	}
	if rr := inboxCall(s, http.MethodPatch, "/api/notifications/std", `{}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("empty patch status = %d, want 400", rr.Code)
	}
}

func TestInboxCriticalCannotBeReprioritised(t *testing.T) {
	s := newInboxServer(t)
	rr := inboxCall(s, http.MethodPatch, "/api/notifications/crit", `{"priority":"low"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "critical") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestInboxActions(t *testing.T) {
	s := newInboxServer(t)
	for _, path := range []string{"acknowledge", "resolve"} {
		if rr := inboxCall(s, http.MethodPost, "/api/notifications/crit/"+path, ""); rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", path, rr.Code, rr.Body.String())
		}
	}
	rr := inboxCall(s, http.MethodPost, "/api/notifications/std/snooze", `{"minutes":60}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("snooze status = %d: %s", rr.Code, rr.Body.String())
	}
	if d := decode[inbox.Detail](t, rr); d.Item.SnoozedUntil == nil {
		t.Fatal("expected snoozedUntil")
	}
	for body, want := range map[string]int{`{}`: 400, `{"minutes":5}`: 200, `{"until":"soon"}`: 400, `{"minutes":999999}`: 400} {
		if rr := inboxCall(s, http.MethodPost, "/api/notifications/bg/snooze", body); rr.Code != want {
			t.Errorf("snooze %s: status = %d, want %d", body, rr.Code, want)
		}
	}
	if rr := inboxCall(s, http.MethodPost, "/api/notifications/std/teleport", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown action status = %d, want 404", rr.Code)
	}
}

func TestInboxBulkAndMarkAllRead(t *testing.T) {
	s := newInboxServer(t)
	rr := inboxCall(s, http.MethodPost, "/api/notifications/bulk-actions", `{"ids":["std","ghost","crit"],"action":"priority","priority":"high"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	res := decode[struct{ Results []inbox.BulkResult }](t, rr).Results
	if !res[0].OK || res[1].OK || res[2].OK {
		t.Fatalf("results = %+v (critical and unknown ids must fail, the rest succeed)", res)
	}
	if rr := inboxCall(s, http.MethodPost, "/api/notifications/bulk-actions", `{"ids":[],"action":"read"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("empty ids status = %d, want 400", rr.Code)
	}
	if rr := inboxCall(s, http.MethodPost, "/api/notifications/bulk-actions", `{"ids":["std"],"action":"explode"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown action status = %d, want 400", rr.Code)
	}

	rr = inboxCall(s, http.MethodPost, "/api/notifications/mark-all-read", "")
	if got := decode[map[string]int](t, rr)["changed"]; got != 3 {
		t.Fatalf("changed = %d, want 3", got)
	}
}

func TestInboxDeleteHidesButKeepsEvent(t *testing.T) {
	s := newInboxServer(t)
	if rr := inboxCall(s, http.MethodDelete, "/api/notifications/bg", ""); rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	if rr := inboxCall(s, http.MethodGet, "/api/notifications/bg", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("deleted status = %d, want 404", rr.Code)
	}
	ok, err := s.eventRepo.Exists(context.Background(), "bg")
	if err != nil || !ok {
		t.Fatal("the underlying event must be kept")
	}
}

func TestInboxMethodsAndUnavailable(t *testing.T) {
	s := newInboxServer(t)
	if rr := inboxCall(s, http.MethodPost, "/api/notifications", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if rr := inboxCall(s, http.MethodPut, "/api/notifications/std", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	s.inbox = nil
	if rr := inboxCall(s, http.MethodGet, "/api/notifications", ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestRouteLabelCollapsesNotificationIDs(t *testing.T) {
	for _, p := range []string{"/api/notifications", "/api/notifications/abc-123", "/api/notifications/abc/snooze"} {
		if got := routeLabel(p); got != inboxPrefix {
			t.Errorf("routeLabel(%q) = %q", p, got)
		}
	}
}
