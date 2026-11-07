package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/pipeline"
	"github.com/agentpulse/notification-service/pkg/policy"
	"github.com/agentpulse/notification-service/pkg/rules"
)

type ruleList struct {
	Rules []policy.Rule `json:"rules"`
}

func newRulesServer(t *testing.T) (*Server, *policy.Engine) {
	t.Helper()
	s := newInboxServer(t)
	engine := policy.NewEngine(nil)
	svc, err := rules.NewService(context.Background(), rules.NewMemoryStore(), engine, policy.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	s.rules = svc
	s.policyEngine = engine
	return s, engine
}

func TestRulesListCreateUpdateDelete(t *testing.T) {
	s, engine := newRulesServer(t)

	rr := inboxCall(s, http.MethodGet, "/api/rules", "")
	list := decode[ruleList](t, rr).Rules
	if rr.Code != http.StatusOK || len(list) != 2 || !list[0].Locked {
		t.Fatalf("list = %d %+v", rr.Code, list)
	}

	rr = inboxCall(s, http.MethodPost, "/api/rules", `{"name":"quiet-stars","when":{"source":"gitlab"},"then":{"attentionClass":"background","reason":"stars"}}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rr.Code, rr.Body.String())
	}
	created := decode[policy.Rule](t, rr)
	if d, ok := engine.Evaluate(event.Event{Source: "gitlab"}); !ok || d.MatchedRule != "quiet-stars" {
		t.Fatalf("engine did not pick up the new rule: %+v", d)
	}

	rr = inboxCall(s, http.MethodPut, "/api/rules/"+created.ID, `{"name":"quiet-stars","disabled":true,"when":{"source":"gitlab"},"then":{"attentionClass":"background"}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rr.Code, rr.Body.String())
	}
	if _, ok := engine.Evaluate(event.Event{Source: "gitlab"}); ok {
		t.Fatal("disabled rule still matches")
	}

	if rr = inboxCall(s, http.MethodDelete, "/api/rules/"+created.ID, ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rr.Code)
	}
	if rr = inboxCall(s, http.MethodDelete, "/api/rules/"+created.ID, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", rr.Code)
	}
}

func TestLockedRuleIsProtectedOverHTTP(t *testing.T) {
	s, _ := newRulesServer(t)
	locked := decode[ruleList](t, inboxCall(s, http.MethodGet, "/api/rules", "")).Rules[0]
	if rr := inboxCall(s, http.MethodDelete, "/api/rules/"+locked.ID, ""); rr.Code != http.StatusConflict {
		t.Fatalf("delete locked = %d", rr.Code)
	}
	body := `{"name":"x","then":{"attentionClass":"background"}}`
	if rr := inboxCall(s, http.MethodPut, "/api/rules/"+locked.ID, body); rr.Code != http.StatusConflict {
		t.Fatalf("update locked = %d", rr.Code)
	}
}

func TestRulesValidationAndMethods(t *testing.T) {
	s, _ := newRulesServer(t)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/rules", `{"name":"","then":{"attentionClass":"standard"}}`, http.StatusBadRequest},
		{http.MethodPost, "/api/rules", `{"name":"x","then":{"attentionClass":"critcal"}}`, http.StatusBadRequest},
		{http.MethodPost, "/api/rules", `not json`, http.StatusBadRequest},
		{http.MethodPut, "/api/rules/nope", `{"name":"x","then":{"attentionClass":"standard"}}`, http.StatusNotFound},
		{http.MethodPatch, "/api/rules", ``, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/rules/some-id", ``, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/rules/a/b", ``, http.StatusNotFound},
		{http.MethodPost, "/api/rules/reorder", `{"ids":["only-one"]}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if rr := inboxCall(s, c.method, c.path, c.body); rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d (%s)", c.method, c.path, rr.Code, c.want, rr.Body.String())
		}
	}
}

func TestRulesReorderAndTest(t *testing.T) {
	s, _ := newRulesServer(t)
	a := decode[policy.Rule](t, inboxCall(s, http.MethodPost, "/api/rules", `{"name":"a","then":{"attentionClass":"standard"}}`))
	b := decode[policy.Rule](t, inboxCall(s, http.MethodPost, "/api/rules", `{"name":"b","then":{"attentionClass":"standard"}}`))
	list := decode[ruleList](t, inboxCall(s, http.MethodGet, "/api/rules", "")).Rules

	ids := `["` + list[0].ID + `","` + list[1].ID + `","` + b.ID + `","` + a.ID + `"]`
	rr := inboxCall(s, http.MethodPost, "/api/rules/reorder", `{"ids":`+ids+`}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("reorder = %d %s", rr.Code, rr.Body.String())
	}
	if got := decode[ruleList](t, rr).Rules; got[2].Name != "b" || got[3].Name != "a" {
		t.Fatalf("order = %+v", got)
	}

	rr = inboxCall(s, http.MethodPost, "/api/rules/test", `{"source":"github","eventType":"security_advisory"}`)
	res := decode[rules.Result](t, rr)
	if rr.Code != http.StatusOK || res.AttentionClass != "critical" || !res.Bypassed || res.MatchedRule != "github-security-advisory" {
		t.Fatalf("test = %d %+v", rr.Code, res)
	}
}

func TestPoliciesEndpoint(t *testing.T) {
	s, _ := newRulesServer(t)
	s.cfg.DedupWindow = 600_000_000_000
	rr := inboxCall(s, http.MethodGet, "/api/policies", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	sum := decode[policy.Summary](t, rr)
	if sum.DedupWindowSeconds != 600 || sum.RuleCount != 2 || len(sum.LockedRules) != 1 || len(sum.Guardrails) == 0 {
		t.Fatalf("summary = %+v", sum)
	}
	if rr := inboxCall(s, http.MethodPost, "/api/policies", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rr.Code)
	}
}

func TestRulesRequireAPIKeyAndService(t *testing.T) {
	s, _ := newRulesServer(t)
	for _, path := range []string{"/api/rules", "/api/policies"} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s without key = %d", path, rr.Code)
		}
	}
	s.rules = nil
	if rr := inboxCall(s, http.MethodGet, "/api/rules", ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no service = %d", rr.Code)
	}
}

// The rules a person edits must change what the real pipeline does with the next event.
func TestEditedRulesChangeThePipelineDecision(t *testing.T) {
	s, engine := newRulesServer(t)
	p := pipeline.New(engine, nil, event.NewInMemoryRepository(), pipeline.Config{})

	evt := event.Event{ID: "e1", Source: "gitlab", EventType: "push", Payload: map[string]interface{}{}}
	got, err := p.Handle(context.Background(), evt)
	if err != nil || got.AttentionClass != event.AttentionStandard {
		t.Fatalf("before = %+v %v", got, err)
	}
	rr := inboxCall(s, http.MethodPost, "/api/rules", `{"name":"gitlab-quiet","when":{"source":"gitlab"},"then":{"attentionClass":"background","reason":"quiet"}}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create = %d", rr.Code)
	}
	evt.ID = "e2"
	got, err = p.Handle(context.Background(), evt)
	if err != nil || got.AttentionClass != event.AttentionBackground || got.DecisionState.MatchedRule != "gitlab-quiet" {
		t.Fatalf("after = %+v %v", got, err)
	}
}
