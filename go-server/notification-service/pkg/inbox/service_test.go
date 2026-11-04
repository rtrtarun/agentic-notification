package inbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/event"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func ev(id, class, status string, minutes int) event.Event {
	return event.Event{
		ID: id, Source: "github", EventType: "pull_request", Repository: "acme/web",
		AttentionClass: class, CreatedAt: t0.Add(time.Duration(minutes) * time.Minute),
		DecisionState: &event.DecisionState{Status: status, Reason: "because " + id},
	}
}

// newService seeds events in order; created times follow the minutes argument.
func newService(t *testing.T, events ...event.Event) *Service {
	t.Helper()
	repo := event.NewInMemoryRepository()
	for _, e := range events {
		if err := repo.Save(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	clock := t0.Add(time.Hour)
	return NewService(repo, NewMemoryRepository()).WithClock(func() time.Time { return clock })
}

func ids(p Page) []string {
	out := make([]string, len(p.Items))
	for i, it := range p.Items {
		out[i] = it.ID
	}
	return out
}

func TestLevelMapping(t *testing.T) {
	cases := []struct {
		name   string
		e      event.Event
		s      State
		expect Level
	}{
		{"critical", ev("a", event.AttentionCritical, event.DecisionSent, 0), State{}, LevelCritical},
		{"critical ignores override", ev("a", event.AttentionCritical, event.DecisionSent, 0), State{PriorityOverride: LevelLow}, LevelCritical},
		{"standard sent is high", ev("a", event.AttentionStandard, event.DecisionSent, 0), State{}, LevelHigh},
		{"standard held is normal", ev("a", event.AttentionStandard, event.DecisionQueued, 0), State{}, LevelNormal},
		{"background is low", ev("a", event.AttentionBackground, event.DecisionDigest, 0), State{}, LevelLow},
		{"override applies below critical", ev("a", event.AttentionStandard, event.DecisionQueued, 0), State{PriorityOverride: LevelHigh}, LevelHigh},
	}
	for _, c := range cases {
		if got := LevelOf(c.e, c.s); got != c.expect {
			t.Errorf("%s: level = %s, want %s", c.name, got, c.expect)
		}
	}
}

func TestOutcomeMapping(t *testing.T) {
	merged := ev("m", event.AttentionBackground, event.DecisionMerged, 0)
	merged.MergedInto = "x"
	cases := map[string]event.Event{
		"delivered":  ev("a", event.AttentionStandard, event.DecisionSent, 0),
		"held":       ev("a", event.AttentionStandard, event.DecisionDelayed, 0),
		"suppressed": ev("a", event.AttentionStandard, event.DecisionBudgetExceeded, 0),
		"merged":     merged,
		"recorded":   {ID: "a"},
	}
	for want, e := range cases {
		if got := OutcomeOf(e); string(got) != want {
			t.Errorf("outcome = %s, want %s", got, want)
		}
	}
}

func TestListSortsNewestFirstAndHidesMergedRepeats(t *testing.T) {
	repeat := ev("c", event.AttentionBackground, event.DecisionMerged, 3)
	repeat.MergedInto = "a"
	s := newService(t,
		ev("a", event.AttentionStandard, event.DecisionSent, 1),
		ev("b", event.AttentionCritical, event.DecisionSent, 2),
		repeat,
	)
	page, err := s.List(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(ids(page)); got != "[b a]" {
		t.Fatalf("items = %s, want [b a]", got)
	}
	if page.Items[1].RepeatCount != 1 {
		t.Fatalf("repeat count = %d, want 1", page.Items[1].RepeatCount)
	}
	merged, _ := s.List(context.Background(), Query{Outcomes: []Outcome{OutcomeMerged}})
	if got := fmt.Sprint(ids(merged)); got != "[c]" {
		t.Fatalf("merged filter = %s, want [c]", got)
	}
}

func TestListSortOrders(t *testing.T) {
	s := newService(t,
		ev("low", event.AttentionBackground, event.DecisionDigest, 3),
		ev("crit", event.AttentionCritical, event.DecisionSent, 1),
		ev("norm", event.AttentionStandard, event.DecisionQueued, 2),
	)
	want := map[string]string{
		SortNewest:   "[low norm crit]",
		SortOldest:   "[crit norm low]",
		SortPriority: "[crit norm low]",
		SortLowest:   "[low norm crit]",
	}
	for order, expect := range want {
		page, _ := s.List(context.Background(), Query{Sort: order})
		if got := fmt.Sprint(ids(page)); got != expect {
			t.Errorf("sort %s = %s, want %s", order, got, expect)
		}
	}
}

func TestListFiltersCombine(t *testing.T) {
	other := ev("o", event.AttentionStandard, event.DecisionSent, 4)
	other.Repository = "acme/api"
	other.Source = "gitlab"
	s := newService(t,
		ev("a", event.AttentionStandard, event.DecisionSent, 1),
		ev("b", event.AttentionBackground, event.DecisionQueued, 2),
		other,
	)
	ctx := context.Background()
	read := false

	page, _ := s.List(ctx, Query{Priorities: []Level{LevelHigh}, Repository: "acme/web"})
	if got := fmt.Sprint(ids(page)); got != "[a]" {
		t.Errorf("priority+repo = %s", got)
	}
	page, _ = s.List(ctx, Query{Source: "gitlab"})
	if got := fmt.Sprint(ids(page)); got != "[o]" {
		t.Errorf("source = %s", got)
	}
	page, _ = s.List(ctx, Query{Text: "pull request GITLAB"})
	if got := fmt.Sprint(ids(page)); got != "[o]" {
		t.Errorf("text = %s", got)
	}
	if page, _ = s.List(ctx, Query{Text: "nonsense"}); page.Total != 0 {
		t.Errorf("unmatched text should return nothing, got %d", page.Total)
	}
	page, _ = s.List(ctx, Query{Outcomes: []Outcome{OutcomeHeld}, Read: &read})
	if got := fmt.Sprint(ids(page)); got != "[b]" {
		t.Errorf("outcome+unread = %s", got)
	}
	from := t0.Add(2 * time.Minute)
	page, _ = s.List(ctx, Query{From: &from})
	if got := fmt.Sprint(ids(page)); got != "[o b]" {
		t.Errorf("from = %s", got)
	}
}

func TestPaging(t *testing.T) {
	var all []event.Event
	for i := 0; i < 7; i++ {
		all = append(all, ev(fmt.Sprintf("e%d", i), event.AttentionStandard, event.DecisionQueued, i))
	}
	s := newService(t, all...)
	page, _ := s.List(context.Background(), Query{PageSize: 3, Page: 3})
	if page.Total != 7 || page.Page != 3 || len(page.Items) != 1 {
		t.Fatalf("page = %+v", page)
	}
	page, _ = s.List(context.Background(), Query{PageSize: 3, Page: 99})
	if page.Page != 3 {
		t.Fatalf("out-of-range page clamps to the last, got %d", page.Page)
	}
	page, _ = s.List(context.Background(), Query{PageSize: 1000})
	if page.PageSize != MaxPageSize {
		t.Fatalf("page size = %d, want cap %d", page.PageSize, MaxPageSize)
	}
}

func TestReadAndUnreadPersist(t *testing.T) {
	s := newService(t, ev("a", event.AttentionStandard, event.DecisionSent, 1))
	ctx := context.Background()
	if err := s.Apply(ctx, "a", Action{Type: ActionRead}); err != nil {
		t.Fatal(err)
	}
	page, _ := s.List(ctx, Query{})
	if !page.Items[0].Read || page.Counts.Unread != 0 {
		t.Fatalf("expected read, got %+v counts %+v", page.Items[0], page.Counts)
	}
	_ = s.Apply(ctx, "a", Action{Type: ActionUnread})
	page, _ = s.List(ctx, Query{})
	if page.Items[0].Read || page.Counts.Unread != 1 {
		t.Fatal("expected unread again")
	}
}

func TestArchiveMovesBetweenViewsAndDeleteHides(t *testing.T) {
	s := newService(t,
		ev("a", event.AttentionStandard, event.DecisionSent, 1),
		ev("b", event.AttentionStandard, event.DecisionSent, 2),
	)
	ctx := context.Background()
	_ = s.Apply(ctx, "a", Action{Type: ActionArchive})
	_ = s.Apply(ctx, "b", Action{Type: ActionDelete})

	inbox, _ := s.List(ctx, Query{})
	archived, _ := s.List(ctx, Query{Archived: true})
	if len(inbox.Items) != 0 || len(archived.Items) != 1 || archived.Items[0].ID != "a" {
		t.Fatalf("inbox=%v archived=%v", ids(inbox), ids(archived))
	}
	if inbox.Counts.Archived != 1 || inbox.Counts.All != 0 {
		t.Fatalf("counts = %+v", inbox.Counts)
	}
	if err := s.Apply(ctx, "b", Action{Type: ActionRead}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted event err = %v, want not found", err)
	}
}

func TestAcknowledgeAndResolveMarkReadAndRecordTrail(t *testing.T) {
	s := newService(t, ev("a", event.AttentionCritical, event.DecisionSent, 1))
	ctx := context.Background()
	_ = s.Apply(ctx, "a", Action{Type: ActionAcknowledge})
	_ = s.Apply(ctx, "a", Action{Type: ActionResolve})

	d, err := s.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Item.Read || d.Item.AcknowledgedAt == nil || d.Item.ResolvedAt == nil {
		t.Fatalf("item = %+v", d.Item)
	}
	var recorded, reconstructed []string
	for _, e := range d.Trail {
		if e.Reconstructed {
			reconstructed = append(reconstructed, e.Step)
		} else {
			recorded = append(recorded, e.Step)
		}
	}
	if fmt.Sprint(recorded) != "[acknowledge resolve]" {
		t.Fatalf("recorded trail = %v", recorded)
	}
	if fmt.Sprint(reconstructed) != "[received classified rules dedup outcome]" {
		t.Fatalf("reconstructed trail = %v", reconstructed)
	}
}

func TestSnoozeBoundsAndUnreadCount(t *testing.T) {
	s := newService(t, ev("a", event.AttentionStandard, event.DecisionSent, 1))
	ctx := context.Background()
	now := t0.Add(time.Hour)

	for _, until := range []time.Time{now.Add(10 * time.Second), now.Add(31 * 24 * time.Hour), now.Add(-time.Hour)} {
		if err := s.Apply(ctx, "a", Action{Type: ActionSnooze, Until: until}); !errors.Is(err, ErrInvalid) {
			t.Errorf("snooze until %v err = %v, want invalid", until, err)
		}
	}
	if err := s.Apply(ctx, "a", Action{Type: ActionSnooze, Until: now.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	page, _ := s.List(ctx, Query{})
	if page.Items[0].SnoozedUntil == nil || page.Counts.Unread != 0 || page.Counts.All != 1 {
		t.Fatalf("snoozed item still listed but not unread: %+v %+v", page.Items[0], page.Counts)
	}
}

func TestCriticalCannotBeReprioritised(t *testing.T) {
	s := newService(t,
		ev("crit", event.AttentionCritical, event.DecisionSent, 1),
		ev("std", event.AttentionStandard, event.DecisionQueued, 2),
	)
	ctx := context.Background()
	if err := s.Apply(ctx, "crit", Action{Type: ActionPriority, Priority: LevelLow}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("lowering critical err = %v, want invalid", err)
	}
	if err := s.Apply(ctx, "std", Action{Type: ActionPriority, Priority: LevelCritical}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("raising to critical err = %v, want invalid", err)
	}
	if err := s.Apply(ctx, "std", Action{Type: ActionPriority, Priority: LevelHigh}); err != nil {
		t.Fatal(err)
	}
	page, _ := s.List(ctx, Query{Priorities: []Level{LevelHigh}})
	if fmt.Sprint(ids(page)) != "[std]" || page.Counts.Critical != 1 {
		t.Fatalf("items %v counts %+v", ids(page), page.Counts)
	}
	_ = s.Apply(ctx, "std", Action{Type: ActionPriority}) // clear
	page, _ = s.List(ctx, Query{Priorities: []Level{LevelNormal}})
	if fmt.Sprint(ids(page)) != "[std]" {
		t.Fatalf("override should clear, got %v", ids(page))
	}
}

func TestUnknownActionAndMissingEvent(t *testing.T) {
	s := newService(t, ev("a", event.AttentionStandard, event.DecisionSent, 1))
	ctx := context.Background()
	if err := s.Apply(ctx, "a", Action{Type: "explode"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	if err := s.Apply(ctx, "nope", Action{Type: ActionRead}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get err = %v", err)
	}
}

func TestBulkReportsEachIDAndKeepsGoing(t *testing.T) {
	s := newService(t,
		ev("crit", event.AttentionCritical, event.DecisionSent, 1),
		ev("std", event.AttentionStandard, event.DecisionQueued, 2),
	)
	res, err := s.Bulk(context.Background(), []string{"crit", "ghost", "std"}, Action{Type: ActionPriority, Priority: LevelLow})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || res[1].OK || !res[2].OK {
		t.Fatalf("results = %+v", res)
	}
	if _, err := s.Bulk(context.Background(), nil, Action{Type: ActionRead}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty bulk err = %v", err)
	}
	tooMany := make([]string, MaxBulk()+1)
	if _, err := s.Bulk(context.Background(), tooMany, Action{Type: ActionRead}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized bulk err = %v", err)
	}
}

func TestMarkAllReadSkipsArchivedAndCountsChanges(t *testing.T) {
	s := newService(t,
		ev("a", event.AttentionStandard, event.DecisionSent, 1),
		ev("b", event.AttentionStandard, event.DecisionSent, 2),
		ev("c", event.AttentionStandard, event.DecisionSent, 3),
	)
	ctx := context.Background()
	_ = s.Apply(ctx, "a", Action{Type: ActionRead})
	_ = s.Apply(ctx, "b", Action{Type: ActionArchive})
	n, err := s.MarkAllRead(ctx)
	if err != nil || n != 1 {
		t.Fatalf("changed = %d err = %v, want 1", n, err)
	}
	page, _ := s.List(ctx, Query{Archived: true})
	if page.Items[0].Read {
		t.Fatal("archived item must stay unread")
	}
}

func TestRelatedUsesThreadKeyAndRepository(t *testing.T) {
	a := ev("a", event.AttentionStandard, event.DecisionSent, 1)
	b := ev("b", event.AttentionStandard, event.DecisionSent, 2)
	b.Repository = "acme/other"
	b.ThreadID = "t1"
	a.ThreadID = "t1"
	c := ev("c", event.AttentionStandard, event.DecisionSent, 3)
	c.Repository = "elsewhere"
	s := newService(t, a, b, c)
	d, _ := s.Get(context.Background(), "a")
	if len(d.Related) != 1 || d.Related[0].ID != "b" {
		t.Fatalf("related = %v", d.Related)
	}
}

func TestCountsTabs(t *testing.T) {
	s := newService(t,
		ev("c", event.AttentionCritical, event.DecisionSent, 1),
		ev("h", event.AttentionStandard, event.DecisionSent, 2),
		ev("n", event.AttentionStandard, event.DecisionQueued, 3),
		ev("l", event.AttentionBackground, event.DecisionBudgetExceeded, 4),
	)
	page, _ := s.List(context.Background(), Query{})
	want := Counts{All: 4, Unread: 4, Critical: 1, High: 1, Normal: 1, Low: 1, Held: 1, Delivered: 2, Suppressed: 1}
	if page.Counts != want {
		t.Fatalf("counts = %+v, want %+v", page.Counts, want)
	}
}
