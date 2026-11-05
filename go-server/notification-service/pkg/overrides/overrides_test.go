package overrides

import (
	"context"
	"errors"
	"testing"

	"github.com/example/notification-service/pkg/event"
)

var ctx = context.Background()

func newSvc(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(ctx, NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func evt(source, typ, repo, class string) event.Event {
	return event.Event{Source: source, EventType: typ, Repository: repo, AttentionClass: class}
}

func TestUpsertValidates(t *testing.T) {
	s := newSvc(t)
	for name, o := range map[string]Override{
		"no mode":           {Source: "github"},
		"bad mode":          {Source: "github", Mode: "sometimes"},
		"empty scope":       {Mode: ModeNever},
		"whitespace scope":  {Mode: ModeNever, Source: "   "},
		"too long":          {Mode: ModeNever, Source: string(make([]byte, 300))},
		"malformed pattern": {Mode: ModeNever, Repository: "acme/["},
	} {
		if _, err := s.Upsert(ctx, o); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestDecideMatchesByScope(t *testing.T) {
	s := newSvc(t)
	s.Upsert(ctx, Override{Mode: ModeNever, Source: "github", EventType: "star", CreatedBy: "a"})
	s.Upsert(ctx, Override{Mode: ModeNever, Repository: "acme/*", CreatedBy: "a"})

	for _, c := range []struct {
		name string
		e    event.Event
		want bool
	}{
		{"same type", evt("github", "star", "x/y", "background"), true},
		{"case insensitive", evt("GitHub", "STAR", "x/y", "standard"), true},
		{"other type", evt("github", "push", "x/y", "standard"), false},
		{"repository wildcard", evt("gitlab", "push", "acme/web", "standard"), true},
		{"wildcard does not cross a slash", evt("gitlab", "push", "acme/web/extra", "standard"), false},
		{"unrelated", evt("jira", "issue", "other/web", "standard"), false},
	} {
		if _, got := s.Decide(c.e); got != c.want {
			t.Errorf("%s: matched = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCriticalEventsAreNeverOverridden(t *testing.T) {
	s := newSvc(t)
	s.Upsert(ctx, Override{Mode: ModeNever, Source: "github", CreatedBy: "a"})
	if _, ok := s.Decide(evt("github", "security_advisory", "acme/web", event.AttentionCritical)); ok {
		t.Fatal("no override may ever apply to a critical event")
	}
}

func TestMostSpecificWinsAndAlwaysWinsATie(t *testing.T) {
	s := newSvc(t)
	s.Upsert(ctx, Override{Mode: ModeNever, Source: "github", CreatedBy: "a"})
	s.Upsert(ctx, Override{Mode: ModeAlways, Source: "github", EventType: "pull_request", CreatedBy: "a"})
	d, _ := s.Decide(evt("github", "pull_request", "x/y", "standard"))
	if d.Mode != ModeAlways {
		t.Fatalf("the more specific override must win, got %s", d.Mode)
	}
	d, _ = s.Decide(evt("github", "push", "x/y", "standard"))
	if d.Mode != ModeNever {
		t.Fatalf("the broader override applies to the rest, got %s", d.Mode)
	}

	tie := newSvc(t)
	tie.Upsert(ctx, Override{Mode: ModeNever, Source: "github", CreatedBy: "a"})
	tie.Upsert(ctx, Override{Mode: ModeAlways, Repository: "acme/web", CreatedBy: "a"})
	d, _ = tie.Decide(evt("github", "push", "acme/web", "standard"))
	if d.Mode != ModeAlways {
		t.Fatalf("on a tie always must win: missing something is worse than one extra message, got %s", d.Mode)
	}
}

func TestSameScopeReplacesTheMode(t *testing.T) {
	s := newSvc(t)
	first, _ := s.Upsert(ctx, Override{Mode: ModeNever, Source: "github", EventType: "star", CreatedBy: "a"})
	second, err := s.Upsert(ctx, Override{Mode: ModeAlways, Source: "GITHUB", EventType: "Star", CreatedBy: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || len(s.List()) != 1 || s.List()[0].Mode != ModeAlways {
		t.Fatalf("list = %+v", s.List())
	}
}

func TestDeleteAndReload(t *testing.T) {
	store := NewMemoryStore()
	s, _ := NewService(ctx, store)
	o, _ := s.Upsert(ctx, Override{Mode: ModeNever, Source: "github", CreatedBy: "a"})
	reloaded, _ := NewService(ctx, store)
	if len(reloaded.List()) != 1 {
		t.Fatal("overrides must survive a restart")
	}
	if err := s.Delete(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Decide(evt("github", "push", "", "standard")); ok {
		t.Fatal("a deleted override must stop applying at once")
	}
	if err := s.Delete(ctx, o.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestReasonNamesTheOverride(t *testing.T) {
	s := newSvc(t)
	s.Upsert(ctx, Override{Mode: ModeNever, Source: "github", EventType: "star", CreatedBy: "a"})
	d, _ := s.Decide(evt("github", "star", "", "standard"))
	if d.Reason != "never notify override for github / star" || d.OverrideID == "" {
		t.Fatalf("decision = %+v", d)
	}
	if Describe(Override{Repository: "acme/*"}) != "repository acme/*" {
		t.Fatal("describe")
	}
}

func TestListIsNewestFirst(t *testing.T) {
	s := newSvc(t)
	a, _ := s.Upsert(ctx, Override{Mode: ModeNever, Source: "a", CreatedBy: "x"})
	b, _ := s.Upsert(ctx, Override{Mode: ModeNever, Source: "b", CreatedBy: "x"})
	b2 := s.List()
	if len(b2) != 2 || b2[0].ID == "" || (b2[0].ID != b.ID && !b2[0].CreatedAt.Equal(a.CreatedAt)) {
		t.Fatalf("list = %+v", b2)
	}
}
