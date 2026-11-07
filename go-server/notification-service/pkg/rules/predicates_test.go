package rules_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/policy"
	"github.com/example/notification-service/pkg/rules"
)

func agentRule() policy.Rule {
	return policy.Rule{
		Name: "  Slow agent ",
		When: policy.RuleCondition{
			Source: "agent",
			PayloadMatch: []policy.Predicate{
				{Field: " metadata.attempt ", Op: " GT ", Value: 3},
			},
		},
		Then: policy.RuleOutcome{AttentionClass: "standard", Reason: "an agent keeps retrying", Channels: []string{" Slack ", "EMAIL"}},
	}
}

func TestACreatedRuleKeepsItsPredicatesAndTidiedChannelsAndTakesEffect(t *testing.T) {
	svc, engine, _ := newService(t)
	created, err := svc.Create(context.Background(), agentRule())
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Slow agent" || created.When.PayloadMatch[0].Field != "metadata.attempt" || created.When.PayloadMatch[0].Op != "gt" {
		t.Fatalf("predicate not tidied: %+v", created.When)
	}
	if strings.Join(created.Then.Channels, ",") != "slack,email" {
		t.Fatalf("channels not tidied: %v", created.Then.Channels)
	}
	hit := event.Event{Source: "agent", Payload: map[string]any{"metadata": map[string]any{"attempt": 5}}}
	if d, ok := engine.Evaluate(hit); !ok || d.MatchedRule != "Slow agent" || strings.Join(d.Channels, ",") != "slack,email" {
		t.Fatalf("the new rule must apply at once: %+v %v", d, ok)
	}
	miss := event.Event{Source: "agent", Payload: map[string]any{"metadata": map[string]any{"attempt": 1}}}
	if d, _ := engine.Evaluate(miss); d.MatchedRule == "Slow agent" {
		t.Fatal("the predicate must keep the rule from matching a quiet run")
	}
}

func TestInvalidPredicatesAndChannelsAreRefusedWhenTheRuleIsSaved(t *testing.T) {
	svc, _, _ := newService(t)
	cases := map[string]func(*policy.Rule){
		"unknown operator":     func(r *policy.Rule) { r.When.PayloadMatch[0].Op = "like" },
		"threshold with text":  func(r *policy.Rule) { r.When.PayloadMatch[0].Value = "many" },
		"empty field":          func(r *policy.Rule) { r.When.PayloadMatch[0].Field = "" },
		"unknown channel":      func(r *policy.Rule) { r.Then.Channels = []string{"pager"} },
		"duplicate channel":    func(r *policy.Rule) { r.Then.Channels = []string{"slack", "SLACK"} },
		"channels on critical": func(r *policy.Rule) { r.Then.AttentionClass = "critical" },
		"too many predicates": func(r *policy.Rule) {
			for i := 0; i < 6; i++ {
				r.When.PayloadMatch = append(r.When.PayloadMatch, policy.Predicate{Field: "a", Value: 1})
			}
		},
	}
	for name, mutate := range cases {
		r := agentRule()
		mutate(&r)
		if _, err := svc.Create(context.Background(), r); !errors.Is(err, rules.ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestADryRunWithASamplePayloadShowsWhichRuleWinsAndItsChannels(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.Create(context.Background(), agentRule()); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Test(context.Background(), rules.Sample{Source: "agent", Payload: map[string]any{"metadata": map[string]any{"attempt": 9}}})
	if err != nil || res.MatchedRule != "Slow agent" || strings.Join(res.Channels, ",") != "slack,email" {
		t.Fatalf("with the payload: %+v %v", res, err)
	}
	res, _ = svc.Test(context.Background(), rules.Sample{Source: "agent"})
	if res.MatchedRule == "Slow agent" || len(res.Channels) != 0 {
		t.Fatalf("without the payload the predicate cannot hold: %+v", res)
	}
}
