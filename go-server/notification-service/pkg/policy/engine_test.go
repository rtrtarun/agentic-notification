package policy

import (
	"testing"

	"github.com/agentpulse/notification-service/pkg/event"
)

func TestEngineEvaluatesMatchingRule(t *testing.T) {
	engine := NewEngine([]Rule{{
		Name: "security-alerts",
		When: RuleCondition{Source: "github", EventType: "pull_request"},
		Then: RuleOutcome{AttentionClass: "critical", Reason: "security review"},
	}})

	decision, ok := engine.Evaluate(event.Event{Source: "github", EventType: "pull_request", Repository: "octo/repo"})
	if !ok {
		t.Fatal("expected a matching rule")
	}
	if decision.AttentionClass != "critical" {
		t.Fatalf("expected critical attention class, got %s", decision.AttentionClass)
	}
	if decision.MatchedRule != "security-alerts" {
		t.Fatalf("expected matched rule security-alerts, got %s", decision.MatchedRule)
	}
}

func TestEngineReturnsFalseWhenNoRuleMatches(t *testing.T) {
	engine := NewEngine([]Rule{{
		Name: "security-alerts",
		When: RuleCondition{Source: "github", EventType: "issues"},
		Then: RuleOutcome{AttentionClass: "standard", Reason: "team update"},
	}})

	_, ok := engine.Evaluate(event.Event{Source: "github", EventType: "pull_request"})
	if ok {
		t.Fatal("expected no rule to match")
	}
}
