package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type adjustmentList struct {
	Adjustments []struct {
		ID        string `json:"id"`
		Source    string `json:"source"`
		EventType string `json:"eventType"`
		Delta     int    `json:"delta"`
		Status    string `json:"status"`
		Version   int    `json:"version"`
		Evidence  struct {
			Negative int    `json:"negative"`
			People   int    `json:"people"`
			Summary  string `json:"summary"`
		} `json:"evidence"`
		Impact struct {
			Replayed int `json:"replayed"`
		} `json:"impact"`
	} `json:"adjustments"`
	Version       int `json:"version"`
	MaxAdjustment int `json:"maxAdjustment"`
	MinSignals    int `json:"minSignals"`
	MinPeople     int `json:"minPeople"`
}

// dismissedByTwoPeople makes n pull requests and has the API key and a member take turns dismissing them.
func dismissedByTwoPeople(t *testing.T, s *Server, n int, member session) {
	t.Helper()
	for i := 1; i <= n; i++ {
		body := fmt.Sprintf(`{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":%d},"sender":{"login":"alice"}}`, i)
		id := eventID(t, webhook(s, "pull_request", fmt.Sprintf("tune-%d", i), body))
		edit := withKey
		if i%2 == 0 {
			edit = member.apply(true)
		}
		if rr := call(s, http.MethodPost, "/feedback", `{"eventId":"`+id+`","action":"dismissed"}`, edit); rr.Code != http.StatusAccepted {
			t.Fatalf("feedback = %d %s", rr.Code, rr.Body.String())
		}
	}
}

func readAdjustments(t *testing.T, s *Server, query string, edit func(*http.Request)) adjustmentList {
	t.Helper()
	rr := call(s, http.MethodGet, "/api/learning/adjustments"+query, "", edit)
	if rr.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rr.Code, rr.Body.String())
	}
	var out adjustmentList
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFeedbackFromTwoPeopleBecomesAProposalThatChangesNothingUntilAnAdminApprovesIt(t *testing.T) {
	s := newAuthServer(t, nil)
	m := addMember(t, s, "member@corp.com")
	dismissedByTwoPeople(t, s, 5, m)

	if got := readAdjustments(t, s, "", withKey); len(got.Adjustments) != 0 || got.MaxAdjustment != 20 || got.MinSignals != 5 || got.MinPeople != 2 || got.Version != 0 {
		t.Fatalf("nothing is proposed before a run: %+v", got)
	}
	if rr := call(s, http.MethodPost, "/api/learning/runs?wait=true", "", m.apply(true)); rr.Code != http.StatusForbidden {
		t.Fatalf("a member starting a run = %d, want 403", rr.Code)
	}
	rr := call(s, http.MethodPost, "/api/learning/runs?wait=true", "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"done"`) || !strings.Contains(rr.Body.String(), `"proposalsMade":1`) || !strings.Contains(rr.Body.String(), `"feedbackRead":5`) {
		t.Fatalf("run = %d %s", rr.Code, rr.Body.String())
	}

	got := readAdjustments(t, s, "?status=proposed", m.apply(false))
	if len(got.Adjustments) != 1 {
		t.Fatalf("a member may read proposals: %+v", got)
	}
	adj := got.Adjustments[0]
	if adj.Source != "github" || adj.EventType != "pull_request" || adj.Delta != -14 || adj.Status != "proposed" || adj.Evidence.People != 2 || adj.Evidence.Negative != 5 ||
		adj.Evidence.Summary != "5 of 5 signals from 2 people were negative" || adj.Impact.Replayed != 5 {
		t.Fatalf("adjustment = %+v", adj)
	}

	// Still nothing active: the next pull request is scored as before.
	before := eventDecision(t, webhook(s, "pull_request", "after-run", `{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":90}}`))
	if strings.Contains(before, "learned adjustment") {
		t.Fatalf("a proposal must not change a decision: %s", before)
	}

	if rr := call(s, http.MethodPost, "/api/learning/adjustments/"+adj.ID+"/approve", "", m.apply(true)); rr.Code != http.StatusForbidden {
		t.Fatalf("a member approving = %d, want 403", rr.Code)
	}
	rr = call(s, http.MethodPost, "/api/learning/adjustments/"+adj.ID+"/approve", "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"active"`) || !strings.Contains(rr.Body.String(), `"version":1`) {
		t.Fatalf("approve = %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(s, http.MethodPost, "/api/learning/adjustments/"+adj.ID+"/approve", "", withKey); rr.Code != http.StatusConflict {
		t.Fatalf("approving twice = %d, want 409", rr.Code)
	}
	if got := readAdjustments(t, s, "?status=active", withKey); len(got.Adjustments) != 1 || got.Version != 1 {
		t.Fatalf("active = %+v", got)
	}

	after := eventDecision(t, webhook(s, "pull_request", "after-approve", `{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":91}}`))
	if !strings.Contains(after, "learned adjustment -14: 5 of 5 signals from 2 people were negative") || !strings.Contains(after, "heuristic-v1+tuned-1") {
		t.Fatalf("an approved adjustment changes the score and says so: %s", after)
	}

	rr = call(s, http.MethodPost, "/api/learning/adjustments/"+adj.ID+"/retire", "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"retired"`) || !strings.Contains(rr.Body.String(), `"version":2`) {
		t.Fatalf("retire = %d %s", rr.Code, rr.Body.String())
	}
	restored := eventDecision(t, webhook(s, "pull_request", "after-retire", `{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":92}}`))
	if strings.Contains(restored, "learned adjustment") || strings.Contains(restored, "tuned") {
		t.Fatalf("retiring restores the old score: %s", restored)
	}

	audit := call(s, http.MethodGet, "/api/audit", "", withKey).Body.String()
	for _, action := range []string{"learning.run", "learning.approve", "learning.retire"} {
		if !strings.Contains(audit, action) {
			t.Fatalf("%s must be audited: %s", action, audit)
		}
	}
}

// eventDecision returns the decision (reason and model) recorded for the event a webhook just made.
func eventDecision(t *testing.T, rr interface {
	Result() *http.Response
}) string {
	t.Helper()
	res := rr.Result()
	defer res.Body.Close()
	var body struct {
		DecisionState struct {
			Reason       string `json:"reason"`
			ModelVersion string `json:"modelVersion"`
		} `json:"decisionState"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.DecisionState.Reason + " | " + body.DecisionState.ModelVersion
}

func TestRejectingAProposalChangesNothingAndItIsNotProposedAgain(t *testing.T) {
	s := newAuthServer(t, nil)
	m := addMember(t, s, "member@corp.com")
	dismissedByTwoPeople(t, s, 5, m)
	call(s, http.MethodPost, "/api/learning/runs?wait=true", "", withKey)
	adj := readAdjustments(t, s, "", withKey).Adjustments[0]

	rr := call(s, http.MethodPost, "/api/learning/adjustments/"+adj.ID+"/reject", "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"rejected"`) {
		t.Fatalf("reject = %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(s, http.MethodPost, "/api/learning/adjustments/"+adj.ID+"/retire", "", withKey); rr.Code != http.StatusConflict {
		t.Fatalf("retiring a rejected one = %d, want 409", rr.Code)
	}
	if rr := call(s, http.MethodPost, "/api/learning/runs?wait=true", "", withKey); !strings.Contains(rr.Body.String(), `"proposalsMade":0`) {
		t.Fatalf("a rejected adjustment is not proposed again: %s", rr.Body.String())
	}
	if got := readAdjustments(t, s, "?status=rejected", withKey); len(got.Adjustments) != 1 {
		t.Fatalf("rejected = %+v", got)
	}
	if audit := call(s, http.MethodGet, "/api/audit", "", withKey).Body.String(); !strings.Contains(audit, "learning.reject") {
		t.Fatalf("rejecting must be audited: %s", audit)
	}
}

func TestARunStartedWithoutWaitingReturnsAtOnceAndFinishesInTheBackground(t *testing.T) {
	s := newAuthServer(t, nil)
	rr := call(s, http.MethodPost, "/api/learning/runs", "", withKey)
	if rr.Code != http.StatusAccepted || !strings.Contains(rr.Body.String(), `"status":"running"`) {
		t.Fatalf("start = %d %s", rr.Code, rr.Body.String())
	}
	s.tuner.Wait()
	rr = call(s, http.MethodGet, "/api/learning/runs", "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"done"`) || !strings.Contains(rr.Body.String(), `"every":"0s"`) {
		t.Fatalf("runs = %d %s", rr.Code, rr.Body.String())
	}
}

func TestTheLearningScoreEndpointsRefuseWhatTheyShould(t *testing.T) {
	s := newAuthServer(t, nil)
	cases := []struct {
		name, method, path string
		edit               func(*http.Request)
		want               int
	}{
		{"adjustments need a login", http.MethodGet, "/api/learning/adjustments", nil, http.StatusUnauthorized},
		{"runs need a login", http.MethodGet, "/api/learning/runs", nil, http.StatusUnauthorized},
		{"an unknown status is refused", http.MethodGet, "/api/learning/adjustments?status=maybe", withKey, http.StatusBadRequest},
		{"adjustments cannot be written to", http.MethodPut, "/api/learning/adjustments", withKey, http.StatusMethodNotAllowed},
		{"runs cannot be deleted", http.MethodDelete, "/api/learning/runs", withKey, http.StatusMethodNotAllowed},
		{"an unknown action is not found", http.MethodPost, "/api/learning/adjustments/abc/explode", withKey, http.StatusNotFound},
		{"an id with no action is not found", http.MethodPost, "/api/learning/adjustments/abc", withKey, http.StatusNotFound},
		{"an unknown adjustment is not found", http.MethodPost, "/api/learning/adjustments/abc/approve", withKey, http.StatusNotFound},
		{"a run has no sub-path", http.MethodGet, "/api/learning/runs/abc", withKey, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if rr := call(s, c.method, c.path, "", c.edit); rr.Code != c.want {
				t.Fatalf("%s %s = %d %s, want %d", c.method, c.path, rr.Code, rr.Body.String(), c.want)
			}
		})
	}
}

func TestFeedbackAboutCriticalEventsNeverBecomesAProposal(t *testing.T) {
	s := newAuthServer(t, nil)
	m := addMember(t, s, "member@corp.com")
	for i := 1; i <= 8; i++ {
		id := eventID(t, webhook(s, "security_advisory", fmt.Sprintf("crit-%d", i), fmt.Sprintf(`{"action":"published","repository":{"full_name":"acme/api"},"n":%d}`, i)))
		edit := withKey
		if i%2 == 0 {
			edit = m.apply(true)
		}
		call(s, http.MethodPost, "/feedback", `{"eventId":"`+id+`","action":"dismissed"}`, edit)
	}
	rr := call(s, http.MethodPost, "/api/learning/runs?wait=true", "", withKey)
	if !strings.Contains(rr.Body.String(), `"proposalsMade":0`) {
		t.Fatalf("critical feedback must teach nothing: %s", rr.Body.String())
	}
	if got := readAdjustments(t, s, "", withKey); len(got.Adjustments) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestAnExplicitOverrideStillWinsOverAnApprovedAdjustment(t *testing.T) {
	s := newAuthServer(t, nil)
	m := addMember(t, s, "member@corp.com")
	dismissedByTwoPeople(t, s, 5, m)
	call(s, http.MethodPost, "/api/learning/runs?wait=true", "", withKey)
	adj := readAdjustments(t, s, "", withKey).Adjustments[0]
	call(s, http.MethodPost, "/api/learning/adjustments/"+adj.ID+"/approve", "", withKey)

	if rr := call(s, http.MethodPost, "/api/overrides", `{"mode":"always","eventType":"pull_request"}`, withKey); rr.Code != http.StatusCreated {
		t.Fatalf("override = %d %s", rr.Code, rr.Body.String())
	}
	got := eventDecision(t, webhook(s, "pull_request", "override-wins", `{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":93}}`))
	if strings.Contains(got, "learned adjustment") || !strings.Contains(got, "always notify override") {
		t.Fatalf("a person's explicit choice outranks anything learned: %s", got)
	}
}
