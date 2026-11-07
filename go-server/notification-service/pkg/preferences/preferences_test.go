package preferences_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/preferences"
)

func at(h, m int) time.Time { return time.Date(2026, 6, 10, h, m, 0, 0, time.UTC) }

func quiet(start, end string) preferences.QuietHours {
	return preferences.QuietHours{Enabled: true, Start: start, End: end, Timezone: "UTC"}
}

func TestWindowCrossingMidnight(t *testing.T) {
	q := quiet("22:00", "07:00")
	cases := []struct {
		now    time.Time
		inside bool
		until  time.Time
	}{
		{at(23, 0), true, time.Date(2026, 6, 11, 7, 0, 0, 0, time.UTC)},
		{at(22, 0), true, time.Date(2026, 6, 11, 7, 0, 0, 0, time.UTC)},
		{at(3, 0), true, at(7, 0)},
		{at(6, 59), true, at(7, 0)},
		{at(7, 0), false, time.Time{}},
		{at(12, 0), false, time.Time{}},
		{at(21, 59), false, time.Time{}},
	}
	for _, c := range cases {
		inside, until := q.Window(c.now)
		if inside != c.inside || !until.Equal(c.until) {
			t.Errorf("%s: inside=%v until=%v, want %v %v", c.now.Format("15:04"), inside, until, c.inside, c.until)
		}
	}
}

func TestWindowSameDayAndDisabled(t *testing.T) {
	q := quiet("13:00", "15:00")
	if in, _ := q.Window(at(14, 0)); !in {
		t.Fatal("14:00 should be inside 13:00-15:00")
	}
	if in, _ := q.Window(at(15, 0)); in {
		t.Fatal("15:00 is the end and must be outside")
	}
	q.Enabled = false
	if in, _ := q.Window(at(14, 0)); in {
		t.Fatal("disabled quiet hours must never apply")
	}
}

func TestWindowUsesTheConfiguredZone(t *testing.T) {
	q := preferences.QuietHours{Enabled: true, Start: "22:00", End: "07:00", Timezone: "Asia/Kolkata"} // UTC+5:30
	// 17:00 UTC is 22:30 in India: inside. 12:00 UTC is 17:30: outside.
	if in, _ := q.Window(at(17, 0)); !in {
		t.Fatal("17:00 UTC should be inside for Asia/Kolkata")
	}
	if in, _ := q.Window(at(12, 0)); in {
		t.Fatal("12:00 UTC should be outside for Asia/Kolkata")
	}
}

func TestNormaliseValidation(t *testing.T) {
	base := preferences.Default()
	bad := map[string]func(p *preferences.Preferences){
		"zone":    func(p *preferences.Preferences) { p.QuietHours.Timezone = "Mars/Base" },
		"start":   func(p *preferences.Preferences) { p.QuietHours.Start = "25:00" },
		"end":     func(p *preferences.Preferences) { p.QuietHours.End = "soon" },
		"equal":   func(p *preferences.Preferences) { p.QuietHours = quiet("08:00", "08:00") },
		"pattern": func(p *preferences.Preferences) { p.MutedRepositories = []string{"acme/[x"} },
		"too many": func(p *preferences.Preferences) {
			p.MutedSources = make([]string, 51)
			for i := range p.MutedSources {
				p.MutedSources[i] = string(rune('a'+i%26)) + strings.Repeat("x", i)
			}
		},
		"long pattern": func(p *preferences.Preferences) { p.MutedSources = []string{strings.Repeat("x", 101)} },
	}
	for name, mutate := range bad {
		p := base
		mutate(&p)
		if _, err := preferences.Normalise(p); !errors.Is(err, preferences.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// Equal times are fine while quiet hours are off, so a person can keep their draft.
	p := base
	p.QuietHours.Start, p.QuietHours.End = "08:00", "08:00"
	if _, err := preferences.Normalise(p); err != nil {
		t.Fatalf("disabled equal times: %v", err)
	}
}

func TestNormaliseTidiesLists(t *testing.T) {
	p := preferences.Default()
	p.MutedSources = []string{" GitHub ", "github", "", "GitLab"}
	p.MutedRepositories = []string{" acme/* ", "acme/*", ""}
	p.QuietHours.Timezone = " "
	got, err := preferences.Normalise(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MutedSources) != 2 || got.MutedSources[0] != "github" || got.MutedSources[1] != "gitlab" {
		t.Fatalf("sources = %v", got.MutedSources)
	}
	if len(got.MutedRepositories) != 1 || got.MutedRepositories[0] != "acme/*" || got.QuietHours.Timezone != "UTC" {
		t.Fatalf("got = %+v", got)
	}
}

func evt(class, source, repo string) event.Event {
	return event.Event{Source: source, Repository: repo, AttentionClass: class}
}

func TestCriticalIsNeverAdjusted(t *testing.T) {
	p := preferences.Default()
	p.QuietHours = quiet("00:00", "23:59")
	p.MutedSources = []string{"github"}
	p.MutedRepositories = []string{"*/*"}
	if adj, ok := preferences.Decide(p, evt(event.AttentionCritical, "github", "acme/api"), at(12, 0)); ok {
		t.Fatalf("critical event was adjusted: %+v", adj)
	}
}

func TestMutedSourceAndRepositoryBecomeBackgroundDigest(t *testing.T) {
	p := preferences.Default()
	p.MutedSources = []string{"gitlab"}
	p.MutedRepositories = []string{"acme/*"}
	for name, e := range map[string]event.Event{
		"source": evt(event.AttentionStandard, "GitLab", "x/y"),
		"repo":   evt(event.AttentionStandard, "github", "acme/api"),
	} {
		adj, ok := preferences.Decide(p, e, at(12, 0))
		if !ok || adj.AttentionClass != event.AttentionBackground || adj.Status != event.DecisionDigest {
			t.Errorf("%s: %+v %v", name, adj, ok)
		}
	}
	if _, ok := preferences.Decide(p, evt(event.AttentionStandard, "github", "other/api"), at(12, 0)); ok {
		t.Fatal("unmuted event adjusted")
	}
	if _, ok := preferences.Decide(p, evt(event.AttentionStandard, "github", ""), at(12, 0)); ok {
		t.Fatal("an empty repository must not match a repository pattern")
	}
}

func TestQuietHoursDelayStandardOnly(t *testing.T) {
	p := preferences.Default()
	p.QuietHours = quiet("22:00", "07:00")
	adj, ok := preferences.Decide(p, evt(event.AttentionStandard, "github", "a/b"), at(23, 30))
	if !ok || adj.Status != event.DecisionDelayed || !strings.Contains(adj.Reason, "07:00 UTC") {
		t.Fatalf("standard at night = %+v %v", adj, ok)
	}
	if _, ok := preferences.Decide(p, evt(event.AttentionBackground, "github", "a/b"), at(23, 30)); ok {
		t.Fatal("background is already quiet; nothing to change")
	}
	if _, ok := preferences.Decide(p, evt(event.AttentionStandard, "github", "a/b"), at(12, 0)); ok {
		t.Fatal("daytime event delayed")
	}
}

func TestServiceStoresAndActivates(t *testing.T) {
	ctx := context.Background()
	store := preferences.NewMemoryStore()
	svc, err := preferences.NewService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	now := at(12, 0)
	svc.WithClock(func() time.Time { return now })

	if got := svc.Get(); got.QuietHours.Enabled || len(got.MutedSources) != 0 || got.MutedSources == nil {
		t.Fatalf("defaults = %+v", got)
	}
	in := preferences.Default()
	in.MutedSources = []string{"Gitlab"}
	saved, err := svc.Update(ctx, in)
	if err != nil || !saved.UpdatedAt.Equal(now) || saved.MutedSources[0] != "gitlab" {
		t.Fatalf("saved = %+v %v", saved, err)
	}
	if _, ok := svc.Decide(evt(event.AttentionStandard, "gitlab", "")); !ok {
		t.Fatal("update did not take effect")
	}
	if _, err := svc.Update(ctx, preferences.Preferences{QuietHours: preferences.QuietHours{Start: "bad"}}); !errors.Is(err, preferences.ErrInvalid) {
		t.Fatalf("invalid update = %v", err)
	}
	if svc.Get().MutedSources[0] != "gitlab" {
		t.Fatal("a rejected update must leave the preferences unchanged")
	}

	again, err := preferences.NewService(ctx, store)
	if err != nil || again.Get().MutedSources[0] != "gitlab" {
		t.Fatalf("not persisted: %+v %v", again.Get(), err)
	}
}
