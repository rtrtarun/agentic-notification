package insights_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/inbox"
	"github.com/example/notification-service/pkg/insights"
)

var now = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

func item(id string, age time.Duration, level inbox.Level, outcome inbox.Outcome) inbox.Item {
	return inbox.Item{
		ID: id, Source: "github", EventType: "push", Repository: "acme/api",
		Priority: level, Outcome: outcome, CreatedAt: now.Add(-age), UpdatedAt: now.Add(-age),
	}
}

func TestParseRange(t *testing.T) {
	if r, err := insights.ParseRange(""); err != nil || r != insights.Range7d {
		t.Fatalf("empty = %q, %v", r, err)
	}
	for _, ok := range []string{"24h", "7d", "30d"} {
		if _, err := insights.ParseRange(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	if _, err := insights.ParseRange("1y"); !errors.Is(err, insights.ErrInvalidRange) {
		t.Fatalf("1y error = %v", err)
	}
}

func TestAnalyzeCountsAndBuckets(t *testing.T) {
	items := []inbox.Item{
		item("a", 30*time.Minute, inbox.LevelCritical, inbox.OutcomeDelivered),
		item("b", 90*time.Minute, inbox.LevelNormal, inbox.OutcomeHeld),
		item("c", 90*time.Minute, inbox.LevelLow, inbox.OutcomeMerged),
		item("d", 5*time.Hour, inbox.LevelHigh, inbox.OutcomeDelivered),
		item("old", 48*time.Hour, inbox.LevelNormal, inbox.OutcomeHeld), // outside 24h
		item("e", 10*time.Hour, inbox.LevelNormal, inbox.OutcomeSuppressed),
	}
	got := insights.Analyze(items, now, insights.Range24h)

	if got.Totals.Events != 5 || got.Totals.Critical != 1 || got.Totals.High != 1 || got.Totals.Normal != 2 || got.Totals.Low != 1 {
		t.Fatalf("totals = %+v", got.Totals)
	}
	if got.Totals.Delivered != 2 || got.Totals.Held != 1 || got.Totals.Merged != 1 || got.Totals.Suppressed != 1 {
		t.Fatalf("outcomes = %+v", got.Totals)
	}
	if got.Attention.Interruptions != 2 || got.Attention.HeldBack != 3 || got.Attention.HeldBackPercent != 60 {
		t.Fatalf("attention = %+v", got.Attention)
	}
	if len(got.Series) != 24 || got.BucketSeconds != 3600 {
		t.Fatalf("series = %d buckets of %ds", len(got.Series), got.BucketSeconds)
	}
	sum := 0
	for _, b := range got.Series {
		sum += b.Critical + b.High + b.Normal + b.Low
	}
	if sum != got.Totals.Events {
		t.Fatalf("series sums to %d, want %d", sum, got.Totals.Events)
	}
	// "a" arrived 30 minutes ago, so it belongs to the last bucket.
	if last := got.Series[23]; last.Critical != 1 {
		t.Fatalf("last bucket = %+v", last)
	}
}

func TestAnalyzeEmpty(t *testing.T) {
	got := insights.Analyze(nil, now, insights.Range7d)
	if got.Totals.Events != 0 || got.Attention.HeldBackPercent != 0 || got.Attention.MedianReadSeconds != nil || len(got.Series) != 7 {
		t.Fatalf("empty = %+v", got)
	}
	if got.TopSources == nil || got.TopRepositories == nil || got.TopEvents == nil {
		t.Fatal("top lists must be empty arrays, not null, so clients can iterate them")
	}
}

func TestAnalyzeMedianReadAndOpenCritical(t *testing.T) {
	read := func(id string, after time.Duration) inbox.Item {
		it := item(id, time.Hour, inbox.LevelNormal, inbox.OutcomeHeld)
		at := it.CreatedAt.Add(after)
		it.Read, it.ReadAt = true, &at
		return it
	}
	ackTime := now
	acked := item("ack", time.Hour, inbox.LevelCritical, inbox.OutcomeDelivered)
	acked.AcknowledgedAt = &ackTime
	items := []inbox.Item{
		read("r1", 10*time.Second), read("r2", 60*time.Second), read("r3", 20*time.Second),
		item("open", time.Hour, inbox.LevelCritical, inbox.OutcomeDelivered), acked,
	}
	got := insights.Analyze(items, now, insights.Range24h)
	if got.Attention.MedianReadSeconds == nil || *got.Attention.MedianReadSeconds != 20 {
		t.Fatalf("median = %v", got.Attention.MedianReadSeconds)
	}
	if got.Attention.OpenCritical != 1 || got.Attention.Acknowledged != 1 || got.Attention.ReadCount != 3 {
		t.Fatalf("attention = %+v", got.Attention)
	}
	if got.Totals.Unread != 2 {
		t.Fatalf("unread = %d", got.Totals.Unread)
	}
}

func TestAnalyzeTopListsAreOrderedAndCapped(t *testing.T) {
	var items []inbox.Item
	for i, repo := range []string{"r1", "r2", "r3", "r4", "r5", "r6", "r7"} {
		for n := 0; n <= i%3; n++ {
			it := item(repo+string(rune('a'+n)), time.Hour, inbox.LevelNormal, inbox.OutcomeHeld)
			it.Repository = repo
			items = append(items, it)
		}
	}
	got := insights.Analyze(items, now, insights.Range24h)
	if len(got.TopRepositories) != 5 {
		t.Fatalf("top = %d, want 5", len(got.TopRepositories))
	}
	for i := 1; i < len(got.TopRepositories); i++ {
		a, b := got.TopRepositories[i-1], got.TopRepositories[i]
		if a.Count < b.Count || (a.Count == b.Count && a.Name > b.Name) {
			t.Fatalf("not ordered: %+v", got.TopRepositories)
		}
	}
}

func TestBuildActivityMergesAndOrders(t *testing.T) {
	items := []inbox.Item{
		item("a", 3*time.Hour, inbox.LevelCritical, inbox.OutcomeDelivered),
		item("b", 1*time.Hour, inbox.LevelNormal, inbox.OutcomeHeld),
	}
	actions := []inbox.TrailRow{
		{EventID: "a", TrailEntry: inbox.TrailEntry{Step: "acknowledged", At: now.Add(-2 * time.Hour)}},
		{EventID: "gone", TrailEntry: inbox.TrailEntry{Step: "archived", At: now}}, // deleted event
	}
	got := insights.BuildActivity(items, actions, 10)
	if len(got) != 3 {
		t.Fatalf("entries = %d: %+v", len(got), got)
	}
	want := []struct{ id, kind string }{{"b", insights.KindReceived}, {"a", insights.KindAction}, {"a", insights.KindReceived}}
	for i, w := range want {
		if got[i].EventID != w.id || got[i].Kind != w.kind {
			t.Fatalf("entry %d = %s/%s, want %s/%s", i, got[i].EventID, got[i].Kind, w.id, w.kind)
		}
	}
	if limited := insights.BuildActivity(items, actions, 2); len(limited) != 2 {
		t.Fatalf("limit ignored: %d", len(limited))
	}
}

func TestParseLimit(t *testing.T) {
	if n, err := insights.ParseLimit(0); err != nil || n != 50 {
		t.Fatalf("default = %d, %v", n, err)
	}
	for _, bad := range []int{-1, 201} {
		if _, err := insights.ParseLimit(bad); err == nil {
			t.Errorf("%d accepted", bad)
		}
	}
}

type fakeSource struct {
	items []inbox.Item
	rows  []inbox.TrailRow
	err   error
}

func (f fakeSource) Items(context.Context) ([]inbox.Item, error) { return f.items, f.err }
func (f fakeSource) RecentActions(context.Context, int) ([]inbox.TrailRow, error) {
	return f.rows, f.err
}

func TestServiceUsesSourceAndPropagatesErrors(t *testing.T) {
	svc := insights.NewService(fakeSource{items: []inbox.Item{item("a", time.Hour, inbox.LevelHigh, inbox.OutcomeDelivered)}}).
		WithClock(func() time.Time { return now })
	a, err := svc.Analytics(context.Background(), insights.Range24h)
	if err != nil || a.Totals.Events != 1 {
		t.Fatalf("analytics = %+v, %v", a, err)
	}
	if _, err := svc.Analytics(context.Background(), "bogus"); !errors.Is(err, insights.ErrInvalidRange) {
		t.Fatalf("bogus range error = %v", err)
	}
	boom := errors.New("boom")
	bad := insights.NewService(fakeSource{err: boom})
	if _, err := bad.Analytics(context.Background(), insights.Range24h); !errors.Is(err, boom) {
		t.Fatalf("analytics error = %v", err)
	}
	if _, err := bad.Activity(context.Background(), 5); !errors.Is(err, boom) {
		t.Fatalf("activity error = %v", err)
	}
}
