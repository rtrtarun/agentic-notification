package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type suggestionList struct {
	Suggestions []struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Status   string `json:"status"`
		RuleName string `json:"ruleName"`
		Evidence struct {
			Dismissed int `json:"dismissed"`
		} `json:"evidence"`
	} `json:"suggestions"`
	MinSignals int `json:"minSignals"`
}

// dismissedPullRequests makes n ordinary pull requests and dismisses each, so the
// default-github rule gathers negative feedback with no positive signal.
func dismissedPullRequests(t *testing.T, s *Server, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		body := fmt.Sprintf(`{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":%d},"sender":{"login":"alice"}}`, i)
		id := eventID(t, webhook(s, "pull_request", fmt.Sprintf("pr-%d", i), body))
		rr := call(s, http.MethodPost, "/feedback", `{"eventId":"`+id+`","action":"dismissed"}`, withKey)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("feedback = %d %s", rr.Code, rr.Body.String())
		}
	}
}

func readSuggestions(t *testing.T, s *Server, edit func(*http.Request)) suggestionList {
	t.Helper()
	rr := call(s, http.MethodGet, "/api/learning/suggestions", "", edit)
	if rr.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rr.Code, rr.Body.String())
	}
	var out suggestionList
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRepeatedDismissalsBecomeASuggestionThatAnAdminCanApply(t *testing.T) {
	s := newAuthServer(t, nil)
	dismissedPullRequests(t, s, 5)

	got := readSuggestions(t, s, withKey)
	if len(got.Suggestions) != 1 || got.MinSignals != 5 {
		t.Fatalf("suggestions = %+v", got)
	}
	sug := got.Suggestions[0]
	if sug.Kind != "demote" || sug.RuleName != "default-github" || sug.Evidence.Dismissed != 5 || sug.Status != "pending" {
		t.Fatalf("suggestion = %+v", sug)
	}

	m := addMember(t, s, "member@corp.com")
	if list := readSuggestions(t, s, m.apply(false)); len(list.Suggestions) != 1 {
		t.Fatalf("a member may read suggestions: %+v", list)
	}
	if rr := call(s, http.MethodPost, "/api/learning/suggestions/"+sug.ID+"/apply", "", m.apply(true)); rr.Code != http.StatusForbidden {
		t.Fatalf("a member applying = %d, want 403", rr.Code)
	}

	rr := call(s, http.MethodPost, "/api/learning/suggestions/"+sug.ID+"/apply", "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"applied"`) {
		t.Fatalf("apply = %d %s", rr.Code, rr.Body.String())
	}
	rules := call(s, http.MethodGet, "/api/rules", "", withKey).Body.String()
	if !strings.Contains(rules, `"name":"default-github"`) || !strings.Contains(afterName(rules, "default-github"), `"attentionClass":"background"`) {
		t.Fatalf("the rule should now be background: %s", rules)
	}
	if audit := call(s, http.MethodGet, "/api/audit", "", withKey).Body.String(); !strings.Contains(audit, "learning.apply") {
		t.Fatalf("applying must be audited: %s", audit)
	}
	if rr := call(s, http.MethodPost, "/api/learning/suggestions/"+sug.ID+"/apply", "", withKey); rr.Code != http.StatusConflict {
		t.Fatalf("applying twice = %d, want 409", rr.Code)
	}
	if list := readSuggestions(t, s, withKey); len(list.Suggestions) != 0 {
		t.Fatalf("an applied suggestion must not come back: %+v", list)
	}
}

// afterName returns the part of a rules response that follows a rule's name, up to the next rule.
func afterName(body, name string) string {
	_, rest, _ := strings.Cut(body, `"name":"`+name+`"`)
	if i := strings.Index(rest, `"name":"`); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func TestDismissingASuggestionChangesNothingAndItDoesNotReturn(t *testing.T) {
	s := newAuthServer(t, nil)
	dismissedPullRequests(t, s, 5)
	sug := readSuggestions(t, s, withKey).Suggestions[0]
	before := call(s, http.MethodGet, "/api/rules", "", withKey).Body.String()

	rr := call(s, http.MethodPost, "/api/learning/suggestions/"+sug.ID+"/dismiss", "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"dismissed"`) {
		t.Fatalf("dismiss = %d %s", rr.Code, rr.Body.String())
	}
	if after := call(s, http.MethodGet, "/api/rules", "", withKey).Body.String(); after != before {
		t.Fatal("dismissing must not change any rule")
	}
	if list := readSuggestions(t, s, withKey); len(list.Suggestions) != 0 {
		t.Fatalf("a dismissed suggestion must not return: %+v", list)
	}
	if audit := call(s, http.MethodGet, "/api/audit", "", withKey).Body.String(); !strings.Contains(audit, "learning.dismiss") {
		t.Fatalf("dismissing must be audited: %s", audit)
	}
}

func TestFeedbackAboutCriticalEventsNeverBecomesASuggestion(t *testing.T) {
	s := newAuthServer(t, nil)
	for i := 1; i <= 8; i++ {
		id := eventID(t, webhook(s, "security_advisory", fmt.Sprintf("sec-%d", i), strings.Replace(securityPayload, "GHSA", fmt.Sprintf("GHSA-%d", i), 1)))
		if rr := call(s, http.MethodPost, "/feedback", `{"eventId":"`+id+`","action":"dismissed"}`, withKey); rr.Code != http.StatusAccepted {
			t.Fatalf("feedback on a critical event must still be recorded, got %d %s", rr.Code, rr.Body.String())
		}
		call(s, http.MethodPost, "/feedback", `{"eventId":"`+id+`","action":"never"}`, withKey)
	}
	if got := readSuggestions(t, s, withKey); len(got.Suggestions) != 0 {
		t.Fatalf("critical events must never be learned from: %+v", got)
	}
	rules := call(s, http.MethodGet, "/api/rules", "", withKey).Body.String()
	if !strings.Contains(afterName(rules, "github-security-advisory"), `"attentionClass":"critical"`) {
		t.Fatalf("the critical rule must be untouched: %s", rules)
	}
}

func TestLearningEndpointsValidateAndRequireAccess(t *testing.T) {
	s := newAuthServer(t, nil)
	checks := []struct {
		method, path string
		edit         func(*http.Request)
		want         int
	}{
		{http.MethodGet, "/api/learning/suggestions", nil, http.StatusUnauthorized},
		{http.MethodPost, "/api/learning/suggestions", withKey, http.StatusMethodNotAllowed},
		{http.MethodDelete, "/api/learning/suggestions/x/apply", withKey, http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/learning/suggestions/x/apply", withKey, http.StatusNotFound},
		{http.MethodPost, "/api/learning/suggestions/x/dismiss", withKey, http.StatusNotFound},
		{http.MethodPost, "/api/learning/suggestions/x/explode", withKey, http.StatusNotFound},
		{http.MethodPost, "/api/learning/suggestions/x", withKey, http.StatusNotFound},
	}
	for _, c := range checks {
		if rr := call(s, c.method, c.path, "", c.edit); rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
}
