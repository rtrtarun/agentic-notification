package policy

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/example/notification-service/pkg/event"
)

type RulesFile struct {
	Rules []Rule `yaml:"rules"`
}

func LoadRulesFromFile(path string) ([]Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rules file: %w", err)
	}
	return ParseRules(data)
}

func ParseRules(data []byte) ([]Rule, error) {
	var file RulesFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse rules yaml: %w", err)
	}
	if len(file.Rules) == 0 {
		return nil, fmt.Errorf("rules file contains no rules")
	}
	for i, rule := range file.Rules {
		if rule.Name == "" {
			return nil, fmt.Errorf("rule at index %d missing name", i)
		}
		if rule.Then.AttentionClass == "" {
			return nil, fmt.Errorf("rule %q missing attentionClass", rule.Name)
		}
		// A typo such as "critcal" would silently demote a rule to non-critical.
		switch rule.Then.AttentionClass {
		case event.AttentionCritical, event.AttentionStandard, event.AttentionBackground:
		default:
			return nil, fmt.Errorf("rule %q has unknown attentionClass %q", rule.Name, rule.Then.AttentionClass)
		}
		for _, pattern := range []string{rule.When.Source, rule.When.EventType, rule.When.Repository} {
			if err := ValidatePattern(pattern); err != nil {
				return nil, fmt.Errorf("rule %q has an invalid pattern %q: %w", rule.Name, pattern, err)
			}
		}
	}
	return file.Rules, nil
}

func DefaultRules() []Rule {
	return []Rule{
		{
			ID:     "builtin-security-advisory",
			Name:   "github-security-advisory",
			When:   RuleCondition{Source: "github", EventType: "security_advisory"},
			Then:   RuleOutcome{AttentionClass: "critical", Reason: "security vulnerability detected"},
			Locked: true,
		},
		{
			ID:   "builtin-default-github",
			Name: "default-github",
			When: RuleCondition{Source: "github"},
			Then: RuleOutcome{AttentionClass: "standard", Reason: "default routing"},
		},
	}
}
