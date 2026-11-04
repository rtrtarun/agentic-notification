package dedup

import (
	"context"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/event"
)

// check runs Check with a background context and fails the test on error.
func check(d Deduper, evt event.Event) Result {
	res, err := d.Check(context.Background(), evt)
	if err != nil {
		panic(err)
	}
	return res
}

func prEvent(id string, number float64, action string) event.Event {
	return event.Event{
		ID: id, Source: "github", EventType: "pull_request", Repository: "octo/repo",
		Payload: map[string]interface{}{"number": number, "action": action},
	}
}

func TestDuplicateWithinWindow(t *testing.T) {
	d := NewMemoryDeduper(time.Minute)
	first := check(d, prEvent("a", 1, "opened"))
	second := check(d, prEvent("b", 1, "opened"))
	if first.Duplicate {
		t.Fatal("first event must not be a duplicate")
	}
	if !second.Duplicate || second.OriginalID != "a" {
		t.Fatalf("expected duplicate of a, got %+v", second)
	}
}

func TestDuplicateOutsideWindowIsNotMerged(t *testing.T) {
	d := NewMemoryDeduper(time.Minute)
	now := time.Now()
	d.now = func() time.Time { return now }
	check(d, prEvent("a", 1, "opened"))
	d.now = func() time.Time { return now.Add(2 * time.Minute) }
	if check(d, prEvent("b", 1, "opened")).Duplicate {
		t.Fatal("event outside window must not be a duplicate")
	}
}

func TestDifferentActionsShareThreadButNotKey(t *testing.T) {
	d := NewMemoryDeduper(time.Minute)
	a := check(d, prEvent("a", 7, "opened"))
	b := check(d, prEvent("b", 7, "closed"))
	if b.Duplicate {
		t.Fatal("different action must not be a duplicate")
	}
	if a.ThreadID != b.ThreadID {
		t.Fatalf("expected same thread, got %q and %q", a.ThreadID, b.ThreadID)
	}
}

func TestDifferentSubjectsAreDifferentThreads(t *testing.T) {
	if ThreadID(prEvent("a", 1, "opened")) == ThreadID(prEvent("b", 2, "opened")) {
		t.Fatal("different PR numbers must produce different threads")
	}
}

func TestSubjectFromNestedIssue(t *testing.T) {
	evt := event.Event{Source: "github", Repository: "r", Payload: map[string]interface{}{
		"issue": map[string]interface{}{"number": float64(5)},
	}}
	if got := ThreadID(evt); got != "github:r:5" {
		t.Fatalf("unexpected thread id %q", got)
	}
}
