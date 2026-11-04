package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/event"
)

func TestEngineMatchesEveryConditionField(t *testing.T) {
	engine := NewEngine([]Rule{{
		Name: "scoped",
		When: RuleCondition{Source: "github", EventType: "push", Repository: "octo/repo"},
		Then: RuleOutcome{AttentionClass: "critical", Reason: "r"},
	}})
	tests := []struct {
		name  string
		evt   event.Event
		match bool
	}{
		{"all fields match", event.Event{Source: "github", EventType: "push", Repository: "octo/repo"}, true},
		{"wrong source", event.Event{Source: "jira", EventType: "push", Repository: "octo/repo"}, false},
		{"wrong event type", event.Event{Source: "github", EventType: "star", Repository: "octo/repo"}, false},
		{"wrong repository", event.Event{Source: "github", EventType: "push", Repository: "other/repo"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := engine.Evaluate(tt.evt); ok != tt.match {
				t.Fatalf("match = %v, want %v", ok, tt.match)
			}
		})
	}
}

func TestEngineFirstMatchingRuleWins(t *testing.T) {
	engine := NewEngine([]Rule{
		{Name: "first", When: RuleCondition{Source: "github"}, Then: RuleOutcome{AttentionClass: "critical"}},
		{Name: "second", When: RuleCondition{Source: "github"}, Then: RuleOutcome{AttentionClass: "standard"}},
	})
	decision, ok := engine.Evaluate(event.Event{Source: "github"})
	if !ok || decision.MatchedRule != "first" {
		t.Fatalf("decision = %+v, ok = %v", decision, ok)
	}
}

func TestLoadRulesFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	yaml := "rules:\n  - name: r\n    when:\n      source: github\n    then:\n      attentionClass: background\n      reason: noise\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadRulesFromFile(path)
	if err != nil || len(rules) != 1 || rules[0].Then.AttentionClass != "background" {
		t.Fatalf("rules = %+v, err = %v", rules, err)
	}
}

func TestLoadRulesFromMissingFile(t *testing.T) {
	if _, err := LoadRulesFromFile(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParseRulesRejectsUnknownAttentionClass(t *testing.T) {
	_, err := ParseRules([]byte("rules:\n  - name: typo\n    then:\n      attentionClass: critcal\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown attentionClass") {
		t.Fatalf("a mistyped class must be rejected, got %v", err)
	}
}

func TestParseRulesRejectsInvalidYAML(t *testing.T) {
	if _, err := ParseRules([]byte("rules: [unclosed")); err == nil {
		t.Fatal("expected yaml error")
	}
}

func TestDefaultRulesAreValid(t *testing.T) {
	for _, r := range DefaultRules() {
		switch r.Then.AttentionClass {
		case event.AttentionCritical, event.AttentionStandard, event.AttentionBackground:
		default:
			t.Fatalf("default rule %s has unknown class %s", r.Name, r.Then.AttentionClass)
		}
	}
}
