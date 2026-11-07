package attention

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
)

type fakeQueue struct {
	mu         sync.Mutex
	claimed    []event.Event
	delayed    []event.Event
	claimErr   error
	delayedErr error
	updateErr  error
	updates    []string
	claims     int
	lastLease  time.Duration
	lastBatch  int
}

func (q *fakeQueue) ClaimPending(_ context.Context, _ time.Time, lease time.Duration, limit int) ([]event.Event, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.claims++
	q.lastLease, q.lastBatch = lease, limit
	if q.claimErr != nil {
		return nil, q.claimErr
	}
	out := q.claimed
	q.claimed = nil
	return out, nil
}

func (q *fakeQueue) ListDelayed(context.Context, int) ([]event.Event, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.delayed, q.delayedErr
}

func (q *fakeQueue) UpdateDecision(_ context.Context, id, class string, s event.DecisionState) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.updates = append(q.updates, id+":"+class+":"+s.Status)
	return q.updateErr
}

type fakeProc struct {
	processErr  error
	releaseErr  error
	release     bool
	processed   []string
	releasedIDs []string
}

func (p *fakeProc) ProcessPending(_ context.Context, e event.Event) (event.Event, error) {
	p.processed = append(p.processed, e.ID)
	if p.processErr != nil {
		return e, p.processErr
	}
	e.DecisionState = &event.DecisionState{Status: event.DecisionQueued, Reason: "decided"}
	return e, nil
}

func (p *fakeProc) ReleaseDelayed(_ context.Context, e event.Event) (event.Event, bool, error) {
	p.releasedIDs = append(p.releasedIDs, e.ID)
	if p.releaseErr != nil {
		return e, false, p.releaseErr
	}
	e.DecisionState = &event.DecisionState{Status: event.DecisionQueued, Reason: "released"}
	return e, p.release, nil
}

func queuedEvent(id, class string) event.Event {
	return event.Event{ID: id, AttentionClass: class, DecisionState: &event.DecisionState{Status: event.DecisionQueued}}
}

func workerWith(q *fakeQueue, p *fakeProc) (*Worker, *bytes.Buffer) {
	var logs bytes.Buffer
	return NewWorker(q, p, slog.New(slog.NewTextHandler(&logs, nil))), &logs
}

func TestWorkerDecidesClaimedEventsAndStoresEachDecision(t *testing.T) {
	q := &fakeQueue{claimed: []event.Event{queuedEvent("a", event.AttentionStandard), queuedEvent("b", event.AttentionBackground)}}
	p := &fakeProc{}
	w, _ := workerWith(q, p)
	n, err := w.RunOnce(context.Background())
	if err != nil || n != 2 || strings.Join(p.processed, ",") != "a,b" {
		t.Fatalf("n=%d err=%v processed=%v", n, err, p.processed)
	}
	if strings.Join(q.updates, ",") != "a:standard:queued,b:background:queued" {
		t.Fatalf("updates = %v", q.updates)
	}
	if q.lastLease != DefaultWorkerLease || q.lastBatch != DefaultWorkerBatch {
		t.Fatalf("lease %v batch %d", q.lastLease, q.lastBatch)
	}
}

func TestWorkerIgnoresACriticalEventOnEveryPath(t *testing.T) {
	q := &fakeQueue{
		claimed: []event.Event{queuedEvent("crit1", event.AttentionCritical)},
		delayed: []event.Event{queuedEvent("crit2", event.AttentionCritical)},
	}
	p := &fakeProc{release: true}
	w, logs := workerWith(q, p)
	n, err := w.RunOnce(context.Background())
	if err != nil || n != 0 || len(p.processed) != 0 || len(p.releasedIDs) != 0 || len(q.updates) != 0 {
		t.Fatalf("a critical event was touched: n=%d err=%v processed=%v released=%v updates=%v", n, err, p.processed, p.releasedIDs, q.updates)
	}
	if strings.Count(logs.String(), "critical event reached") != 2 {
		t.Fatalf("each leak must be logged loudly: %s", logs.String())
	}
}

func TestWorkerKeepsGoingAfterOneEventFailsAndReportsIt(t *testing.T) {
	q := &fakeQueue{claimed: []event.Event{queuedEvent("a", event.AttentionStandard), queuedEvent("b", event.AttentionStandard)}}
	p := &fakeProc{processErr: errors.New("boom")}
	w, _ := workerWith(q, p)
	n, err := w.RunOnce(context.Background())
	if err == nil || n != 0 || len(p.processed) != 2 || len(q.updates) != 0 {
		t.Fatalf("n=%d err=%v processed=%v updates=%v", n, err, p.processed, q.updates)
	}
}

func TestWorkerReportsAFailureToStoreADecision(t *testing.T) {
	q := &fakeQueue{claimed: []event.Event{queuedEvent("a", event.AttentionStandard)}, updateErr: errors.New("db down")}
	w, _ := workerWith(q, &fakeProc{})
	if n, err := w.RunOnce(context.Background()); err == nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestWorkerReturnsQueueErrors(t *testing.T) {
	w, _ := workerWith(&fakeQueue{claimErr: errors.New("claim failed")}, &fakeProc{})
	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("a claim failure must be returned")
	}
	q := &fakeQueue{delayedErr: errors.New("list failed"), claimed: []event.Event{queuedEvent("a", event.AttentionStandard)}}
	w, _ = workerWith(q, &fakeProc{})
	n, err := w.RunOnce(context.Background())
	if err == nil || n != 1 {
		t.Fatalf("the claimed event is still decided, and the list failure is reported: n=%d err=%v", n, err)
	}
}

func TestWorkerReleasesOnlyDelayedEventsTheProcessorReleases(t *testing.T) {
	delayed := queuedEvent("d", event.AttentionStandard)
	delayed.DecisionState.Status = event.DecisionDelayed
	q := &fakeQueue{delayed: []event.Event{delayed}}
	p := &fakeProc{release: false}
	w, _ := workerWith(q, p)
	if n, _ := w.RunOnce(context.Background()); n != 0 || len(q.updates) != 0 {
		t.Fatalf("still quiet: n=%d updates=%v", n, q.updates)
	}
	p.release = true
	if n, err := w.RunOnce(context.Background()); err != nil || n != 1 || strings.Join(q.updates, ",") != "d:standard:queued" {
		t.Fatalf("n=%d err=%v updates=%v", n, err, q.updates)
	}
	p.releaseErr = errors.New("nope")
	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("a release failure must be returned")
	}
	p.releaseErr = nil
	q.updateErr = errors.New("db down")
	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("a failure to store a release must be returned")
	}
}

func TestWorkerRunPollsUntilCancelled(t *testing.T) {
	q := &fakeQueue{}
	w, _ := workerWith(q, &fakeProc{})
	w.WithInterval(5 * time.Millisecond).WithInterval(0).WithClock(time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	for {
		q.mu.Lock()
		n := q.claims
		q.mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the worker did not poll")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run must return when its context is cancelled")
	}
}

func TestNewWorkerUsesDefaultLoggerAndInterval(t *testing.T) {
	w := NewWorker(&fakeQueue{}, &fakeProc{}, nil)
	if w.logger == nil || w.interval != DefaultWorkerInterval {
		t.Fatalf("defaults wrong: %v %v", w.logger, w.interval)
	}
}

func TestDigestIncludesBudgetHeldEventsAndTheExplanationSaysSo(t *testing.T) {
	over := held("over", "pull_request", t0.Add(time.Hour), event.DecisionBudgetExceeded, nil)
	over.DecisionState.Reason = "default; attention budget used (10 of 10 interruptions this hour), held for the next digest"
	g, store := newGen([]event.Event{over}, nil)
	d, made, err := g.Generate(ctx, "schedule")
	if err != nil || !made || d.Count != 1 {
		t.Fatalf("a budget-held event must reach the digest: %+v %v %v", d, made, err)
	}
	if got, _ := store.List(ctx, 1); len(got) != 1 {
		t.Fatalf("stored = %+v", got)
	}
	text, err := NewHeuristic().Explain(ctx, over)
	if err != nil || !strings.Contains(text, "attention budget") || !strings.Contains(text, "not dropped") {
		t.Fatalf("explanation = %q %v", text, err)
	}
}
