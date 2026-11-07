package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/config"
)

const bigPullRequestRule = `{
  "name": "big-pr",
  "when": {"source": "github", "payloadMatch": [{"field": "pull_request.number", "op": "gt", "value": 100}]},
  "then": {"attentionClass": "standard", "reason": "a large pull request", "channels": ["sms"]}
}`

// putRuleFirst creates the rule and moves it ahead of the catch-all, because the first match wins.
func putRuleFirst(t *testing.T, s *Server, body string) {
	t.Helper()
	rr := call(s, http.MethodPost, "/api/rules", body, withKey)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create rule = %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	list := call(s, http.MethodGet, "/api/rules", "", withKey)
	var all struct {
		Rules []struct {
			ID     string `json:"id"`
			Locked bool   `json:"locked"`
		} `json:"rules"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &all)
	var locked, others []string
	for _, r := range all.Rules {
		switch {
		case r.Locked:
			locked = append(locked, r.ID)
		case r.ID != created.ID:
			others = append(others, r.ID)
		}
	}
	ids := append(append(locked, created.ID), others...)
	payload, _ := json.Marshal(map[string]any{"ids": ids})
	if rr := call(s, http.MethodPost, "/api/rules/reorder", string(payload), withKey); rr.Code != http.StatusOK {
		t.Fatalf("reorder = %d %s", rr.Code, rr.Body.String())
	}
}

func pr(s *Server, n int) {
	body := fmt.Sprintf(`{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":%d},"sender":{"login":"alice"}}`, n)
	webhook(s, "pull_request", fmt.Sprintf("pr-%d", n), body)
}

func TestARulesPayloadPredicateAndChannelsRouteOverHTTP(t *testing.T) {
	// No class route at all: only a rule's own channels can deliver anything.
	s := newDeliveryServer(t, func(c *config.Config) { c.StandardChannels = nil; c.AttentionBudgetPerHour = 0 })
	putRuleFirst(t, s, bigPullRequestRule)
	pr(s, 250) // matches the predicate
	pr(s, 7)   // does not
	list, err := s.deliveryStore.List(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Channel != "sms" || !strings.Contains(list[0].EventID, "pr-250") {
		t.Fatalf("only the large pull request should be delivered, to its rule's channel: %+v", list)
	}
}

func TestADryRunWithASamplePayloadMatchesThePredicate(t *testing.T) {
	s := newDeliveryServer(t, nil)
	putRuleFirst(t, s, bigPullRequestRule)
	hit := call(s, http.MethodPost, "/api/rules/test", `{"source":"github","eventType":"pull_request","payload":{"pull_request":{"number":500}}}`, withKey).Body.String()
	if !strings.Contains(hit, `"matchedRule":"big-pr"`) || !strings.Contains(hit, `"channels":["sms"]`) {
		t.Fatalf("dry run with the payload = %s", hit)
	}
	miss := call(s, http.MethodPost, "/api/rules/test", `{"source":"github","eventType":"pull_request","payload":{"pull_request":{"number":3}}}`, withKey).Body.String()
	if strings.Contains(miss, `"matchedRule":"big-pr"`) {
		t.Fatalf("a small pull request must not match: %s", miss)
	}
}

func TestTheAPIRefusesBadPredicatesAndChannelsOnCriticalRules(t *testing.T) {
	s := newDeliveryServer(t, nil)
	for name, body := range map[string]string{
		"unknown operator":     `{"name":"a","when":{"payloadMatch":[{"field":"x","op":"like","value":1}]},"then":{"attentionClass":"standard","reason":"r"}}`,
		"threshold with text":  `{"name":"b","when":{"payloadMatch":[{"field":"x","op":"gt","value":"lots"}]},"then":{"attentionClass":"standard","reason":"r"}}`,
		"unknown channel":      `{"name":"c","when":{},"then":{"attentionClass":"standard","reason":"r","channels":["pager"]}}`,
		"channels on critical": `{"name":"d","when":{},"then":{"attentionClass":"critical","reason":"r","channels":["sms"]}}`,
	} {
		if rr := call(s, http.MethodPost, "/api/rules", body, withKey); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d %s, want 400", name, rr.Code, rr.Body.String())
		}
	}
}
