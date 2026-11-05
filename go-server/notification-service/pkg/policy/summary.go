package policy

import "time"

// Guardrail is a rule of the platform that no setting or rule can switch off.
type Guardrail struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ClassPolicy says what happens to events of one attention class.
type ClassPolicy struct {
	Class       string `json:"class"`
	Level       string `json:"level"`
	Delivery    string `json:"delivery"`
	Dedup       string `json:"dedup"`
	AIEligible  bool   `json:"aiEligible"`
	Description string `json:"description"`
}

// Summary describes the effective policy in plain data, for the Policies screen.
type Summary struct {
	Guardrails         []Guardrail   `json:"guardrails"`
	Classes            []ClassPolicy `json:"classes"`
	DedupWindowSeconds int64         `json:"dedupWindowSeconds"`
	RetentionHours     int64         `json:"retentionHours"`
	LockedRules        []string      `json:"lockedRules"`
	RuleCount          int           `json:"ruleCount"`
	DisabledRuleCount  int           `json:"disabledRuleCount"`
}

// Describe builds the summary. The guardrails and class behaviour are facts of
// the pipeline, not settings; the window, retention and rules are live values.
func Describe(dedupWindow, retention time.Duration, rules []Rule) Summary {
	s := Summary{
		Guardrails: []Guardrail{
			{ID: "never-suppress-critical", Title: "Critical events are never suppressed",
				Description: "Critical events skip deduplication, budgets and digests. They are delivered to every configured channel."},
			{ID: "no-ai-in-layer-1", Title: "No AI in the policy layer",
				Description: "Classification by rules, deduplication and threading are deterministic. Critical events never reach the AI layer."},
			{ID: "locked-rules-first", Title: "Locked rules run first",
				Description: "Rules that protect critical routing cannot be edited, disabled, removed or placed below other rules."},
			{ID: "critical-priority-fixed", Title: "Critical priority cannot be lowered",
				Description: "A person can raise or lower standard and background notifications, never a critical one."},
		},
		Classes: []ClassPolicy{
			{Class: "critical", Level: "critical", Delivery: "immediate, every configured channel", Dedup: "never merged", AIEligible: false,
				Description: "Security alerts, incidents, payment failures and outages."},
			{Class: "standard", Level: "high or normal", Delivery: "held and shown in the inbox", Dedup: "repeats merged inside the window", AIEligible: true,
				Description: "Normal work that does not need an interruption."},
			{Class: "background", Level: "low", Delivery: "recorded quietly", Dedup: "repeats merged inside the window", AIEligible: true,
				Description: "Low-value signals such as stars and watches."},
		},
		DedupWindowSeconds: int64(dedupWindow / time.Second),
		RetentionHours:     int64(retention / time.Hour),
		LockedRules:        []string{},
		RuleCount:          len(rules),
	}
	for _, r := range rules {
		if r.Locked {
			s.LockedRules = append(s.LockedRules, r.Name)
		}
		if r.Disabled {
			s.DisabledRuleCount++
		}
	}
	return s
}
