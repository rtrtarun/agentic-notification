package policy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/agentpulse/notification-service/pkg/event"
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
	// Strict decoding: a misspelt key such as "eventTpye" fails at load time instead of
	// silently widening a rule to match everything.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("rules file contains no rules")
		}
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
		if err := ValidatePredicates(rule.When.PayloadMatch); err != nil {
			return nil, fmt.Errorf("rule %q: %w", rule.Name, err)
		}
		if err := ValidateChannels(rule.Then.AttentionClass, rule.Then.Channels); err != nil {
			return nil, fmt.Errorf("rule %q: %w", rule.Name, err)
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
			Then: RuleOutcome{AttentionClass: "standard", Reason: "default routing", Adaptive: true},
		},
	}
}
