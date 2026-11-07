package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/budget"
	"github.com/agentpulse/notification-service/pkg/delivery"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/overrides"
	"github.com/agentpulse/notification-service/pkg/policy"
	"github.com/agentpulse/notification-service/pkg/preferences"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// spyBudget counts calls and can refuse or fail.
type spyBudget struct {
	mu      sync.Mutex
	calls   int
	scopes  []string
	refuse  bool
	failure error
}

func (s *spyBudget) Take(_ context.Context, scope string) (budget.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.scopes = append(s.scopes, scope)
	if s.failure != nil {
		return budget.Result{}, s.failure
	}
	if s.refuse {
		return budget.Result{Allowed: false, Used: 10, Limit: 10}, nil
	}
	return budget.Result{Allowed: true, Used: 1, Limit: 10}, nil
}

func (s *spyBudget) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func routedPipeline(routes map[string][]string) (*Pipeline, *event.InMemoryRepository, *delivery.MemoryStore, *recordingProvider) {
	p, repo, provider := newTestPipeline(outboxRules)
	store := delivery.NewMemoryStore()
	out := delivery.NewOutbox(store, delivery.Targets{Slack: []string{"https://hooks.slack.com/x"}}, routes)
	return p.WithOutbox(out).WithLogger(quiet), repo, store, provider
}

var standardToSlack = map[string][]string{"standard": {delivery.ChannelSlack}}

func stored(t *testing.T, repo *event.InMemoryRepository, id string) event.Event {
	t.Helper()
	all, err := repo.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("event %s not stored", id)
	return event.Event{}
}

// ---- the budget ----

func TestStandardEventsPastTheBudgetAreHeldForTheDigestNotDropped(t *testing.T) {
	p, repo, store, _ := routedPipeline(standardToSlack)
	p.WithBudget(budget.NewMemoryBudget(2, time.Hour))
	for _, id := range []string{"e1", "e2", "e3"} {
		if _, err := p.Handle(context.Background(), prEvt(id)); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"e1", "e2"} {
		if st := stored(t, repo, id).DecisionState.Status; st != event.DecisionQueued {
			t.Fatalf("%s within the budget should be queued, got %s", id, st)
		}
	}
	third := stored(t, repo, "e3")
	if third.DecisionState.Status != event.DecisionBudgetExceeded || third.AttentionClass != event.AttentionStandard {
		t.Fatalf("e3 = %+v", third.DecisionState)
	}
	if !strings.Contains(third.DecisionState.Reason, "attention budget used (2 of 2") || !strings.Contains(third.DecisionState.Reason, "next digest") {
		t.Fatalf("the reason must say what happened: %q", third.DecisionState.Reason)
	}
	if n := len(jobs(t, store)); n != 2 {
		t.Fatalf("only the two within budget may be delivered, got %d jobs", n)
	}
	if got := stored(t, repo, "e3"); got.ID == "" {
		t.Fatal("a held event must still be stored, never dropped")
	}
}

func TestACriticalEventNeverTouchesTheBudget(t *testing.T) {
	p, repo, store, provider := routedPipeline(standardToSlack)
	spy := &spyBudget{refuse: true}
	p.WithBudget(spy)
	res, err := p.Handle(context.Background(), event.Event{ID: "sec-1", Source: "github", EventType: "security_advisory", Repository: "octo/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if spy.callCount() != 0 {
		t.Fatalf("the budget was consulted %d times for a critical event", spy.callCount())
	}
	if res.AttentionClass != event.AttentionCritical || res.DecisionState.Status != event.DecisionSent || len(provider.sent) != 1 {
		t.Fatalf("a critical event must be delivered at once: %+v sent=%d", res.DecisionState, len(provider.sent))
	}
	if stored(t, repo, "sec-1").DecisionState.Status != event.DecisionSent {
		t.Fatal("the stored decision must be sent")
	}
	if n := len(jobs(t, store)); n != 1 { // the direct-delivery log entry
		t.Fatalf("jobs = %d", n)
	}
}

func TestBackgroundEventsDoNotSpendTheBudget(t *testing.T) {
	p, _, _, _ := routedPipeline(map[string][]string{"background": {delivery.ChannelSlack}, "standard": {delivery.ChannelSlack}})
	spy := &spyBudget{}
	p.WithBudget(spy)
	p.policyEngine.SetRules(adaptiveBackgroundRules())
	if _, err := p.Handle(context.Background(), prEvt("b1")); err != nil {
		t.Fatal(err)
	}
	if spy.callCount() != 0 {
		t.Fatalf("a background event is not an interruption, but the budget was asked %d times", spy.callCount())
	}
}

func TestNothingIsCountedWhenStandardEventsAreNotDelivered(t *testing.T) {
	p, repo, _, _ := routedPipeline(nil) // no route: nothing would interrupt anyone
	spy := &spyBudget{refuse: true}
	p.WithBudget(spy)
	p.Handle(context.Background(), prEvt("e1"))
	if spy.callCount() != 0 || stored(t, repo, "e1").DecisionState.Status != event.DecisionQueued {
		t.Fatalf("calls %d status %s", spy.callCount(), stored(t, repo, "e1").DecisionState.Status)
	}
}

type alwaysOverride struct{}

func (alwaysOverride) Decide(event.Event) (overrides.Decision, bool) {
	return overrides.Decision{Mode: overrides.ModeAlways, OverrideID: "o1", Reason: "you asked to always be told"}, true
}

func TestAnAlwaysNotifyChoiceOutranksTheBudget(t *testing.T) {
	p, repo, store, _ := routedPipeline(standardToSlack)
	spy := &spyBudget{refuse: true}
	p.WithBudget(spy).WithOverrides(alwaysOverride{})
	p.Handle(context.Background(), prEvt("e1"))
	if spy.callCount() != 0 || stored(t, repo, "e1").DecisionState.Status != event.DecisionQueued || len(jobs(t, store)) != 1 {
		t.Fatalf("an explicit choice must go out: calls %d, jobs %d", spy.callCount(), len(jobs(t, store)))
	}
}

func TestABudgetThatFailsLetsTheEventThrough(t *testing.T) {
	p, repo, store, _ := routedPipeline(standardToSlack)
	p.WithBudget(&spyBudget{failure: errors.New("redis down")})
	p.Handle(context.Background(), prEvt("e1"))
	if stored(t, repo, "e1").DecisionState.Status != event.DecisionQueued || len(jobs(t, store)) != 1 {
		t.Fatal("an unavailable budget must never suppress a notification")
	}
}

func TestTheBudgetIsCountedForTheWorkspace(t *testing.T) {
	p, _, _, _ := routedPipeline(standardToSlack)
	spy := &spyBudget{}
	p.WithBudget(spy)
	p.Handle(context.Background(), prEvt("e1"))
	if len(spy.scopes) != 1 || spy.scopes[0] != budget.ScopeWorkspace {
		t.Fatalf("scopes = %v", spy.scopes)
	}
}

// ---- the asynchronous worker ----

func asyncSetup(t *testing.T, routes map[string][]string) (*Pipeline, *event.InMemoryRepository, *delivery.MemoryStore, *attention.Worker) {
	t.Helper()
	p, repo, store, _ := routedPipeline(routes)
	p.WithAsyncAttention()
	return p, repo, store, attention.NewWorker(repo, p, quiet)
}

func TestAsyncHandleSavesTheEventPendingAndDeliversNothingYet(t *testing.T) {
	p, repo, store, _ := asyncSetup(t, standardToSlack)
	res, err := p.Handle(context.Background(), prEvt("e1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.DecisionState.Status != event.DecisionQueued || !strings.Contains(res.DecisionState.Reason, "waiting for the attention worker") {
		t.Fatalf("decision = %+v", res.DecisionState)
	}
	if len(jobs(t, store)) != 0 {
		t.Fatal("nothing may be delivered before the worker decides")
	}
	if got, _ := repo.ClaimPending(context.Background(), time.Now(), time.Minute, 5); len(got) != 1 || got[0].ID != "e1" {
		t.Fatalf("the event should be waiting in the queue: %+v", got)
	}
}

func TestTheWorkerDecidesQueuesDeliveryAndStoresTheDecision(t *testing.T) {
	p, repo, store, w := asyncSetup(t, standardToSlack)
	p.Handle(context.Background(), prEvt("e1"))
	n, err := w.RunOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}
	got := stored(t, repo, "e1")
	if got.DecisionState.Status != event.DecisionQueued || strings.Contains(got.DecisionState.Reason, "waiting") {
		t.Fatalf("decision = %+v", got.DecisionState)
	}
	if len(jobs(t, store)) != 1 {
		t.Fatalf("delivery should be queued by the worker, jobs = %d", len(jobs(t, store)))
	}
	if n, _ := w.RunOnce(context.Background()); n != 0 || len(jobs(t, store)) != 1 {
		t.Fatalf("a decided event must not be processed twice: n=%d jobs=%d", n, len(jobs(t, store)))
	}
}

func TestTheWorkerAppliesTheBudgetAndAttention(t *testing.T) {
	p, repo, store, w := asyncSetup(t, standardToSlack)
	p.WithBudget(budget.NewMemoryBudget(1, time.Hour))
	p.Handle(context.Background(), prEvt("e1"))
	p.Handle(context.Background(), prEvt("e2"))
	if n, err := w.RunOnce(context.Background()); err != nil || n != 2 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}
	if stored(t, repo, "e1").DecisionState.Status != event.DecisionQueued || stored(t, repo, "e2").DecisionState.Status != event.DecisionBudgetExceeded {
		t.Fatalf("budget not applied: %s %s", stored(t, repo, "e1").DecisionState.Status, stored(t, repo, "e2").DecisionState.Status)
	}
	if len(jobs(t, store)) != 1 {
		t.Fatalf("jobs = %d", len(jobs(t, store)))
	}
}

func TestACriticalEventIsNeverDeferredToTheWorker(t *testing.T) {
	p, repo, _, provider := func() (*Pipeline, *event.InMemoryRepository, *delivery.MemoryStore, *recordingProvider) {
		p, repo, store, provider := routedPipeline(standardToSlack)
		p.WithAsyncAttention()
		return p, repo, store, provider
	}()
	res, err := p.Handle(context.Background(), event.Event{ID: "sec-1", Source: "github", EventType: "security_advisory"})
	if err != nil || len(provider.sent) != 1 || res.DecisionState.Status != event.DecisionSent {
		t.Fatalf("a critical event must be delivered inline even in async mode: %v %+v sent=%d", err, res.DecisionState, len(provider.sent))
	}
	if got, _ := repo.ClaimPending(context.Background(), time.Now(), time.Minute, 5); len(got) != 0 {
		t.Fatalf("a critical event must never be in the work queue: %+v", got)
	}
}

// panicProcessor fails the test if the worker hands it anything.
type panicProcessor struct{ t *testing.T }

func (p panicProcessor) ProcessPending(_ context.Context, e event.Event) (event.Event, error) {
	p.t.Errorf("the worker processed %s", e.ID)
	return e, nil
}

func (p panicProcessor) ReleaseDelayed(_ context.Context, e event.Event) (event.Event, bool, error) {
	p.t.Errorf("the worker released %s", e.ID)
	return e, false, nil
}

// leakyQueue returns a critical event from every call, as a broken store might.
type leakyQueue struct {
	updated int
}

func criticalEvent() event.Event {
	return event.Event{ID: "crit", Source: "github", EventType: "security_advisory", AttentionClass: event.AttentionCritical,
		DecisionState: &event.DecisionState{Status: event.DecisionQueued}}
}

func (q *leakyQueue) ClaimPending(context.Context, time.Time, time.Duration, int) ([]event.Event, error) {
	return []event.Event{criticalEvent()}, nil
}

func (q *leakyQueue) ListDelayed(context.Context, int) ([]event.Event, error) {
	e := criticalEvent()
	e.DecisionState.Status = event.DecisionDelayed
	return []event.Event{e}, nil
}

func (q *leakyQueue) UpdateDecision(context.Context, string, string, event.DecisionState) error {
	q.updated++
	return nil
}

func TestEvenIfAStoreLeaksACriticalEventTheWorkerNeverProcessesOrChangesIt(t *testing.T) {
	q := &leakyQueue{}
	w := attention.NewWorker(q, panicProcessor{t}, quiet)
	if n, err := w.RunOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}
	if q.updated != 0 {
		t.Fatalf("a critical event's decision was written %d times", q.updated)
	}
}

func adaptiveBackgroundRules() []policy.Rule {
	return []policy.Rule{{Name: "quiet", When: policy.RuleCondition{Source: "github"}, Then: policy.RuleOutcome{AttentionClass: "background", Reason: "quiet"}}}
}

func TestThePipelineRefusesToProcessOrReleaseACriticalEvent(t *testing.T) {
	p, _, _, _ := routedPipeline(standardToSlack)
	spy := &spyBudget{}
	p.WithBudget(spy)
	if _, err := p.ProcessPending(context.Background(), criticalEvent()); !errors.Is(err, attention.ErrCritical) {
		t.Fatalf("ProcessPending: %v", err)
	}
	delayed := criticalEvent()
	delayed.DecisionState.Status = event.DecisionDelayed
	if _, released, err := p.ReleaseDelayed(context.Background(), delayed); !errors.Is(err, attention.ErrCritical) || released {
		t.Fatalf("ReleaseDelayed: %v %v", released, err)
	}
	if spy.callCount() != 0 {
		t.Fatalf("the budget was consulted for a critical event %d times", spy.callCount())
	}
}

// failingOnce is a Processor whose first attempt fails.
type failingOnce struct {
	inner attention.Processor
	fails int
}

func (f *failingOnce) ProcessPending(ctx context.Context, e event.Event) (event.Event, error) {
	if f.fails > 0 {
		f.fails--
		return e, errors.New("transient")
	}
	return f.inner.ProcessPending(ctx, e)
}

func (f *failingOnce) ReleaseDelayed(ctx context.Context, e event.Event) (event.Event, bool, error) {
	return f.inner.ReleaseDelayed(ctx, e)
}

func TestAnEventTheWorkerFailedOnIsRetriedAfterItsLeaseExpires(t *testing.T) {
	p, repo, store, _ := routedPipeline(standardToSlack)
	p.WithAsyncAttention()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	w := attention.NewWorker(repo, &failingOnce{inner: p, fails: 1}, quiet).WithClock(func() time.Time { return now })
	p.Handle(context.Background(), prEvt("e1"))

	if n, err := w.RunOnce(context.Background()); err == nil || n != 0 {
		t.Fatalf("the first pass should fail: %d %v", n, err)
	}
	if n, _ := w.RunOnce(context.Background()); n != 0 {
		t.Fatal("the event is leased, so a second pass right away must leave it alone")
	}
	now = now.Add(attention.DefaultWorkerLease + time.Second)
	if n, err := w.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("after the lease expires it must be retried: %d %v", n, err)
	}
	if len(jobs(t, store)) != 1 || stored(t, repo, "e1").DecisionState.Status != event.DecisionQueued {
		t.Fatal("the retried event must be delivered exactly once")
	}
}

// ---- quiet hours ----

type quietPrefs struct {
	mu   sync.Mutex
	adj  preferences.Adjustment
	live bool
}

func (q *quietPrefs) set(live bool) {
	q.mu.Lock()
	q.live = live
	q.mu.Unlock()
}

func (q *quietPrefs) Decide(event.Event) (preferences.Adjustment, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.adj, q.live
}

func newQuiet() *quietPrefs {
	return &quietPrefs{live: true, adj: preferences.Adjustment{AttentionClass: event.AttentionStandard, Status: event.DecisionDelayed, Reason: "quiet hours until 08:00"}}
}

func TestDelayedEventsAreReleasedWhenQuietHoursEnd(t *testing.T) {
	p, repo, store, _ := routedPipeline(standardToSlack)
	prefs := newQuiet()
	p.WithPreferences(prefs)
	w := attention.NewWorker(repo, p, quiet)

	p.Handle(context.Background(), prEvt("e1"))
	if stored(t, repo, "e1").DecisionState.Status != event.DecisionDelayed || len(jobs(t, store)) != 0 {
		t.Fatal("an event in quiet hours must be delayed and not delivered")
	}
	if n, _ := w.RunOnce(context.Background()); n != 0 || len(jobs(t, store)) != 0 {
		t.Fatal("still quiet: the worker must leave it delayed")
	}

	prefs.set(false) // quiet hours are over
	if n, err := w.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("release = %d, %v", n, err)
	}
	got := stored(t, repo, "e1")
	if got.DecisionState.Status != event.DecisionQueued || !strings.Contains(got.DecisionState.Reason, "released after quiet hours") {
		t.Fatalf("decision = %+v", got.DecisionState)
	}
	if len(jobs(t, store)) != 1 {
		t.Fatalf("a released event must be delivered, jobs = %d", len(jobs(t, store)))
	}
	if n, _ := w.RunOnce(context.Background()); n != 0 || len(jobs(t, store)) != 1 {
		t.Fatal("releasing twice must not deliver twice")
	}
}

func TestAReleasedEventStillRespectsTheBudget(t *testing.T) {
	p, repo, store, _ := routedPipeline(standardToSlack)
	prefs := newQuiet()
	p.WithPreferences(prefs).WithBudget(&spyBudget{refuse: true})
	w := attention.NewWorker(repo, p, quiet)
	p.Handle(context.Background(), prEvt("e1"))
	prefs.set(false)
	w.RunOnce(context.Background())
	if stored(t, repo, "e1").DecisionState.Status != event.DecisionBudgetExceeded || len(jobs(t, store)) != 0 {
		t.Fatalf("a release counts as an interruption: %+v", stored(t, repo, "e1").DecisionState)
	}
}

func TestAMuteThatReplacesQuietHoursMovesTheEventToTheDigest(t *testing.T) {
	p, repo, store, _ := routedPipeline(standardToSlack)
	prefs := newQuiet()
	p.WithPreferences(prefs)
	w := attention.NewWorker(repo, p, quiet)
	p.Handle(context.Background(), prEvt("e1"))
	prefs.mu.Lock()
	prefs.adj = preferences.Adjustment{AttentionClass: event.AttentionBackground, Status: event.DecisionDigest, Reason: "github is muted"}
	prefs.mu.Unlock()
	w.RunOnce(context.Background())
	got := stored(t, repo, "e1")
	if got.DecisionState.Status != event.DecisionDigest || got.AttentionClass != event.AttentionBackground || len(jobs(t, store)) != 0 {
		t.Fatalf("got %+v class %s jobs %d", got.DecisionState, got.AttentionClass, len(jobs(t, store)))
	}
}

func TestACriticalEventIsNeverDelayedByQuietHours(t *testing.T) {
	p, repo, _, provider := routedPipeline(standardToSlack)
	p.WithPreferences(newQuiet())
	res, _ := p.Handle(context.Background(), event.Event{ID: "sec-1", Source: "github", EventType: "security_advisory"})
	if res.DecisionState.Status != event.DecisionSent || len(provider.sent) != 1 {
		t.Fatalf("quiet hours must not delay a critical event: %+v", res.DecisionState)
	}
	if got, _ := repo.ListDelayed(context.Background(), 10); len(got) != 0 {
		t.Fatalf("a critical event is never delayed: %+v", got)
	}
}
