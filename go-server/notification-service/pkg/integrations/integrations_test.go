package integrations_test

import (
	"testing"
	"time"

	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/integrations"
)

func TestStatusFollowsTheWebhookAuthRules(t *testing.T) {
	cases := []struct {
		name string
		in   integrations.GitHubSettings
		want string
	}{
		{"secret set", integrations.GitHubSettings{SecretConfigured: true}, integrations.StatusReady},
		{"secret wins over unsigned", integrations.GitHubSettings{SecretConfigured: true, AllowUnsigned: true}, integrations.StatusReady},
		{"unsigned allowed", integrations.GitHubSettings{AllowUnsigned: true}, integrations.StatusUnsigned},
		{"nothing set fails closed", integrations.GitHubSettings{}, integrations.StatusBlocked},
	}
	for _, c := range cases {
		if got := integrations.GitHub(c.in, nil); got.Status != c.want || got.Signing == "" || got.WebhookPath != "/webhooks/github" {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestCountsOnlyGitHubEventsAndFindsTheLatest(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	events := []event.Event{
		{Source: "github", EventType: "push", CreatedAt: base},
		{Source: "github", EventType: "push", CreatedAt: base.Add(time.Minute)},
		{Source: "github", EventType: "issues", CreatedAt: base.Add(2 * time.Minute)},
		{Source: "gitlab", EventType: "push", CreatedAt: base.Add(time.Hour)},
	}
	got := integrations.GitHub(integrations.GitHubSettings{SecretConfigured: true}, events)
	if got.EventsReceived != 3 || got.LastEventType != "issues" || !got.LastEventAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("got = %+v", got)
	}
	if len(got.EventTypes) != 2 || got.EventTypes[0].EventType != "push" || got.EventTypes[0].Count != 2 {
		t.Fatalf("types = %+v", got.EventTypes)
	}
}

func TestNoEventsGivesEmptyArraysNotNull(t *testing.T) {
	got := integrations.GitHub(integrations.GitHubSettings{}, nil)
	if got.EventTypes == nil || got.LastEventAt != nil || got.EventsReceived != 0 {
		t.Fatalf("got = %+v", got)
	}
}

func TestEventTypesAreCapped(t *testing.T) {
	var events []event.Event
	for i := 0; i < 12; i++ {
		events = append(events, event.Event{Source: "github", EventType: string(rune('a' + i))})
	}
	if got := integrations.GitHub(integrations.GitHubSettings{}, events); len(got.EventTypes) != 8 {
		t.Fatalf("types = %d", len(got.EventTypes))
	}
}
