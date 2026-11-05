package policy

// RuleCondition says which events a rule applies to. An empty field matches
// anything. Each field may use * as a wildcard, for example "acme/*".
type RuleCondition struct {
	Source     string `json:"source,omitempty" yaml:"source,omitempty"`
	EventType  string `json:"eventType,omitempty" yaml:"eventType,omitempty"`
	Repository string `json:"repository,omitempty" yaml:"repository,omitempty"`
}

type RuleOutcome struct {
	AttentionClass string `json:"attentionClass" yaml:"attentionClass"`
	Reason         string `json:"reason" yaml:"reason"`
}

// Rule maps a condition to an attention class. Rules run top to bottom and the
// first match wins.
type Rule struct {
	// ID is assigned by the service; rules from a file get one when they are first stored.
	ID          string        `json:"id,omitempty" yaml:"id,omitempty"`
	Name        string        `json:"name" yaml:"name"`
	Description string        `json:"description,omitempty" yaml:"description,omitempty"`
	When        RuleCondition `json:"when" yaml:"when"`
	Then        RuleOutcome   `json:"then" yaml:"then"`
	// Disabled rules are kept but never match.
	Disabled bool `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	// Locked rules protect critical routing: they cannot be edited, disabled or
	// removed through the API, and always run before unlocked rules.
	Locked bool `json:"locked,omitempty" yaml:"locked,omitempty"`
}

type PolicyDecision struct {
	AttentionClass string
	Reason         string
	MatchedRule    string
}
