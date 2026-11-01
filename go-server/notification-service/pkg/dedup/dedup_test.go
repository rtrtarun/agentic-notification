package dedup

import (
	"testing"
	"time"

	"github.com/example/notification-service/pkg/event"
)

func prEvent(id string, number float64, action string) event.Event {
	return event.Event{
		ID: id, Source: "github", EventType: "pull_request", Repository: "octo/repo",
		Payload: map[string]interface{}{"number": number, "action": action},
	}
}

func TestDuplicateWithinWindow(t *testing.T) {
	d := NewMemoryDeduper(time.Minute)
	first := d.Check(prEvent("a", 1, "opened"))
	second := d.Check(prEvent("b", 1, "opened"))
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
	d.Check(prEvent("a", 1, "opened"))
	d.now = func() time.Time { return now.Add(2 * time.Minute) }
	if d.Check(prEvent("b", 1, "opened")).Duplicate {
		t.Fatal("event outside window must not be a duplicate")
	}
}

func TestDifferentActionsShareThreadButNotKey(t *testing.T) {
	d := NewMemoryDeduper(time.Minute)
	a := d.Check(prEvent("a", 7, "opened"))
	b := d.Check(prEvent("b", 7, "closed"))
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
