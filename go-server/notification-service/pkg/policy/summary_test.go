package policy

import (
	"testing"
	"time"
)

func TestDescribeCountsRulesAndNamesLockedOnes(t *testing.T) {
	s := Describe(10*time.Minute, 72*time.Hour, []Rule{
		{Name: "security", Locked: true},
		{Name: "noisy", Disabled: true},
		{Name: "other"},
	})
	if s.DedupWindowSeconds != 600 || s.RetentionHours != 72 || s.RuleCount != 3 || s.DisabledRuleCount != 1 {
		t.Fatalf("summary = %+v", s)
	}
	if len(s.LockedRules) != 1 || s.LockedRules[0] != "security" {
		t.Fatalf("locked = %v", s.LockedRules)
	}
}

func TestDescribeCriticalIsNeverAIEligible(t *testing.T) {
	for _, c := range Describe(0, 0, nil).Classes {
		if c.Class == "critical" && c.AIEligible {
			t.Fatal("critical must never be AI eligible")
		}
	}
	if s := Describe(0, 0, nil); s.LockedRules == nil || len(s.Guardrails) == 0 {
		t.Fatal("locked rules must be an empty array, and guardrails must be listed")
	}
}
