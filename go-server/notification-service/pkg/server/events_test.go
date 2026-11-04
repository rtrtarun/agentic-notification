package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/event"
)

type eventsResponse struct {
	Events []map[string]any `json:"events"`
}

func newEventsServer(t *testing.T, n int) (*Server, *event.InMemoryRepository) {
	t.Helper()
	repo := event.NewInMemoryRepository()
	for i := 1; i <= n; i++ {
		err := repo.Save(context.Background(), event.Event{
			ID: fmt.Sprintf("e%d", i), Source: "github", EventType: "star", Repository: "o/r",
			AttentionClass: event.AttentionBackground,
			DecisionState:  &event.DecisionState{Status: event.DecisionQueued, Reason: "r"},
			Payload:        map[string]any{"sender": map[string]any{"email": "private@example.com"}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	s := newAPIServer(config.Config{MaxBodyBytes: 1024}, &countingNotifier{})
	s.eventRepo = repo
	return s, repo
}

func getEvents(s *Server, query string, withKey bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/events"+query, nil)
	if withKey {
		req.Header.Set("X-API-Key", testAPIKey)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func TestEventsReturnsNewestFirstWithoutPayload(t *testing.T) {
	s, _ := newEventsServer(t, 3)
	rr := getEvents(s, "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body eventsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Events) != 3 || body.Events[0]["id"] != "e3" || body.Events[2]["id"] != "e1" {
		t.Fatalf("expected e3,e2,e1 newest first, got %v", body.Events)
	}
	if strings.Contains(rr.Body.String(), "private@example.com") || strings.Contains(rr.Body.String(), "payload") {
		t.Fatal("the list must not expose raw payloads")
	}
	if body.Events[0]["createdAt"] == nil || body.Events[0]["createdAt"] == "0001-01-01T00:00:00Z" {
		t.Fatalf("createdAt must be set: %v", body.Events[0]["createdAt"])
	}
}

func TestEventsLimit(t *testing.T) {
	s, _ := newEventsServer(t, 5)
	var body eventsResponse
	rr := getEvents(s, "?limit=2", true)
	json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Events) != 2 || body.Events[0]["id"] != "e5" {
		t.Fatalf("limit=2 should return the two newest, got %v", body.Events)
	}
}

func TestEventsRejectsBadLimit(t *testing.T) {
	s, _ := newEventsServer(t, 1)
	for _, q := range []string{"?limit=0", "?limit=-1", "?limit=abc", "?limit=501"} {
		if rr := getEvents(s, q, true); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rr.Code)
		}
	}
}

func TestEventsEmptyIsAnEmptyArray(t *testing.T) {
	s, _ := newEventsServer(t, 0)
	rr := getEvents(s, "", true)
	if !strings.Contains(rr.Body.String(), `"events":[]`) {
		t.Fatalf("an empty store must serialise as [], got %s", rr.Body.String())
	}
}

func TestEventsRequiresAPIKeyAndGet(t *testing.T) {
	s, _ := newEventsServer(t, 1)
	if rr := getEvents(s, "", false); rr.Code != http.StatusUnauthorized {
		t.Fatalf("no key: status = %d, want 401", rr.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/events", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: status = %d, want 405", rr.Code)
	}
}

type failingList struct{ event.Repository }

func (failingList) List(context.Context) ([]event.Event, error) {
	return nil, errors.New("db password=hunter2 refused")
}

func TestEventsListFailureIsGeneric(t *testing.T) {
	s, _ := newEventsServer(t, 1)
	s.eventRepo = failingList{}
	rr := getEvents(s, "", true)
	if rr.Code != http.StatusInternalServerError || strings.Contains(rr.Body.String(), "hunter2") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestEventsRouteIsAllowedMetricLabel(t *testing.T) {
	if routeLabel("/events") != "/events" {
		t.Fatal("/events must be a known metric label")
	}
}
