package policy

import "testing"

func TestParseRulesValidYAML(t *testing.T) {
	data := []byte(`
rules:
  - name: test-rule
    when:
      source: github
      eventType: pull_request
    then:
      attentionClass: critical
      reason: security review
`)
	rules, err := ParseRules(data)
	if err != nil {
		t.Fatalf("parse rules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Name != "test-rule" {
		t.Fatalf("expected name test-rule, got %s", rules[0].Name)
	}
	if rules[0].Then.AttentionClass != "critical" {
		t.Fatalf("expected critical, got %s", rules[0].Then.AttentionClass)
	}
}

func TestParseRulesRejectsEmptyRules(t *testing.T) {
	_, err := ParseRules([]byte("rules: []"))
	if err == nil {
		t.Fatal("expected error for empty rules")
	}
}

func TestParseRulesRejectsMissingName(t *testing.T) {
	data := []byte(`
rules:
  - when:
      source: github
    then:
      attentionClass: standard
      reason: test
`)
	_, err := ParseRules(data)
	if err == nil {
		t.Fatal("expected error for missing rule name")
	}
}

func TestDefaultRulesIncludeCriticalSecurityRule(t *testing.T) {
	rules := DefaultRules()
	found := false
	for _, rule := range rules {
		if rule.Name == "github-security-advisory" {
			found = true
			if rule.Then.AttentionClass != "critical" {
				t.Fatalf("expected critical attention class")
			}
		}
	}
	if !found {
		t.Fatal("expected github-security-advisory rule in defaults")
	}
}
