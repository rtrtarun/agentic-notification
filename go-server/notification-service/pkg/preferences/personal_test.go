package preferences

import (
	"context"
	"errors"
	"testing"

	"github.com/agentpulse/notification-service/pkg/event"
)

func personalService(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(context.Background(), NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	return s.WithPersonal(NewMemoryPersonalStore())
}

func TestAPersonFallsBackToTheWorkspaceUntilTheySaveTheirOwn(t *testing.T) {
	ctx := context.Background()
	s := personalService(t)
	if _, err := s.Update(ctx, Preferences{MutedSources: []string{"jira"}, QuietHours: Default().QuietHours}); err != nil {
		t.Fatal(err)
	}
	got, personal, err := s.GetFor(ctx, "alice")
	if err != nil || personal || len(got.MutedSources) != 1 || got.MutedSources[0] != "jira" {
		t.Fatalf("fallback = %+v personal=%v err=%v", got, personal, err)
	}
	if _, err := s.UpdateFor(ctx, "alice", Preferences{MutedSources: []string{"github"}, QuietHours: Default().QuietHours}); err != nil {
		t.Fatal(err)
	}
	got, personal, _ = s.GetFor(ctx, "alice")
	if !personal || got.MutedSources[0] != "github" {
		t.Fatalf("own preferences = %+v personal=%v", got, personal)
	}
	if other, personal, _ := s.GetFor(ctx, "bob"); personal || other.MutedSources[0] != "jira" {
		t.Fatalf("bob must still see the workspace's: %+v", other)
	}
	if ws := s.Get(); ws.MutedSources[0] != "jira" {
		t.Fatalf("a personal change must not alter the workspace preferences: %+v", ws)
	}
}

func TestPersonalPreferencesAreValidatedAndNeedAPerson(t *testing.T) {
	ctx := context.Background()
	s := personalService(t)
	bad := Default()
	bad.QuietHours.Start = "late"
	if _, err := s.UpdateFor(ctx, "alice", bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.UpdateFor(ctx, "", Default()); !errors.Is(err, ErrNoUser) {
		t.Fatalf("got %v", err)
	}
	plain, _ := NewService(ctx, NewMemoryStore())
	if _, err := plain.UpdateFor(ctx, "alice", Default()); !errors.Is(err, ErrNoUser) {
		t.Fatalf("without a personal store: %v", err)
	}
	if got, personal, err := plain.GetFor(ctx, "alice"); err != nil || personal || got.QuietHours.Timezone != "UTC" {
		t.Fatalf("GetFor without a store = %+v %v %v", got, personal, err)
	}
	if got, personal, _ := s.GetFor(ctx, ""); personal || got.QuietHours.Timezone != "UTC" {
		t.Fatalf("no person means the workspace's: %+v", got)
	}
}

func TestMutedNeverAppliesToACriticalEvent(t *testing.T) {
	p := Preferences{MutedSources: []string{"github"}, MutedRepositories: []string{"acme/*"}}
	std := event.Event{Source: "github", Repository: "acme/web", AttentionClass: event.AttentionStandard}
	crit := std
	crit.AttentionClass = event.AttentionCritical
	if !p.Muted(std) || p.Muted(crit) {
		t.Fatalf("standard muted=%v critical muted=%v", p.Muted(std), p.Muted(crit))
	}
}

func TestPersonalStoreHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := NewMemoryPersonalStore()
	if _, _, err := m.LoadFor(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("load: %v", err)
	}
	if err := m.SaveFor(ctx, "a", Default()); !errors.Is(err, context.Canceled) {
		t.Fatalf("save: %v", err)
	}
}

// personalStoreContract is the behaviour every PersonalStore must have. It runs on the
// memory store always and on PostgreSQL in the integration test.
func personalStoreContract(t *testing.T, s PersonalStore) {
	t.Helper()
	ctx := context.Background()
	if _, ok, err := s.LoadFor(ctx, "alice"); err != nil || ok {
		t.Fatalf("a person with nothing saved: ok=%v err=%v", ok, err)
	}
	a := Default()
	a.MutedSources = []string{"github"}
	b := Default()
	b.MutedRepositories = []string{"acme/*"}
	for id, p := range map[string]Preferences{"alice": a, "bob": b} {
		if err := s.SaveFor(ctx, id, p); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := s.LoadFor(ctx, "alice")
	if err != nil || !ok || len(got.MutedSources) != 1 || got.MutedSources[0] != "github" || len(got.MutedRepositories) != 0 {
		t.Fatalf("alice = %+v ok=%v err=%v", got, ok, err)
	}
	if got, _, _ := s.LoadFor(ctx, "bob"); len(got.MutedRepositories) != 1 {
		t.Fatalf("bob = %+v", got)
	}
	a.MutedSources = []string{"jira"}
	if err := s.SaveFor(ctx, "alice", a); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.LoadFor(ctx, "alice"); got.MutedSources[0] != "jira" {
		t.Fatalf("saving again must replace: %+v", got)
	}
}

func TestMemoryPersonalStoreContract(t *testing.T) {
	personalStoreContract(t, NewMemoryPersonalStore())
}
