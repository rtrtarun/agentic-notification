package attention

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
)

var tuneNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func tuned(version int, adj ...Adjustment) *Tuning {
	t := NewTuning(time.Minute).WithClock(func() time.Time { return tuneNow })
	t.Set(version, adj)
	return t
}

func star() event.Event {
	return event.Event{ID: "e1", Source: "github", EventType: "star", Repository: "acme/web", AttentionClass: event.AttentionStandard, Payload: map[string]any{}}
}

func pullRequest() event.Event {
	return event.Event{ID: "e2", Source: "github", EventType: "pull_request", Repository: "acme/api", AttentionClass: event.AttentionStandard,
		Payload: map[string]any{"action": "opened"}}
}

func TestWithoutAdjustmentsTheHeuristicScoresExactlyAsBefore(t *testing.T) {
	events := []event.Event{star(), pullRequest(), {ID: "x", Source: "github", EventType: "push", AttentionClass: event.AttentionStandard, Payload: map[string]any{}}}
	plain := NewHeuristic()
	for name, h := range map[string]*Heuristic{
		"nil snapshot":   NewHeuristic().WithTuning(nil),
		"empty snapshot": NewHeuristic().WithTuning(tuned(7)),
		"unrelated one":  NewHeuristic().WithTuning(tuned(7, Adjustment{Source: "gitlab", EventType: "star", Delta: -20, Summary: "x"})),
	} {
		for _, e := range events {
			want, _ := plain.Classify(context.Background(), e)
			got, err := h.Classify(context.Background(), e)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: %+v differs from %+v (%v)", name, got, want, err)
			}
		}
	}
}

func TestAnApprovedAdjustmentChangesTheScoreTheReasonAndNamesTheVersion(t *testing.T) {
	h := NewHeuristic().WithTuning(tuned(3, Adjustment{Source: "github", EventType: "pull_request", Delta: -20, Summary: "9 of 11 signals from 3 people were negative"}))
	got, err := h.Classify(context.Background(), pullRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got.Score != 40 || got.Class != event.AttentionStandard {
		t.Fatalf("score = %d class = %s, want 40 standard (60 - 20)", got.Score, got.Class)
	}
	if got.Model != "heuristic-v1+tuned-3" || SourceOf(got.Model) != "heuristic" {
		t.Fatalf("model = %q source = %q", got.Model, SourceOf(got.Model))
	}
	if want := "scored 40: something new was opened, learned adjustment -20: 9 of 11 signals from 3 people were negative"; got.Reason() != want {
		t.Fatalf("reason = %q, want %q", got.Reason(), want)
	}
}

func TestANegativeAdjustmentCanMoveStandardToBackgroundAndAPositiveOneProtects(t *testing.T) {
	down := NewHeuristic().WithTuning(tuned(1, Adjustment{Source: "github", EventType: "star", Delta: -20, Summary: "s"}))
	got, _ := down.Classify(context.Background(), star())
	if got.Class != event.AttentionBackground {
		t.Fatalf("a quieted star should be background: %+v", got)
	}
	// A star scores 15 on its own (background); +20 makes 35, which is standard: protected from demotion.
	up := NewHeuristic().WithTuning(tuned(1, Adjustment{Source: "github", EventType: "star", Delta: 20, Summary: "s"}))
	got, _ = up.Classify(context.Background(), star())
	if got.Score != 35 || got.Class != event.AttentionStandard {
		t.Fatalf("a protected star should stay standard at 35: %+v", got)
	}
}

func TestACriticalEventIsNeverClassifiedNorAdjusted(t *testing.T) {
	h := NewHeuristic().WithTuning(tuned(1, Adjustment{Source: "github", EventType: "security_advisory", Delta: -20, Summary: "s"}))
	crit := event.Event{ID: "c", Source: "github", EventType: "security_advisory", AttentionClass: event.AttentionCritical, Payload: map[string]any{}}
	if _, err := h.Classify(context.Background(), crit); !errors.Is(err, ErrCritical) {
		t.Fatalf("critical must be refused, got %v", err)
	}
	if _, err := Guarded(h).Classify(context.Background(), crit); !errors.Is(err, ErrCritical) {
		t.Fatalf("guarded critical must be refused, got %v", err)
	}
}

func TestAnAdjustmentForARepositoryBeatsOneForTheWholeType(t *testing.T) {
	h := NewHeuristic().WithTuning(tuned(2,
		Adjustment{Source: "github", EventType: "pull_request", Delta: -5, Summary: "type"},
		Adjustment{Source: "github", EventType: "pull_request", Repository: "ACME/api", Delta: 7, Summary: "repo"}))
	got, _ := h.Classify(context.Background(), pullRequest())
	if got.Score != 67 {
		t.Fatalf("the repository adjustment (+7) should win: %+v", got)
	}
	other := pullRequest()
	other.Repository = "acme/other"
	got, _ = h.Classify(context.Background(), other)
	if got.Score != 55 {
		t.Fatalf("other repositories fall back to the type adjustment (-5): %+v", got)
	}
}

func TestTheScoreStaysBetweenZeroAndAHundredAndOffsetsAreClamped(t *testing.T) {
	h := NewHeuristic().WithTuning(tuned(1, Adjustment{Source: "github", EventType: "pull_request", Delta: 500, Summary: "huge"}))
	got, _ := h.Classify(context.Background(), pullRequest())
	if got.Score != 100 {
		t.Fatalf("score = %d, want it capped at 100", got.Score)
	}
	if !strings.Contains(got.Reason(), "learned adjustment +50:") {
		t.Fatalf("an offset beyond %d must be clamped: %s", MaxTuningDelta, got.Reason())
	}
	h = NewHeuristic().WithTuning(tuned(1, Adjustment{Source: "github", EventType: "star", Delta: -500, Summary: "huge"}))
	if got, _ := h.Classify(context.Background(), star()); got.Score != 0 {
		t.Fatalf("score = %d, want it floored at 0", got.Score)
	}
}

func TestASnapshotThatIsNeverLoadedOrHasGoneStaleIsNotUsed(t *testing.T) {
	clock := tuneNow
	snap := NewTuning(time.Minute).WithClock(func() time.Time { return clock })
	h := NewHeuristic().WithTuning(snap)
	if got, _ := h.Classify(context.Background(), pullRequest()); got.Score != 60 || snap.Version() != 0 || snap.Len() != 0 {
		t.Fatalf("a snapshot that was never loaded must change nothing: %+v", got)
	}
	snap.Set(4, []Adjustment{{Source: "github", EventType: "pull_request", Delta: -10, Summary: "s"}})
	if got, _ := h.Classify(context.Background(), pullRequest()); got.Score != 50 || snap.Version() != 4 || snap.Len() != 1 {
		t.Fatalf("a fresh snapshot is used: %+v", got)
	}
	clock = clock.Add(2 * time.Minute)
	got, _ := h.Classify(context.Background(), pullRequest())
	if got.Score != 60 || got.Model != ModelHeuristic || snap.Version() != 0 || snap.Len() != 0 {
		t.Fatalf("a stale snapshot must be ignored and scores return to normal: %+v", got)
	}
	snap.Set(5, nil)
	if got, _ := h.Classify(context.Background(), pullRequest()); got.Score != 60 {
		t.Fatalf("an empty reload is fresh and changes nothing: %+v", got)
	}
}

func TestANilSnapshotIsSafe(t *testing.T) {
	var snap *Tuning
	snap.Set(1, []Adjustment{{Source: "a", EventType: "b", Delta: 1}})
	if snap.Version() != 0 || snap.Len() != 0 {
		t.Fatal("a nil snapshot answers nothing")
	}
	if _, _, ok := snap.lookup(star()); ok {
		t.Fatal("a nil snapshot has no adjustments")
	}
}

func TestAZeroOffsetIsNotKept(t *testing.T) {
	snap := tuned(1, Adjustment{Source: "github", EventType: "star", Delta: 0, Summary: "s"})
	if snap.Len() != 0 {
		t.Fatalf("len = %d", snap.Len())
	}
}
