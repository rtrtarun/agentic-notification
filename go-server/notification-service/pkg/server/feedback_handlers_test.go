package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/feedback"
)

const securityPayload = `{"action":"published","repository":{"full_name":"acme/web"}}`

func eventID(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		EventID string `json:"eventId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.EventID == "" {
		t.Fatalf("webhook = %d %s", rr.Code, rr.Body.String())
	}
	return body.EventID
}

func sendFeedback(s *Server, edit func(*http.Request), body string) *httptest.ResponseRecorder {
	return call(s, http.MethodPost, "/feedback", body, edit)
}

func TestNeverNotifyFeedbackChangesTheNextEventOfThatKind(t *testing.T) {
	s := newAuthServer(t, nil)
	first := eventID(t, webhook(s, "pull_request", "p1", prPayload))

	rr := sendFeedback(s, withKey, `{"eventId":"`+first+`","action":"never"}`)
	if rr.Code != http.StatusAccepted || !strings.Contains(rr.Body.String(), `"override":{`) {
		t.Fatalf("feedback = %d %s", rr.Code, rr.Body.String())
	}

	second := webhook(s, "pull_request", "p2", `{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":8}}`)
	if !strings.Contains(second.Body.String(), `"status":"digest"`) || !strings.Contains(second.Body.String(), "never notify override") {
		t.Fatalf("the next pull request must go to the digest and say why: %s", second.Body.String())
	}
	other := webhook(s, "issues", "i1", `{"action":"opened","repository":{"full_name":"acme/web"},"issue":{"number":3}}`)
	if strings.Contains(other.Body.String(), "never notify override") {
		t.Fatalf("another kind of event must be unaffected: %s", other.Body.String())
	}
}

func TestACriticalEventKeepsArrivingWhateverPeopleSay(t *testing.T) {
	s := newAuthServer(t, nil)
	std := eventID(t, webhook(s, "pull_request", "p1", prPayload))
	for _, scope := range []string{"type", "repository", "source"} {
		sendFeedback(s, withKey, `{"eventId":"`+std+`","action":"never","scope":"`+scope+`"}`)
	}
	crit := webhook(s, "security_advisory", "s1", securityPayload)
	if !strings.Contains(crit.Body.String(), `"attentionClass":"critical"`) || !strings.Contains(crit.Body.String(), `"status":"sent"`) {
		t.Fatalf("a critical event must still be sent: %s", crit.Body.String())
	}
	critID := eventID(t, crit)
	if rr := sendFeedback(s, withKey, `{"eventId":"`+critID+`","action":"never"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("never on a critical event = %d, want 400", rr.Code)
	}
}

func TestUndoRemovesTheOverride(t *testing.T) {
	s := newAuthServer(t, nil)
	id := eventID(t, webhook(s, "pull_request", "p1", prPayload))
	sendFeedback(s, withKey, `{"eventId":"`+id+`","action":"never"}`)
	if n := len(s.overrides.List()); n != 1 {
		t.Fatalf("overrides = %d", n)
	}
	rr := sendFeedback(s, withKey, `{"eventId":"`+id+`","action":"undo"}`)
	if rr.Code != http.StatusAccepted || !strings.Contains(rr.Body.String(), `"reverted":true`) || len(s.overrides.List()) != 0 {
		t.Fatalf("undo = %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(webhook(s, "pull_request", "p2", `{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":9}}`).Body.String(), "never notify override") {
		t.Fatal("after undo the events must flow again")
	}
}

func TestFeedbackValidationAndAuth(t *testing.T) {
	s := newAuthServer(t, nil)
	id := eventID(t, webhook(s, "pull_request", "p1", prPayload))
	for name, c := range map[string]struct {
		body string
		want int
	}{
		"no action":     {`{"eventId":"` + id + `"}`, http.StatusBadRequest},
		"bad action":    {`{"eventId":"` + id + `","action":"like"}`, http.StatusBadRequest},
		"bad scope":     {`{"eventId":"` + id + `","action":"never","scope":"all"}`, http.StatusBadRequest},
		"empty reply":   {`{"eventId":"` + id + `","action":"reply","reason":""}`, http.StatusBadRequest},
		"unknown event": {`{"eventId":"nope","action":"opened"}`, http.StatusNotFound},
		"ok":            {`{"eventId":"` + id + `","action":"dismissed"}`, http.StatusAccepted},
		"reply":         {`{"eventId":"` + id + `","action":"reply","reason":"too noisy"}`, http.StatusAccepted},
	} {
		if rr := sendFeedback(s, withKey, c.body); rr.Code != c.want {
			t.Errorf("%s = %d, want %d: %s", name, rr.Code, c.want, rr.Body.String())
		}
	}
	if rr := sendFeedback(s, nil, `{"eventId":"`+id+`","action":"opened"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous feedback = %d", rr.Code)
	}
	if rr := call(s, http.MethodGet, "/feedback", "", withKey); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /feedback = %d", rr.Code)
	}
}

func TestFeedbackIsAttributedToThePerson(t *testing.T) {
	s := newAuthServer(t, nil)
	id := eventID(t, webhook(s, "pull_request", "p1", prPayload))
	m := addMember(t, s, "member@corp.com")
	if rr := sendFeedback(s, m.apply(true), `{"eventId":"`+id+`","action":"never"}`); rr.Code != http.StatusAccepted {
		t.Fatalf("member feedback = %d %s", rr.Code, rr.Body.String())
	}
	list := s.overrides.List()
	if len(list) != 1 || list[0].CreatedBy != "member@corp.com" {
		t.Fatalf("overrides = %+v", list)
	}
	audit := call(s, http.MethodGet, "/api/audit", "", withKey).Body.String()
	if !strings.Contains(audit, "override.never") || !strings.Contains(audit, "member@corp.com") {
		t.Fatalf("the change must be audited: %s", audit)
	}
}

func TestMembersCanBeLimitedToSignalsOnly(t *testing.T) {
	s := newAuthServer(t, func(c *config.Config) { c.OverridesAdminOnly = true })
	id := eventID(t, webhook(s, "pull_request", "p1", prPayload))
	m := addMember(t, s, "member@corp.com")

	rr := sendFeedback(s, m.apply(true), `{"eventId":"`+id+`","action":"never"}`)
	if rr.Code != http.StatusAccepted || !strings.Contains(rr.Body.String(), `"overrideSkipped":true`) || len(s.overrides.List()) != 0 {
		t.Fatalf("a member's never must be recorded but must not change anything: %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(s, http.MethodPost, "/api/overrides", `{"mode":"never","source":"github"}`, m.apply(true)); rr.Code != http.StatusForbidden {
		t.Fatalf("member POST override = %d", rr.Code)
	}
	admin := signIn(t, s, adminEmail, adminPass)
	if rr := sendFeedback(s, admin.apply(true), `{"eventId":"`+id+`","action":"never"}`); !strings.Contains(rr.Body.String(), `"override":{`) {
		t.Fatalf("an admin's never must create the override: %s", rr.Body.String())
	}
}

func TestOverridesEndpoints(t *testing.T) {
	s := newAuthServer(t, nil)
	if rr := call(s, http.MethodGet, "/api/overrides", "", withKey); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"overrides":[]`) {
		t.Fatalf("empty list = %d %s", rr.Code, rr.Body.String())
	}
	rr := call(s, http.MethodPost, "/api/overrides", `{"mode":"never","repository":"acme/*","note":"noisy"}`, withKey)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rr.Code, rr.Body.String())
	}
	var created struct{ ID, CreatedBy string }
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.CreatedBy != "api-key" {
		t.Fatalf("createdBy = %q (the caller must not choose it)", created.CreatedBy)
	}
	for name, c := range map[string]struct{ body string }{
		"no scope": {`{"mode":"never"}`}, "bad mode": {`{"mode":"x","source":"a"}`}, "bad pattern": {`{"mode":"never","repository":"a/["}`},
	} {
		if rr := call(s, http.MethodPost, "/api/overrides", c.body, withKey); rr.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, rr.Code)
		}
	}
	if rr := call(s, http.MethodDelete, "/api/overrides/nope", "", withKey); rr.Code != http.StatusNotFound {
		t.Fatalf("delete unknown = %d", rr.Code)
	}
	if rr := call(s, http.MethodDelete, "/api/overrides/"+created.ID, "", withKey); rr.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rr.Code)
	}
	if rr := call(s, http.MethodPut, "/api/overrides", "", withKey); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT = %d", rr.Code)
	}
	if rr := call(s, http.MethodGet, "/api/overrides", "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rr.Code)
	}
}

func TestOpeningANotificationIsRecordedOncePerPerson(t *testing.T) {
	s := newAuthServer(t, nil)
	id := eventID(t, webhook(s, "pull_request", "p1", prPayload))
	m := addMember(t, s, "member@corp.com")
	for i := 0; i < 3; i++ {
		if rr := call(s, http.MethodPatch, "/api/notifications/"+id, `{"read":true}`, m.apply(true)); rr.Code != http.StatusOK {
			t.Fatalf("patch = %d %s", rr.Code, rr.Body.String())
		}
	}
	opened := 0
	for _, f := range mustFeedback(t, s, id) {
		if f.Action == "opened" && f.UserID == "member@corp.com" {
			opened++
		}
	}
	if opened != 1 {
		t.Fatalf("opened recorded %d times, want once", opened)
	}
}

// mustFeedback reads the stored feedback for an event straight from the service's store.
func mustFeedback(t *testing.T, s *Server, id string) []feedbackRow {
	t.Helper()
	rows, err := s.feedbackStore.List(context.Background(), feedback.Filter{EventID: id})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]feedbackRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, feedbackRow{Action: r.Action, UserID: r.UserID})
	}
	return out
}

type feedbackRow struct{ Action, UserID string }
