package dedup

import (
	"context"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
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

func agentStep(id, typ, parent string, attempt any) event.Event {
	return event.Event{
		ID: id, Source: "agent", EventType: typ, TraceID: "run-1", ParentEventID: parent,
		Payload: map[string]any{"metadata": map[string]any{"step_kind": "retry", "attempt": attempt}},
	}
}

func TestRetriesInOneAgentRunAreDistinctSteps(t *testing.T) {
	d := NewMemoryDeduper(0)
	for i, e := range []event.Event{
		agentStep("a2", "agent.retry", "a1", float64(2)),
		agentStep("a3", "agent.retry", "a2", float64(3)),
		agentStep("a4", "agent.retry", "a3", float64(4)),
	} {
		res, err := d.Check(context.Background(), e)
		if err != nil || res.Duplicate {
			t.Fatalf("step %d: a retry with a new attempt is a new fact, not a repeat: %+v %v", i, res, err)
		}
	}
}

func TestTheSameAgentStepSentTwiceStillMerges(t *testing.T) {
	d := NewMemoryDeduper(0)
	first := agentStep("a2", "agent.retry", "a1", float64(2))
	again := agentStep("a2-again", "agent.retry", "a1", float64(2))
	if res, _ := d.Check(context.Background(), first); res.Duplicate {
		t.Fatal("the first is never a duplicate")
	}
	res, _ := d.Check(context.Background(), again)
	if !res.Duplicate || res.OriginalID != "a2" {
		t.Fatalf("same run, type, parent, kind and attempt is a repeat: %+v", res)
	}
}

func TestAnAgentRunIsOneThreadAndDifferentRunsAreSeparate(t *testing.T) {
	a := agentStep("x", "agent.plan", "", nil)
	b := agentStep("y", "tool.call", "x", nil)
	other := agentStep("z", "agent.plan", "", nil)
	other.TraceID = "run-2"
	if ThreadID(a) != ThreadID(b) || ThreadID(a) == ThreadID(other) {
		t.Fatalf("threads: %s %s %s", ThreadID(a), ThreadID(b), ThreadID(other))
	}
	if DedupKey(a) == DedupKey(other) {
		t.Fatal("two runs must never share a dedup key")
	}
}
