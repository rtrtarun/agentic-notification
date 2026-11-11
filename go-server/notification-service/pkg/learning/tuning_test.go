package learning

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/feedback"
	"github.com/agentpulse/notification-service/pkg/policy"
)

type tuneRig struct {
	tuner  *Tuner
	fb     *fakeFeedback
	ev     *fakeEvents
	ru     *fakeRules
	adj    *MemoryAdjustments
	runs   *MemoryRuns
	tuning *attention.Tuning
	clock  time.Time
	n      int
}

func newTuneRig(cfg TunerConfig, rules ...policy.Rule) *tuneRig {
	r := &tuneRig{fb: &fakeFeedback{}, ev: &fakeEvents{}, ru: &fakeRules{list: rules}, adj: NewMemoryAdjustments(), runs: NewMemoryRuns(), clock: now}
	r.tuning = attention.NewTuning(time.Hour).WithClock(func() time.Time { return r.clock })
	r.tuner = NewTuner(r.adj, r.runs, r.fb, r.ev, r.ru, r.tuning, cfg).WithClock(func() time.Time { return r.clock })
	return r
}

func defaultRules() []policy.Rule {
	return []policy.Rule{
		{ID: "r-adv", Name: "github-security-advisory", Locked: true, When: policy.RuleCondition{Source: "github", EventType: "security_advisory"},
			Then: policy.RuleOutcome{AttentionClass: event.AttentionCritical, Reason: "x"}},
		{ID: "r-def", Name: "default-github", When: policy.RuleCondition{Source: "github"},
			Then: policy.RuleOutcome{AttentionClass: event.AttentionStandard, Reason: "x", Adaptive: true}},
	}
}

// signal adds one piece of feedback by a person about a new event of the kind, the age given ago.
func (r *tuneRig) signal(user, action, eventType, class, status string, age time.Duration) {
	r.n++
	id := fmt.Sprintf("evt-%d", r.n)
	r.ev.list = append(r.ev.list, event.Event{
		ID: id, Source: "github", EventType: eventType, Repository: "acme/api", AttentionClass: class,
		DecisionState: &event.DecisionState{Status: status, MatchedRule: "default-github"}, Payload: map[string]any{},
	})
	r.fb.list = append(r.fb.list, feedback.EventFeedback{
		ID: "fb-" + id, EventID: id, UserID: user, Action: action, Source: "github", EventType: eventType, Repository: "acme/api", CreatedAt: r.clock.Add(-age),
	})
}

func (r *tuneRig) signals(n int, users []string, action, eventType string) {
	for i := 0; i < n; i++ {
		r.signal(users[i%len(users)], action, eventType, event.AttentionStandard, event.DecisionQueued, time.Minute)
	}
}

func (r *tuneRig) run(t *testing.T) Run {
	t.Helper()
	run, err := r.tuner.RunNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunDone {
		t.Fatalf("run = %+v", run)
	}
	return run
}

func (r *tuneRig) list(t *testing.T, status string) []Adjustment {
	t.Helper()
	out, err := r.tuner.List(context.Background(), status)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var twoPeople = []string{"alice", "bob"}

func TestTheRuleIsAboundedSmoothedOffset(t *testing.T) {
	cases := []struct {
		p, n float64
		want int
	}{
		{0, 5, -14}, {5, 0, 14}, {0, 3, -12}, {3, 0, 12}, {4, 4, 0}, {2, 1, 4}, {0, 1000, -20}, {1000, 0, 20}, {0, 0, 0},
	}
	for _, c := range cases {
		if got := offset(c.p, c.n, 20); got != c.want {
			t.Errorf("offset(%v, %v) = %d, want %d", c.p, c.n, got, c.want)
		}
	}
	if got := offset(0, 100, 5); got != -5 {
		t.Errorf("a smaller maximum bounds the offset: %d", got)
	}
}

func TestSignalsLoseHalfTheirWeightEveryHalfLife(t *testing.T) {
	day := 24 * time.Hour
	if weight(0, 14*day) != 1 || weight(-time.Hour, 14*day) != 1 {
		t.Fatal("a fresh signal weighs 1, and a future-dated one is not heavier")
	}
	if weight(14*day, 14*day) != 0.5 || weight(28*day, 14*day) != 0.25 {
		t.Fatalf("half-life: %v %v", weight(14*day, 14*day), weight(28*day, 14*day))
	}
	if got := weight(time.Hour, 14*day); got != 0.997939 {
		t.Fatalf("weights are kept to six decimals so two implementations agree: %v", got)
	}
}

func TestEnoughSignalsFromEnoughPeopleProposeABoundedNegativeAdjustment(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(5, twoPeople, feedback.ActionDismissed, "pull_request")
	run := r.run(t)
	if run.FeedbackRead != 5 || run.EventsRead != 5 || run.ProposalsMade != 1 || run.FinishedAt == nil {
		t.Fatalf("run = %+v", run)
	}
	got := r.list(t, AdjustmentProposed)
	if len(got) != 1 {
		t.Fatalf("proposals = %+v", got)
	}
	a := got[0]
	if a.Source != "github" || a.EventType != "pull_request" || a.Repository != "" || a.Status != AdjustmentProposed || a.Version != 0 || a.ID != AdjustmentID("github", "pull_request", "") {
		t.Fatalf("adjustment = %+v", a)
	}
	// Five fresh dismissals, three by one person (capped at 3 weighted) and two by the other: N is a little under 5, so a little under -14.
	if a.Delta != -14 || a.Evidence.Negative != 5 || a.Evidence.Positive != 0 || a.Evidence.People != 2 || a.Evidence.Signals != 5 || a.Evidence.WindowDays != 30 {
		t.Fatalf("evidence = %+v", a)
	}
	if a.Evidence.Summary != "5 of 5 signals from 2 people were negative" {
		t.Fatalf("summary = %q", a.Evidence.Summary)
	}
}

func TestOpensAndRepliesProposeAPositiveAdjustmentThatProtects(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(3, twoPeople, feedback.ActionOpened, "push")
	r.signals(3, twoPeople, feedback.ActionReply, "push")
	r.run(t)
	got := r.list(t, "")
	if len(got) != 1 || got[0].Delta <= 0 || got[0].Evidence.Positive != 6 || !strings.HasSuffix(got[0].Evidence.Summary, "were positive") {
		t.Fatalf("got %+v", got)
	}
}

func TestOnePersonCannotSteerIt(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(40, []string{"alice"}, feedback.ActionDismissed, "pull_request")
	r.run(t)
	if got := r.list(t, ""); len(got) != 0 {
		t.Fatalf("one person alone must not produce a proposal: %+v", got)
	}
}

func TestTooFewSignalsProposeNothing(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(4, twoPeople, feedback.ActionDismissed, "pull_request")
	r.run(t)
	if got := r.list(t, ""); len(got) != 0 {
		t.Fatalf("four signals are below the default of five: %+v", got)
	}
}

func TestAPersonsInfluenceIsCappedSoANoisyPersonCountsAsMuchAsThreeSignals(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(20, []string{"alice"}, feedback.ActionDismissed, "pull_request")
	r.signals(1, []string{"bob"}, feedback.ActionDismissed, "pull_request")
	r.run(t)
	got := r.list(t, "")
	// N = 3 (alice, capped) + about 1 (bob) = about 4: round(20 * -4 / 6) = -13, not the -19 that 21 raw signals would give.
	if len(got) != 1 || got[0].Delta != -13 {
		t.Fatalf("got %+v", got)
	}
}

func TestOlderSignalsCountForLess(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	for i := 0; i < 5; i++ {
		r.signal(twoPeople[i%2], feedback.ActionDismissed, "pull_request", event.AttentionStandard, event.DecisionQueued, 28*24*time.Hour)
	}
	r.run(t)
	got := r.list(t, "")
	// Five signals, each weighing a quarter: N = 1.25, so round(20 * -1.25 / 3.25) = -8.
	if len(got) != 1 || got[0].Delta != -8 {
		t.Fatalf("got %+v", got)
	}
}

func TestOnlyDeliveredEventsAreLearnedFrom(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	for _, status := range []string{event.DecisionDigest, event.DecisionMerged, event.DecisionDelayed, event.DecisionBudgetExceeded} {
		for i := 0; i < 6; i++ {
			r.signal(twoPeople[i%2], feedback.ActionDismissed, "pull_request", event.AttentionStandard, status, time.Minute)
		}
	}
	r.run(t)
	if got := r.list(t, ""); len(got) != 0 {
		t.Fatalf("events held back or merged are not learned from, so a demoted event cannot look worse and worse: %+v", got)
	}
	r.signals(5, twoPeople, feedback.ActionDismissed, "pull_request")
	r.signal("alice", feedback.ActionDismissed, "pull_request", event.AttentionBackground, event.DecisionSent, time.Minute)
	r.run(t)
	if got := r.list(t, ""); len(got) != 1 || got[0].Evidence.Signals != 6 {
		t.Fatalf("queued and sent events are: %+v", got)
	}
}

func TestFeedbackAboutCriticalEventsIsNeverLearnedFrom(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	for i := 0; i < 30; i++ {
		r.signal(twoPeople[i%2], feedback.ActionDismissed, "pull_request", event.AttentionCritical, event.DecisionSent, time.Minute)
	}
	run := r.run(t)
	if got := r.list(t, ""); len(got) != 0 || run.ProposalsMade != 0 {
		t.Fatalf("critical feedback must teach nothing: %+v", got)
	}
}

func TestAScopeCoveredByALockedOrCriticalRuleIsNeverProposed(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	// A security_advisory that somehow is not marked critical on the event still falls in the locked rule's scope.
	r.signals(10, twoPeople, feedback.ActionDismissed, "security_advisory")
	r.run(t)
	if got := r.list(t, ""); len(got) != 0 {
		t.Fatalf("a locked critical rule's scope is never tuned: %+v", got)
	}
	// An event decided by a locked rule is dropped even when its type is not covered.
	rules := append(defaultRules(), policy.Rule{ID: "r-lock", Name: "locked-push", Locked: true, When: policy.RuleCondition{Source: "gitlab"},
		Then: policy.RuleOutcome{AttentionClass: event.AttentionStandard, Reason: "x"}})
	r2 := newTuneRig(TunerConfig{}, rules...)
	for i := 0; i < 8; i++ {
		r2.signals(1, twoPeople[i%2:i%2+1], feedback.ActionDismissed, "push")
		r2.ev.list[len(r2.ev.list)-1].DecisionState.MatchedRule = "locked-push"
	}
	r2.run(t)
	if got := r2.list(t, ""); len(got) != 0 {
		t.Fatalf("events decided by a locked rule are not learned from: %+v", got)
	}
}

func TestUndoAndUnknownEventsAreIgnored(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(10, twoPeople, feedback.ActionUndo, "pull_request")
	r.fb.list = append(r.fb.list, feedback.EventFeedback{ID: "orphan", EventID: "gone", UserID: "alice", Action: feedback.ActionDismissed, Source: "github", EventType: "pull_request", CreatedAt: now})
	r.run(t)
	if got := r.list(t, ""); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestRunningAgainRefreshesTheProposalInsteadOfAddingAnother(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(5, twoPeople, feedback.ActionDismissed, "pull_request")
	r.run(t)
	first := r.list(t, "")[0]
	r.clock = r.clock.Add(time.Hour)
	r.signals(4, twoPeople, feedback.ActionDismissed, "pull_request")
	r.run(t)
	got := r.list(t, "")
	if len(got) != 1 || got[0].ID != first.ID {
		t.Fatalf("a second run must update the proposal, not add one: %+v", got)
	}
	if !got[0].CreatedAt.Equal(first.CreatedAt) || !got[0].UpdatedAt.After(first.UpdatedAt) || got[0].Evidence.Signals != 9 || got[0].Delta >= first.Delta {
		t.Fatalf("created stays, updated moves, evidence grows: first %+v now %+v", first, got[0])
	}
}

func TestTheSameFeedbackAlwaysGivesTheSameProposals(t *testing.T) {
	build := func() []Adjustment {
		r := newTuneRig(TunerConfig{}, defaultRules()...)
		r.signals(7, twoPeople, feedback.ActionDismissed, "pull_request")
		r.signals(6, []string{"alice", "bob", "carol"}, feedback.ActionOpened, "push")
		r.run(t)
		return r.list(t, "")
	}
	a, b := build(), build()
	for i := range a {
		a[i].CreatedAt, a[i].UpdatedAt, b[i].CreatedAt, b[i].UpdatedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	}
	if len(a) != 2 || !reflect.DeepEqual(a, b) {
		t.Fatalf("not deterministic:\n%+v\n%+v", a, b)
	}
}

func TestNothingIsActiveUntilAnAdminApproves(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(9, twoPeople, feedback.ActionDismissed, "pull_request")
	r.run(t)
	r.run(t)
	if active := r.list(t, AdjustmentActive); len(active) != 0 || r.tuning.Len() != 0 || r.tuner.Version() != 0 {
		t.Fatalf("a run must never activate anything: %+v len=%d", active, r.tuning.Len())
	}
	if got, _ := attention.NewHeuristic().WithTuning(r.tuning).Classify(context.Background(), r.ev.list[0]); got.Model != attention.ModelHeuristic {
		t.Fatalf("scoring must be unchanged: %+v", got)
	}
}

func TestApprovingActivatesItAndTheHeuristicUsesItUntilItIsRetired(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(5, twoPeople, feedback.ActionDismissed, "pull_request")
	r.run(t)
	id := r.list(t, "")[0].ID
	h := attention.NewHeuristic().WithTuning(r.tuning)
	pr := event.Event{ID: "p", Source: "github", EventType: "pull_request", AttentionClass: event.AttentionStandard, Payload: map[string]any{"action": "opened"}}
	if got, _ := h.Classify(context.Background(), pr); got.Score != 60 {
		t.Fatalf("before approval: %+v", got)
	}

	a, err := r.tuner.Approve(context.Background(), id, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != AdjustmentActive || a.Version != 1 || a.ResolvedBy != "admin" || a.ResolvedAt == nil {
		t.Fatalf("approved = %+v", a)
	}
	got, _ := h.Classify(context.Background(), pr)
	if got.Score != 46 || got.Model != "heuristic-v1+tuned-1" || !strings.Contains(got.Reason(), "learned adjustment -14: 5 of 5 signals from 2 people were negative") {
		t.Fatalf("after approval: %+v / %s", got, got.Reason())
	}
	if _, err := r.tuner.Approve(context.Background(), id, "admin"); !errors.Is(err, ErrNotProposed) {
		t.Fatalf("approving twice: %v", err)
	}
	if _, err := r.tuner.Reject(context.Background(), id, "admin"); !errors.Is(err, ErrNotProposed) {
		t.Fatalf("rejecting an active one: %v", err)
	}

	r.clock = r.clock.Add(time.Minute)
	retired, err := r.tuner.Retire(context.Background(), id, "admin2")
	if err != nil {
		t.Fatal(err)
	}
	if retired.Status != AdjustmentRetired || retired.Version != 2 || retired.ResolvedBy != "admin2" {
		t.Fatalf("retired = %+v", retired)
	}
	if got, _ := h.Classify(context.Background(), pr); got.Score != 60 || got.Model != attention.ModelHeuristic {
		t.Fatalf("retiring must restore today's score: %+v", got)
	}
	if _, err := r.tuner.Retire(context.Background(), id, "admin"); !errors.Is(err, ErrNotActive) {
		t.Fatalf("retiring twice: %v", err)
	}
	if _, err := r.tuner.Approve(context.Background(), id, "admin"); !errors.Is(err, ErrNotProposed) {
		t.Fatalf("approving a retired one: %v", err)
	}
}

func TestRejectedAndActiveAdjustmentsAreNotProposedAgainAndRetiredOnesNeedFreshEvidence(t *testing.T) {
	ctx := context.Background()
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(5, twoPeople, feedback.ActionDismissed, "pull_request")
	r.signals(5, twoPeople, feedback.ActionDismissed, "push")
	r.signals(5, twoPeople, feedback.ActionDismissed, "issues")
	r.run(t)
	byType := map[string]string{}
	for _, a := range r.list(t, "") {
		byType[a.EventType] = a.ID
	}
	if _, err := r.tuner.Reject(ctx, byType["pull_request"], "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.tuner.Approve(ctx, byType["push"], "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.tuner.Approve(ctx, byType["issues"], "admin"); err != nil {
		t.Fatal(err)
	}
	r.clock = r.clock.Add(time.Hour)
	if _, err := r.tuner.Retire(ctx, byType["issues"], "admin"); err != nil {
		t.Fatal(err)
	}

	r.clock = r.clock.Add(time.Hour)
	run := r.run(t)
	if run.ProposalsMade != 0 {
		t.Fatalf("a rejected, active or freshly retired scope has no new evidence: %+v", run)
	}
	statuses := map[string]string{}
	for _, a := range r.list(t, "") {
		statuses[a.EventType] = a.Status
	}
	if statuses["pull_request"] != AdjustmentRejected || statuses["push"] != AdjustmentActive || statuses["issues"] != AdjustmentRetired {
		t.Fatalf("statuses = %+v", statuses)
	}

	// New evidence after the retirement brings a retired scope back as a fresh proposal.
	r.clock = r.clock.Add(time.Hour)
	r.signals(5, twoPeople, feedback.ActionDismissed, "issues")
	run = r.run(t)
	issues, _ := r.adj.Get(ctx, byType["issues"])
	if run.ProposalsMade != 1 || issues.Status != AdjustmentProposed || issues.ResolvedAt != nil || issues.ResolvedBy != "" || issues.Evidence.Signals != 5 {
		t.Fatalf("run = %+v issues = %+v", run, issues)
	}
}

func TestApprovingIsRefusedForACriticalOrLockedScopeAndForAnOutOfBoundsOffset(t *testing.T) {
	ctx := context.Background()
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	crafted := func(id, eventType string, delta int) {
		_ = r.adj.Save(ctx, Adjustment{ID: id, Source: "github", EventType: eventType, Delta: delta, Status: AdjustmentProposed, CreatedAt: now, UpdatedAt: now})
	}
	crafted("sec", "security_advisory", -10)
	crafted("big", "push", 51)
	crafted("zero", "push2", 0)
	for _, id := range []string{"sec", "big", "zero"} {
		if _, err := r.tuner.Approve(ctx, id, "admin"); !errors.Is(err, ErrTuneGuardrail) {
			t.Fatalf("approving %s: %v", id, err)
		}
		got, _ := r.adj.Get(ctx, id)
		if got.Status != AdjustmentProposed || r.tuning.Len() != 0 {
			t.Fatalf("a refused approval changes nothing: %+v", got)
		}
	}
}

func TestAnActiveAdjustmentThatALockedRuleNowCoversIsLeftOutOfTheSnapshot(t *testing.T) {
	ctx := context.Background()
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	_ = r.adj.Save(ctx, Adjustment{ID: "a", Source: "github", EventType: "push", Delta: -10, Status: AdjustmentActive, Version: 1, CreatedAt: now, UpdatedAt: now})
	_ = r.adj.Save(ctx, Adjustment{ID: "b", Source: "github", EventType: "security_advisory", Delta: -10, Status: AdjustmentActive, Version: 2, CreatedAt: now, UpdatedAt: now})
	if err := r.tuner.RefreshSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if r.tuning.Len() != 1 || r.tuning.Version() != 2 {
		t.Fatalf("only the uncovered adjustment may be applied: len=%d version=%d", r.tuning.Len(), r.tuning.Version())
	}
}

func TestTheImpactEstimateReplaysRecentEventsWithAndWithoutTheAdjustment(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	people := []string{"alice", "bob", "carol"}
	r.signals(9, people, feedback.ActionDismissed, "push") // N = 9 → round(20 × -9 / 11) = -16, so a push (50) drops to 34
	for i := 0; i < 4; i++ {
		r.ev.list = append(r.ev.list, event.Event{ID: fmt.Sprintf("p%d", i), Source: "github", EventType: "push", AttentionClass: event.AttentionStandard, Payload: map[string]any{}})
	}
	r.ev.list = append(r.ev.list,
		event.Event{ID: "bg", Source: "github", EventType: "push", AttentionClass: event.AttentionBackground, Payload: map[string]any{}},
		event.Event{ID: "other", Source: "github", EventType: "issues", AttentionClass: event.AttentionStandard, Payload: map[string]any{}})
	r.run(t)
	got := r.list(t, "")
	if len(got) != 1 || got[0].Delta != -16 {
		t.Fatalf("got %+v", got)
	}
	// 9 standard events from the feedback plus the 4 added, all pushes: every one drops below 35.
	if got[0].Impact.Replayed != 13 || got[0].Impact.WouldChange != 13 {
		t.Fatalf("impact = %+v", got[0].Impact)
	}
	if r.tuning.Len() != 0 {
		t.Fatal("computing the estimate changes nothing")
	}
}

func TestARunReturnsAtOnceRecordsWhatItDidAndRefusesASecondWhileRunning(t *testing.T) {
	ctx := context.Background()
	gate := make(chan struct{})
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(5, twoPeople, feedback.ActionDismissed, "pull_request")
	r.tuner.feedback = &blockingFeedback{inner: r.fb, gate: gate}

	started, err := r.tuner.Start(ctx)
	if err != nil || started.Status != RunRunning || started.FinishedAt != nil {
		t.Fatalf("Start must return at once with a running run: %+v %v", started, err)
	}
	if _, err := r.tuner.Start(ctx); !errors.Is(err, ErrRunInProgress) {
		t.Fatalf("a second run while one is running: %v", err)
	}
	if _, err := r.tuner.RunNow(ctx); !errors.Is(err, ErrRunInProgress) {
		t.Fatalf("a synchronous run while one is running: %v", err)
	}
	close(gate)
	r.tuner.Wait()

	runs, _ := r.tuner.Runs(ctx, 10)
	if len(runs) != 1 || runs[0].ID != started.ID || runs[0].Status != RunDone || runs[0].ProposalsMade != 1 || runs[0].FeedbackRead != 5 || runs[0].FinishedAt == nil {
		t.Fatalf("runs = %+v", runs)
	}
	if _, err := r.tuner.RunNow(ctx); err != nil {
		t.Fatalf("a run can be made again once the last finished: %v", err)
	}
}

type blockingFeedback struct {
	inner FeedbackReader
	gate  chan struct{}
	once  sync.Once
}

func (b *blockingFeedback) List(ctx context.Context, f feedback.Filter) ([]feedback.EventFeedback, error) {
	b.once.Do(func() { <-b.gate })
	return b.inner.List(ctx, f)
}

type failingFeedback struct{}

func (failingFeedback) List(context.Context, feedback.Filter) ([]feedback.EventFeedback, error) {
	return nil, errors.New("connection refused at postgres://user:secret@db/notif")
}

func TestAFailedRunIsRecordedWithARedactedErrorAndChangesNothing(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.tuner.feedback = failingFeedback{}
	run, err := r.tuner.RunNow(context.Background())
	if err != nil || run.Status != RunFailed || !strings.HasPrefix(run.Error, "read feedback") || strings.Contains(run.Error, "secret") || strings.Contains(run.Error, "postgres") {
		t.Fatalf("run = %+v %v", run, err)
	}
	if got := r.list(t, ""); len(got) != 0 {
		t.Fatalf("a failed run proposes nothing: %+v", got)
	}
	if _, err := r.tuner.RunNow(context.Background()); err != nil {
		t.Fatalf("a failed run does not block the next: %v", err)
	}
}

func TestARunThatNeverFinishedIsAbandonedAfterTheStaleLimit(t *testing.T) {
	ctx := context.Background()
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	if err := r.runs.Start(ctx, Run{ID: "old", Status: RunRunning, StartedAt: now.Add(-StaleRunAfter - time.Minute)}, now.Add(-StaleRunAfter-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.tuner.RunNow(ctx); err != nil {
		t.Fatalf("a run abandoned long ago must not block: %v", err)
	}
	runs, _ := r.tuner.Runs(ctx, 10)
	var old Run
	for _, run := range runs {
		if run.ID == "old" {
			old = run
		}
	}
	if old.Status != RunFailed || old.Error != abandonedRun || old.FinishedAt == nil {
		t.Fatalf("old = %+v", old)
	}
}

func TestTheConfigFallsBackToTheDefaultsAndIsBounded(t *testing.T) {
	cfg := newTuneRig(TunerConfig{}).tuner.Config()
	if cfg.MinSignals != 5 || cfg.MinPeople != 2 || cfg.MaxAdjustment != 20 || cfg.Lookback != 30*24*time.Hour || cfg.HalfLife != 14*24*time.Hour {
		t.Fatalf("defaults = %+v", cfg)
	}
	if got := newTuneRig(TunerConfig{MaxAdjustment: 500}).tuner.Config().MaxAdjustment; got != attention.MaxTuningDelta {
		t.Fatalf("the maximum is bounded by %d: %d", attention.MaxTuningDelta, got)
	}
}

func TestTheAdjustmentIDMatchesTheOneJavaComputes(t *testing.T) {
	if got := AdjustmentID("github", "pull_request", ""); got != "68abe35a59d2dd14" {
		t.Fatalf("id = %s", got)
	}
	if AdjustmentID("GitHub", "Pull_Request", "") != AdjustmentID("github", "pull_request", "") {
		t.Fatal("the scope is case-insensitive")
	}
	if AdjustmentID("github", "pull_request", "acme/api") == AdjustmentID("github", "pull_request", "") {
		t.Fatal("a repository is a different scope")
	}
}

func TestSchedulingWithAZeroIntervalDoesNothingAndACancelledContextStops(t *testing.T) {
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.tuner.Schedule(context.Background(), 0)
	r.tuner.KeepSnapshotFresh(context.Background(), 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.tuner.Schedule(ctx, time.Hour)
	r.tuner.KeepSnapshotFresh(ctx, time.Hour)
}

func TestTheScheduleMakesRunsAndTheRefresherKeepsTheSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newTuneRig(TunerConfig{}, defaultRules()...)
	r.signals(5, twoPeople, feedback.ActionDismissed, "pull_request")
	_ = r.adj.Save(ctx, Adjustment{ID: "x", Source: "github", EventType: "push", Delta: -3, Status: AdjustmentActive, Version: 1, CreatedAt: now, UpdatedAt: now})
	done := make(chan struct{})
	go func() { r.tuner.Schedule(ctx, 5*time.Millisecond); close(done) }()
	refreshed := make(chan struct{})
	go func() { r.tuner.KeepSnapshotFresh(ctx, 5*time.Millisecond); close(refreshed) }()
	deadline := time.After(5 * time.Second)
	for {
		runs, _ := r.tuner.Runs(ctx, 5)
		if len(runs) > 0 && r.tuning.Len() == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the schedule and the refresher never did their work")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	<-refreshed
}
