package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/dedup"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/policy"
	"github.com/example/notification-service/pkg/preferences"
)

var outboxRules = []policy.Rule{
	{Name: "sec", When: policy.RuleCondition{Source: "github", EventType: "security_advisory"},
		Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "security"}},
	{Name: "default", When: policy.RuleCondition{Source: "github"},
		Then: policy.RuleOutcome{AttentionClass: "standard", Reason: "default"}},
}

func outboxPipeline(routes map[string][]string) (*Pipeline, *delivery.MemoryStore, *recordingProvider) {
	p, _, provider := newTestPipeline(outboxRules)
	store := delivery.NewMemoryStore()
	out := delivery.NewOutbox(store, delivery.Targets{Slack: []string{"https://hooks.slack.com/x"}}, routes)
	return p.WithOutbox(out), store, provider
}

func jobs(t *testing.T, s *delivery.MemoryStore) []delivery.Job {
	t.Helper()
	list, err := s.List(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestStandardEventIsQueuedForItsRoutedChannels(t *testing.T) {
	p, store, provider := outboxPipeline(map[string][]string{"standard": {delivery.ChannelSlack}})
	if _, err := p.Handle(context.Background(), prEvt("e1")); err != nil {
		t.Fatal(err)
	}
	list := jobs(t, store)
	if len(list) != 1 || list[0].Channel != delivery.ChannelSlack || list[0].Status != delivery.StatusPending || list[0].Direct {
		t.Fatalf("jobs = %+v", list)
	}
	if len(provider.sent) != 0 {
		t.Fatal("a standard event must not be sent inline")
	}
}

func TestNothingIsQueuedWithoutARoute(t *testing.T) {
	p, store, _ := outboxPipeline(nil)
	p.Handle(context.Background(), prEvt("e1"))
	if len(jobs(t, store)) != 0 {
		t.Fatal("with no route, a standard event is only recorded")
	}
}

type stubPrefs struct{ adj preferences.Adjustment }

func (s stubPrefs) Decide(event.Event) (preferences.Adjustment, bool) { return s.adj, true }

func TestHeldAndMutedEventsAreNotQueued(t *testing.T) {
	p, store, _ := outboxPipeline(map[string][]string{"standard": {delivery.ChannelSlack}})
	p.WithPreferences(stubPrefs{preferences.Adjustment{AttentionClass: "background", Status: event.DecisionDigest, Reason: "muted"}})
	p.Handle(context.Background(), prEvt("e1"))
	if len(jobs(t, store)) != 0 {
		t.Fatal("an event preferences sent to the digest must not be delivered now")
	}
}

func TestDuplicateEventsAreNotQueuedTwice(t *testing.T) {
	p, store, _ := outboxPipeline(map[string][]string{"standard": {delivery.ChannelSlack}})
	p.WithDeduper(dedup.NewMemoryDeduper(time.Minute))
	p.Handle(context.Background(), prEvt("e1"))
	p.Handle(context.Background(), prEvt("e2"))
	if n := len(jobs(t, store)); n != 1 {
		t.Fatalf("%d jobs for two duplicate events", n)
	}
}

func TestCriticalIsDeliveredInlineLoggedAndNeverQueued(t *testing.T) {
	p, store, provider := outboxPipeline(map[string][]string{"critical": {delivery.ChannelSlack}, "standard": {delivery.ChannelSlack}})
	_, err := p.Handle(context.Background(), event.Event{ID: "c1", Source: "github", EventType: "security_advisory", Repository: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.sent) != 1 {
		t.Fatal("critical must be delivered inline")
	}
	list := jobs(t, store)
	if len(list) != 1 || !list[0].Direct || list[0].Status != delivery.StatusSent || list[0].Channel != "recording" {
		t.Fatalf("log = %+v", list)
	}
}

type failingOutbox struct{}

func (failingOutbox) Enqueue(context.Context, delivery.Message) (int, error) {
	return 0, errors.New("queue down")
}
func (failingOutbox) Routed(string) bool { return false }
func (failingOutbox) RecordDirect(context.Context, delivery.Message, string, error) error {
	return errors.New("log down")
}

func TestOutboxFailureNeverFailsOrLosesTheEvent(t *testing.T) {
	p, repo, _ := newTestPipeline(outboxRules)
	p.WithOutbox(failingOutbox{})
	if _, err := p.Handle(context.Background(), prEvt("e1")); err != nil {
		t.Fatalf("a queue outage must not fail the event: %v", err)
	}
	if _, err := p.Handle(context.Background(), event.Event{ID: "c1", Source: "github", EventType: "security_advisory"}); err != nil {
		t.Fatalf("a log outage must not fail a critical event: %v", err)
	}
	if stored, _ := repo.List(context.Background()); len(stored) != 2 {
		t.Fatalf("stored %d events", len(stored))
	}
}
