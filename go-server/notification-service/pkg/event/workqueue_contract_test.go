package event

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

type queueRepo interface {
	Repository
	WorkQueue
}

var queueNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func queued(id, class string, pending bool) Event {
	return Event{
		ID: id, Source: "github", EventType: "pull_request", AttentionClass: class, AttentionPending: pending,
		DecisionState: &DecisionState{Status: DecisionQueued, Reason: "rule"}, Payload: map[string]any{},
	}
}

func idsOf(events []Event) string {
	ids := make([]string, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// workQueueContract is the behaviour every WorkQueue must have. It runs against
// the memory repository always and against PostgreSQL in the integration test.
func workQueueContract(t *testing.T, r queueRepo) {
	t.Helper()
	ctx := context.Background()

	delayed := queued("dl1", AttentionStandard, false)
	delayed.DecisionState.Status = DecisionDelayed
	delayedCritical := queued("dlc", AttentionCritical, false)
	delayedCritical.DecisionState.Status = DecisionDelayed
	// A critical event wrongly marked pending and queued: the queue must still never hand it out.
	rogue := queued("c1", AttentionCritical, true)
	for _, e := range []Event{
		queued("p1", AttentionStandard, true), queued("p2", AttentionBackground, true), rogue,
		queued("d1", AttentionStandard, false), delayed, delayedCritical,
	} {
		if err := r.Save(ctx, e); err != nil {
			t.Fatalf("save %s: %v", e.ID, err)
		}
	}

	t.Run("only pending standard and background events are claimed, never critical", func(t *testing.T) {
		got, err := r.ClaimPending(ctx, queueNow, time.Minute, 10)
		if err != nil || idsOf(got) != "p1,p2" {
			t.Fatalf("claimed %q, %v; want p1,p2", idsOf(got), err)
		}
		if got[0].DecisionState == nil || got[0].DecisionState.Status != DecisionQueued {
			t.Fatalf("a claimed event must carry its decision: %+v", got[0])
		}
	})

	t.Run("a leased event is not handed out twice, and an expired lease releases it", func(t *testing.T) {
		if again, _ := r.ClaimPending(ctx, queueNow.Add(30*time.Second), time.Minute, 10); len(again) != 0 {
			t.Fatalf("leased events were claimed again: %q", idsOf(again))
		}
		later, _ := r.ClaimPending(ctx, queueNow.Add(2*time.Minute), time.Minute, 10)
		if idsOf(later) != "p1,p2" {
			t.Fatalf("after the lease expired, claimed %q", idsOf(later))
		}
		if one, _ := r.ClaimPending(ctx, queueNow.Add(10*time.Minute), time.Minute, 1); len(one) != 1 {
			t.Fatalf("the limit must be honoured, got %d", len(one))
		}
	})

	t.Run("updating a decision stores it and takes the event out of the queue", func(t *testing.T) {
		if err := r.UpdateDecision(ctx, "p1", AttentionBackground, DecisionState{Status: DecisionDigest, Reason: "held", ModelVersion: "m", Confidence: 0.8}); err != nil {
			t.Fatal(err)
		}
		all, _ := r.List(ctx)
		var got Event
		for _, e := range all {
			if e.ID == "p1" {
				got = e
			}
		}
		if got.AttentionClass != AttentionBackground || got.DecisionState == nil || got.DecisionState.Status != DecisionDigest ||
			got.DecisionState.Reason != "held" || got.DecisionState.ModelVersion != "m" || got.DecisionState.Confidence != 0.8 {
			t.Fatalf("decision not stored: %+v", got)
		}
		rest, _ := r.ClaimPending(ctx, queueNow.Add(time.Hour), time.Minute, 10)
		if idsOf(rest) != "p2" {
			t.Fatalf("a decided event must leave the queue, claimed %q", idsOf(rest))
		}
		if err := r.UpdateDecision(ctx, "ghost", AttentionStandard, DecisionState{Status: DecisionQueued}); err != nil {
			t.Fatalf("an event removed by retention is not an error: %v", err)
		}
	})

	t.Run("a critical event's decision can never be changed", func(t *testing.T) {
		if err := r.UpdateDecision(ctx, "c1", AttentionStandard, DecisionState{Status: DecisionDigest, Reason: "demoted"}); !errors.Is(err, ErrCriticalImmutable) {
			t.Fatalf("changing a critical event: %v", err)
		}
		if err := r.UpdateDecision(ctx, "p2", AttentionCritical, DecisionState{Status: DecisionQueued}); !errors.Is(err, ErrCriticalImmutable) {
			t.Fatalf("escalating to critical: %v", err)
		}
		all, _ := r.List(ctx)
		for _, e := range all {
			switch e.ID {
			case "c1":
				if e.AttentionClass != AttentionCritical || e.DecisionState.Reason != "rule" {
					t.Fatalf("the critical event changed: %+v", e)
				}
			case "p2":
				if e.AttentionClass != AttentionBackground {
					t.Fatalf("p2 was escalated: %+v", e)
				}
			}
		}
	})

	t.Run("delayed events are listed, critical ones never", func(t *testing.T) {
		got, err := r.ListDelayed(ctx, 10)
		if err != nil || idsOf(got) != "dl1" {
			t.Fatalf("delayed = %q, %v", idsOf(got), err)
		}
		if none, _ := r.ListDelayed(ctx, 0); len(none) != 0 {
			t.Fatalf("limit 0 returned %q", idsOf(none))
		}
	})
}

func TestInMemoryWorkQueue(t *testing.T) { workQueueContract(t, NewInMemoryRepository()) }

func TestInMemoryClaimDoesNotShareDecisionState(t *testing.T) {
	r := NewInMemoryRepository()
	if err := r.Save(context.Background(), queued("p1", AttentionStandard, true)); err != nil {
		t.Fatal(err)
	}
	got, _ := r.ClaimPending(context.Background(), queueNow, time.Minute, 1)
	got[0].DecisionState.Reason = "tampered"
	all, _ := r.List(context.Background())
	if all[0].DecisionState.Reason != "rule" {
		t.Fatal("a claimed event must be a copy")
	}
}

// traceFieldsContract checks that the agent-trace fields survive storage.
func traceFieldsContract(t *testing.T, r Repository) {
	t.Helper()
	ctx := context.Background()
	step := Event{
		ID: "trace-step-2", Source: "agent", EventType: "tool.call.failed", AttentionClass: AttentionStandard,
		TraceID: "run-42", ParentEventID: "trace-step-1", AgentName: "researcher",
		Payload: map[string]any{"metadata": map[string]any{"attempt": float64(3), "tool_name": "search"}},
	}
	plain := Event{ID: "trace-none", Source: "github", EventType: "push", AttentionClass: AttentionStandard, Payload: map[string]any{}}
	for _, e := range []Event{step, plain} {
		if err := r.Save(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	all, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range all {
		switch e.ID {
		case "trace-step-2":
			found++
			md, _ := e.Payload["metadata"].(map[string]any)
			if e.TraceID != "run-42" || e.ParentEventID != "trace-step-1" || e.AgentName != "researcher" || md["tool_name"] != "search" {
				t.Fatalf("trace fields lost: %+v", e)
			}
		case "trace-none":
			found++
			if e.TraceID != "" || e.ParentEventID != "" || e.AgentName != "" {
				t.Fatalf("a webhook event must have no trace fields: %+v", e)
			}
		}
	}
	if found != 2 {
		t.Fatalf("found %d of 2 events", found)
	}
}

func TestInMemoryTraceFields(t *testing.T) { traceFieldsContract(t, NewInMemoryRepository()) }
