package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/delivery"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/overrides"
	"github.com/agentpulse/notification-service/pkg/preferences"
)

func overridePipeline(t *testing.T) (*Pipeline, *overrides.Service, *delivery.MemoryStore, *recordingProvider) {
	t.Helper()
	p, store, provider := outboxPipeline(map[string][]string{"standard": {delivery.ChannelSlack}})
	ov, err := overrides.NewService(context.Background(), overrides.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	return p.WithOverrides(ov), ov, store, provider
}

func TestNeverOverrideSendsAStandardEventToTheDigestAndQueuesNothing(t *testing.T) {
	p, ov, store, _ := overridePipeline(t)
	ov.Upsert(context.Background(), overrides.Override{Mode: overrides.ModeNever, Source: "github", EventType: "pull_request", CreatedBy: "ann"})

	res, err := p.Handle(context.Background(), prEvt("e1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.AttentionClass != event.AttentionBackground || res.DecisionState.Status != event.DecisionDigest {
		t.Fatalf("class %s status %s", res.AttentionClass, res.DecisionState.Status)
	}
	if !strings.Contains(res.DecisionState.Reason, "never notify override for github / pull_request") {
		t.Fatalf("the decision must name the override: %q", res.DecisionState.Reason)
	}
	if len(jobs(t, store)) != 0 {
		t.Fatal("a never-notify event must not be delivered")
	}
}

func TestAlwaysOverrideBeatsPreferencesThatWouldHoldTheEvent(t *testing.T) {
	p, ov, store, _ := overridePipeline(t)
	p.WithPreferences(stubPrefs{preferences.Adjustment{AttentionClass: "background", Status: event.DecisionDigest, Reason: "muted"}})
	ov.Upsert(context.Background(), overrides.Override{Mode: overrides.ModeAlways, Source: "github", CreatedBy: "ann"})

	res, _ := p.Handle(context.Background(), prEvt("e1"))
	if res.DecisionState.Status != event.DecisionQueued || res.AttentionClass != event.AttentionStandard {
		t.Fatalf("always must keep the event deliverable: class %s status %s", res.AttentionClass, res.DecisionState.Status)
	}
	if len(jobs(t, store)) != 1 {
		t.Fatal("an always-notify event must be delivered")
	}
}

func TestNeverOverrideBeatsPreferences(t *testing.T) {
	p, ov, _, _ := overridePipeline(t)
	p.WithPreferences(stubPrefs{preferences.Adjustment{AttentionClass: "standard", Status: event.DecisionQueued, Reason: "no"}})
	ov.Upsert(context.Background(), overrides.Override{Mode: overrides.ModeNever, Source: "github", CreatedBy: "ann"})
	res, _ := p.Handle(context.Background(), prEvt("e1"))
	if res.DecisionState.Status != event.DecisionDigest {
		t.Fatalf("status = %s", res.DecisionState.Status)
	}
}

func TestOverridesNeverTouchACriticalEvent(t *testing.T) {
	p, ov, store, provider := overridePipeline(t)
	ov.Upsert(context.Background(), overrides.Override{Mode: overrides.ModeNever, Source: "github", CreatedBy: "ann"})

	res, err := p.Handle(context.Background(), event.Event{ID: "c1", Source: "github", EventType: "security_advisory", Repository: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	if res.AttentionClass != event.AttentionCritical || res.DecisionState.Status != event.DecisionSent {
		t.Fatalf("a never override must not downgrade a critical event: %s / %s", res.AttentionClass, res.DecisionState.Status)
	}
	if len(provider.sent) != 1 {
		t.Fatal("the critical event must still be delivered")
	}
	for _, j := range jobs(t, store) {
		if !j.Direct {
			t.Fatalf("critical must never be queued: %+v", j)
		}
	}
}

func TestEventsWithoutAMatchingOverrideAreUnaffected(t *testing.T) {
	p, ov, store, _ := overridePipeline(t)
	ov.Upsert(context.Background(), overrides.Override{Mode: overrides.ModeNever, Source: "gitlab", CreatedBy: "ann"})
	res, _ := p.Handle(context.Background(), prEvt("e1"))
	if res.DecisionState.Status != event.DecisionQueued || len(jobs(t, store)) != 1 {
		t.Fatalf("status %s", res.DecisionState.Status)
	}
}
