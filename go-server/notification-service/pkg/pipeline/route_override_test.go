package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/delivery"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/policy"
)

func routingRules() []policy.Rule {
	return []policy.Rule{
		// A critical rule with a channel list set directly (the API refuses this): it must be ignored.
		{Name: "sec", When: policy.RuleCondition{Source: "github", EventType: "security_advisory"},
			Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "security", Channels: []string{"sms"}}},
		{Name: "big-pr", When: policy.RuleCondition{Source: "github", PayloadMatch: []policy.Predicate{{Field: "number", Op: policy.OpGt, Value: 100}}},
			Then: policy.RuleOutcome{AttentionClass: "standard", Reason: "a large pull request", Channels: []string{"sms"}}},
		{Name: "default", When: policy.RuleCondition{Source: "github"}, Then: policy.RuleOutcome{AttentionClass: "standard", Reason: "default"}},
	}
}

func routingPipeline() (*Pipeline, *delivery.MemoryStore, *event.InMemoryRepository, *recordingProvider) {
	p, repo, provider := newTestPipeline(routingRules())
	store := delivery.NewMemoryStore()
	targets := delivery.Targets{Slack: []string{"https://hooks.slack.com/x"}, SMS: []string{"+14155550100"}}
	out := delivery.NewOutbox(store, targets, map[string][]string{"standard": {delivery.ChannelSlack}})
	return p.WithOutbox(out).WithLogger(quiet), store, repo, provider
}

func prNumber(id string, n float64) event.Event {
	return event.Event{ID: id, Source: "github", EventType: "pull_request", Repository: "acme/web", Payload: map[string]any{"number": n}}
}

func TestARuleWithPayloadPredicatesAndChannelsRoutesOnlyMatchingEvents(t *testing.T) {
	p, store, repo, _ := routingPipeline()
	if _, err := p.Handle(context.Background(), prNumber("big", 250)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Handle(context.Background(), prNumber("small", 7)); err != nil {
		t.Fatal(err)
	}
	byEvent := map[string][]string{}
	for _, j := range jobs(t, store) {
		byEvent[j.EventID] = append(byEvent[j.EventID], j.Channel)
	}
	if len(byEvent["big"]) != 1 || byEvent["big"][0] != delivery.ChannelSMS {
		t.Fatalf("the matching event must go only to its rule's channel: %v", byEvent)
	}
	if len(byEvent["small"]) != 1 || byEvent["small"][0] != delivery.ChannelSlack {
		t.Fatalf("an event the rule does not match uses the class route: %v", byEvent)
	}
	if got := stored(t, repo, "big").DecisionState.MatchedRule; got != "big-pr" {
		t.Fatalf("matched rule = %q", got)
	}
}

func TestACriticalRuleCannotNarrowWhereACriticalEventGoes(t *testing.T) {
	p, store, _, provider := routingPipeline()
	res, err := p.Handle(context.Background(), event.Event{ID: "sec-1", Source: "github", EventType: "security_advisory", Repository: "acme/web"})
	if err != nil {
		t.Fatal(err)
	}
	if res.AttentionClass != event.AttentionCritical || res.DecisionState.Status != event.DecisionSent || len(provider.sent) != 1 {
		t.Fatalf("a critical event must be delivered inline to every channel: %+v sent=%d", res.DecisionState, len(provider.sent))
	}
	if len(res.RouteChannels) != 0 {
		t.Fatalf("a critical rule must not carry a channel override: %v", res.RouteChannels)
	}
	for _, j := range jobs(t, store) {
		if !j.Direct {
			t.Fatalf("a critical event must never be queued: %+v", j)
		}
	}
}

func TestTheWorkerRoutesByTheRulesChannelsToo(t *testing.T) {
	p, store, repo, _ := routingPipeline()
	p.WithAsyncAttention()
	if _, err := p.Handle(context.Background(), prNumber("big", 250)); err != nil {
		t.Fatal(err)
	}
	claimed, _ := repo.ClaimPending(context.Background(), time.Now(), time.Minute, 5)
	if len(claimed) != 1 {
		t.Fatalf("claimed %d", len(claimed))
	}
	out, err := p.ProcessPending(context.Background(), claimed[0])
	if err != nil {
		t.Fatal(err)
	}
	if out.DecisionState.Status != event.DecisionQueued {
		t.Fatalf("status = %s", out.DecisionState.Status)
	}
	list := jobs(t, store)
	if len(list) != 1 || list[0].Channel != delivery.ChannelSMS {
		t.Fatalf("the worker must use the rule's channels: %+v", list)
	}
}

func TestARuleChannelOverrideCountsAsRoutedEvenWhenTheClassHasNoRoute(t *testing.T) {
	p, repo, provider := newTestPipeline(routingRules())
	store := delivery.NewMemoryStore()
	targets := delivery.Targets{SMS: []string{"+14155550100"}}
	p.WithOutbox(delivery.NewOutbox(store, targets, nil)).WithLogger(quiet)
	_ = provider
	if _, err := p.Handle(context.Background(), prNumber("big", 250)); err != nil {
		t.Fatal(err)
	}
	if stored(t, repo, "big").DecisionState.Status != event.DecisionQueued || len(jobs(t, store)) != 1 {
		t.Fatalf("the rule's own channel is a route: %+v", stored(t, repo, "big").DecisionState)
	}
}
