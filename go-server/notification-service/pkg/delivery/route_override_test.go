package delivery

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/policy"
)

func routedOutbox() (*Outbox, *MemoryStore) {
	store := NewMemoryStore()
	targets := Targets{Slack: []string{"https://hooks.slack.com/x"}, SMS: []string{"+14155550100"}, Email: []string{"ops@example.com"}}
	return NewOutbox(store, targets, map[string][]string{"standard": {ChannelSlack}}), store
}

func channelsOf(t *testing.T, s *MemoryStore) string {
	t.Helper()
	list, err := s.List(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, j := range list {
		out = append(out, j.Channel)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestARuleChannelOverrideReplacesTheClassRoute(t *testing.T) {
	o, store := routedOutbox()
	n, err := o.Enqueue(context.Background(), Message{EventID: "e1", Class: "standard", Title: "t", Body: "b", Channels: []string{ChannelSMS, ChannelEmail}})
	if err != nil || n != 2 || channelsOf(t, store) != "email,sms" {
		t.Fatalf("n=%d err=%v channels=%s", n, err, channelsOf(t, store))
	}
	if n, _ := o.Enqueue(context.Background(), Message{EventID: "e2", Class: "standard", Title: "t", Body: "b"}); n != 1 {
		t.Fatalf("without an override the class route applies, got %d", n)
	}
}

func TestAnOverrideNamingAnUnconfiguredChannelDeliversNothingThere(t *testing.T) {
	o, store := routedOutbox()
	if n, _ := o.Enqueue(context.Background(), Message{EventID: "e1", Class: "standard", Channels: []string{ChannelTeams}}); n != 0 || channelsOf(t, store) != "" {
		t.Fatalf("a channel with no target must be skipped, got %q", channelsOf(t, store))
	}
}

func TestACriticalMessageIsNeverQueuedEvenWithAnOverride(t *testing.T) {
	o, store := routedOutbox()
	if n, _ := o.Enqueue(context.Background(), Message{EventID: "e1", Class: "critical", Channels: []string{ChannelSlack}}); n != 0 || channelsOf(t, store) != "" {
		t.Fatal("critical events are delivered inline, never queued")
	}
}

func TestRoutedForCountsOnlyConfiguredChannels(t *testing.T) {
	o, _ := routedOutbox()
	cases := []struct {
		class    string
		override []string
		want     bool
	}{
		{"standard", nil, true},
		{"background", nil, false},
		{"background", []string{ChannelSMS}, true},
		{"standard", []string{ChannelTeams}, false},
		{"standard", []string{ChannelTeams, ChannelEmail}, true},
	}
	for _, c := range cases {
		if got := o.RoutedFor(c.class, c.override); got != c.want {
			t.Errorf("RoutedFor(%s, %v) = %v, want %v", c.class, c.override, got, c.want)
		}
	}
}

// The rule engine cannot import the delivery package, so it keeps its own list of
// channel names. This test makes sure the two never drift apart.
func TestPolicyKnowsExactlyTheChannelsDeliveryKnows(t *testing.T) {
	got := append([]string(nil), policy.KnownChannels...)
	want := append([]string(nil), channelOrder...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("policy.KnownChannels = %v but delivery knows %v", got, want)
	}
}
