package policy

type RuleCondition struct {
	Source     string `json:"source,omitempty" yaml:"source,omitempty"`
	EventType  string `json:"eventType,omitempty" yaml:"eventType,omitempty"`
	Repository string `json:"repository,omitempty" yaml:"repository,omitempty"`
}

type RuleOutcome struct {
	AttentionClass string `json:"attentionClass" yaml:"attentionClass"`
	Reason         string `json:"reason" yaml:"reason"`
}

type Rule struct {
	Name string        `json:"name" yaml:"name"`
	When RuleCondition `json:"when" yaml:"when"`
	Then RuleOutcome   `json:"then" yaml:"then"`
}

type PolicyDecision struct {
	AttentionClass string
	Reason         string
	MatchedRule    string
}
