package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/policy"
	"github.com/example/notification-service/pkg/preferences"
	"github.com/example/notification-service/pkg/stream"
)

type capturePublisher struct{ got []stream.Message }

func (c *capturePublisher) Publish(m stream.Message) { c.got = append(c.got, m) }

func prefPipeline(t *testing.T, prefs preferences.Preferences) (*Pipeline, *recordingProvider, *capturePublisher) {
	t.Helper()
	svc, err := preferences.NewService(context.Background(), preferences.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(context.Background(), prefs); err != nil {
		t.Fatal(err)
	}
	night := time.Date(2026, 6, 10, 23, 0, 0, 0, time.UTC)
	svc.WithClock(func() time.Time { return night })

	provider := &recordingProvider{}
	pub := &capturePublisher{}
	rules := []policy.Rule{
		{Name: "security", When: policy.RuleCondition{EventType: "security_advisory"}, Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "security"}},
		{Name: "default", Then: policy.RuleOutcome{AttentionClass: "standard", Reason: "default"}},
	}
	p := New(policy.NewEngine(rules), delivery.NewService(provider), event.NewInMemoryRepository(), Config{DefaultEmailRecipients: []string{"ops@example.com"}}).
		WithPreferences(svc).WithPublisher(pub)
	return p, provider, pub
}

func handle(t *testing.T, p *Pipeline, id, source, eventType string) event.Event {
	t.Helper()
	got, err := p.Handle(context.Background(), event.Event{ID: id, Source: source, EventType: eventType, Payload: map[string]interface{}{}})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPreferencesNeverTouchCriticalEvents(t *testing.T) {
	prefs := preferences.Default()
	prefs.QuietHours = preferences.QuietHours{Enabled: true, Start: "22:00", End: "07:00", Timezone: "UTC"}
	prefs.MutedSources = []string{"github"}
	p, provider, _ := prefPipeline(t, prefs)

	got := handle(t, p, "c1", "github", "security_advisory")
	if got.AttentionClass != event.AttentionCritical || got.DecisionState.Status != event.DecisionSent || len(provider.sent) != 1 {
		t.Fatalf("critical event was changed by preferences: %+v sent=%d", got.DecisionState, len(provider.sent))
	}
}

func TestMutedSourceBecomesBackgroundDigest(t *testing.T) {
	prefs := preferences.Default()
	prefs.MutedSources = []string{"gitlab"}
	p, provider, _ := prefPipeline(t, prefs)

	got := handle(t, p, "m1", "gitlab", "push")
	if got.AttentionClass != event.AttentionBackground || got.DecisionState.Status != event.DecisionDigest || got.DecisionState.MatchedRule != "default" {
		t.Fatalf("got %+v class=%s", got.DecisionState, got.AttentionClass)
	}
	if len(provider.sent) != 0 {
		t.Fatal("a muted event must not be delivered")
	}
}

func TestQuietHoursDelayStandardEvents(t *testing.T) {
	prefs := preferences.Default()
	prefs.QuietHours = preferences.QuietHours{Enabled: true, Start: "22:00", End: "07:00", Timezone: "UTC"}
	p, _, _ := prefPipeline(t, prefs)

	got := handle(t, p, "q1", "github", "push")
	if got.DecisionState.Status != event.DecisionDelayed || got.AttentionClass != event.AttentionStandard {
		t.Fatalf("got %+v class=%s", got.DecisionState, got.AttentionClass)
	}
}

func TestProcessedEventsAreAnnounced(t *testing.T) {
	p, _, pub := prefPipeline(t, preferences.Default())
	handle(t, p, "a1", "github", "push")
	if len(pub.got) != 1 || pub.got[0].Type != stream.TypeEvent || pub.got[0].ID != "a1" {
		t.Fatalf("published = %+v", pub.got)
	}
	// A redelivery is rejected before processing and must not announce anything.
	_, _ = p.Handle(context.Background(), event.Event{ID: "a1", Source: "github", EventType: "push", Payload: map[string]interface{}{}})
	if len(pub.got) != 1 {
		t.Fatalf("duplicate delivery was announced: %+v", pub.got)
	}
}
