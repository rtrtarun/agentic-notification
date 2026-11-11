package learning

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/feedback"
	"github.com/agentpulse/notification-service/pkg/policy"
)

// RuleReader reads the rules, to find the protected ones. *rules.Service satisfies it.
type RuleReader interface {
	List(ctx context.Context) ([]policy.Rule, error)
}

// TunerConfig tunes how much evidence a score adjustment needs. Zero values use the defaults.
type TunerConfig struct {
	// MinSignals is the number of positive plus negative signals a scope needs.
	MinSignals int
	// MinPeople is the number of distinct people those signals must come from.
	MinPeople int
	// MaxAdjustment bounds the offset, in score points, either way.
	MaxAdjustment int
	// Lookback is how far back feedback is read.
	Lookback time.Duration
	// HalfLife is the age at which a signal counts half as much.
	HalfLife time.Duration
}

// Tuner turns stored feedback into reviewable, bounded score adjustments for the
// built-in heuristic. It is offline: a run reads stored feedback and events and
// writes proposals, and it is never part of the live event path. Nothing it
// proposes changes a score until an admin approves it; the heuristic reads only
// the in-memory snapshot of approved adjustments (see attention.Tuning).
//
// The rule is deterministic and explainable: for each source and event type,
// signals about delivered, non-critical events are weighted by age (a fixed
// half-life), capped per person, and combined as
//
//	offset = round(max × (P − N) / (P + N + 2))
//
// where P and N are the weighted positive (opened, reply, always) and negative
// (dismissed, never) totals. The same feedback always gives the same proposals.
//
// Guardrail, enforced when proposing, when approving and when building the
// snapshot: a scope covered by a critical or locked rule is never adjusted, and
// feedback about a critical event is never counted.
type Tuner struct {
	adjustments AdjustmentRepository
	runs        RunRepository
	feedback    FeedbackReader
	events      EventReader
	rules       RuleReader
	tuning      *attention.Tuning
	cfg         TunerConfig
	logger      *slog.Logger
	now         func() time.Time
	mu          sync.Mutex // one analysis or resolution at a time in this process
	wg          sync.WaitGroup
}

// NewTuner wires the tuner. tuning may be nil, in which case no snapshot is kept.
func NewTuner(adj AdjustmentRepository, runs RunRepository, fb FeedbackReader, ev EventReader, rules RuleReader, tuning *attention.Tuning, cfg TunerConfig) *Tuner {
	if cfg.MinSignals <= 0 {
		cfg.MinSignals = DefaultMinSignals
	}
	if cfg.MinPeople <= 0 {
		cfg.MinPeople = DefaultMinPeople
	}
	if cfg.MaxAdjustment <= 0 {
		cfg.MaxAdjustment = DefaultMaxAdjustment
	}
	if cfg.MaxAdjustment > attention.MaxTuningDelta {
		cfg.MaxAdjustment = attention.MaxTuningDelta
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = DefaultLookback
	}
	if cfg.HalfLife <= 0 {
		cfg.HalfLife = DefaultHalfLife
	}
	return &Tuner{adjustments: adj, runs: runs, feedback: fb, events: ev, rules: rules, tuning: tuning, cfg: cfg, logger: slog.Default(), now: time.Now}
}

// WithClock replaces the clock; tests use it.
func (t *Tuner) WithClock(now func() time.Time) *Tuner {
	t.now = now
	return t
}

// WithLogger sets the logger.
func (t *Tuner) WithLogger(l *slog.Logger) *Tuner {
	if l != nil {
		t.logger = l
	}
	return t
}

// Config reports the settings in use, for the screen to show.
func (t *Tuner) Config() TunerConfig { return t.cfg }

// Version is the version of the active set the heuristic is using, 0 when none.
func (t *Tuner) Version() int { return t.tuning.Version() }

// Wait blocks until background runs started by Start have finished; tests and shutdown use it.
func (t *Tuner) Wait() { t.wg.Wait() }

// List returns adjustments with the status (all when empty).
func (t *Tuner) List(ctx context.Context, status string) ([]Adjustment, error) {
	return t.adjustments.List(ctx, status)
}

// Runs returns the latest runs, newest first.
func (t *Tuner) Runs(ctx context.Context, limit int) ([]Run, error) {
	return t.runs.List(ctx, limit)
}

// Start records a new run and returns at once; the analysis continues in the background.
// ErrRunInProgress when another run is running, in this process or any other.
func (t *Tuner) Start(ctx context.Context) (Run, error) {
	run, err := t.begin(ctx)
	if err != nil {
		return Run{}, err
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		// The request that started the run may end first; the run belongs to the service.
		t.finish(context.WithoutCancel(ctx), run)
	}()
	return run, nil
}

// RunNow records a run and waits for it to finish. Schedules and tests use it.
func (t *Tuner) RunNow(ctx context.Context) (Run, error) {
	run, err := t.begin(ctx)
	if err != nil {
		return Run{}, err
	}
	return t.finish(ctx, run), nil
}

func (t *Tuner) begin(ctx context.Context) (Run, error) {
	now := t.now().UTC()
	run := Run{ID: newRunID(), Status: RunRunning, StartedAt: now}
	if err := t.runs.Start(ctx, run, now.Add(-StaleRunAfter)); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (t *Tuner) finish(ctx context.Context, run Run) Run {
	t.mu.Lock()
	read, made, err := t.analyze(ctx)
	t.mu.Unlock()
	end := t.now().UTC()
	run.FinishedAt = &end
	run.FeedbackRead, run.EventsRead, run.ProposalsMade = read.feedback, read.events, made
	if err != nil {
		run.Status, run.Error = RunFailed, redact(err)
		t.logger.Warn("learning run failed", "run", run.ID, "error", err)
	} else {
		run.Status = RunDone
	}
	if err := t.runs.Finish(ctx, run); err != nil {
		t.logger.Warn("recording the learning run failed", "run", run.ID, "error", err)
	}
	return run
}

// Schedule makes a run every interval until ctx is cancelled. With a zero interval it does nothing.
func (t *Tuner) Schedule(ctx context.Context, every time.Duration) {
	if every <= 0 {
		return
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if run, err := t.RunNow(ctx); err != nil {
				t.logger.Info("scheduled learning run skipped", "reason", err.Error())
			} else {
				t.logger.Info("learning run finished", "run", run.ID, "status", run.Status, "proposals", run.ProposalsMade)
			}
		}
	}
}

// KeepSnapshotFresh reloads the snapshot of approved adjustments every interval until ctx is cancelled,
// so a change made through another process reaches this one.
func (t *Tuner) KeepSnapshotFresh(ctx context.Context, every time.Duration) {
	if every <= 0 {
		return
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := t.RefreshSnapshot(ctx); err != nil {
				t.logger.Warn("refreshing the learned adjustments failed; using the last good set until it goes stale", "error", err)
			}
		}
	}
}

type readCounts struct{ feedback, events int }

// voter is what one person did in one scope.
type voter struct {
	pos, neg float64
	n        int
}

// scope collects the evidence for one source and event type.
type scope struct {
	source, eventType string
	voters            map[string]*voter
	positive          int // raw counts, for the evidence
	negative          int
}

const maxFeedbackReadForTuning = maxFeedbackRead

func (t *Tuner) analyze(ctx context.Context) (readCounts, int, error) {
	var read readCounts
	now := t.now()
	signals, err := t.feedback.List(ctx, feedback.Filter{Since: now.Add(-t.cfg.Lookback), Limit: maxFeedbackReadForTuning})
	if err != nil {
		return read, 0, fmt.Errorf("read feedback: %w", err)
	}
	read.feedback = len(signals)
	events, err := t.events.List(ctx)
	if err != nil {
		return read, 0, fmt.Errorf("read events: %w", err)
	}
	read.events = len(events)
	rules, err := t.rules.List(ctx)
	if err != nil {
		return read, 0, fmt.Errorf("read rules: %w", err)
	}
	existing, err := t.adjustments.List(ctx, "")
	if err != nil {
		return read, 0, fmt.Errorf("read adjustments: %w", err)
	}
	byScope := map[string]Adjustment{}
	for _, a := range existing {
		byScope[ScopeKey(a.Source, a.EventType, a.Repository)] = a
	}
	byID := make(map[string]event.Event, len(events))
	for _, e := range events {
		byID[e.ID] = e
	}
	protectedByName := map[string]bool{}
	for _, r := range rules {
		if protected(r) {
			protectedByName[r.Name] = true
		}
	}

	scopes := map[string]*scope{}
	for _, f := range signals {
		positive := f.Action == feedback.ActionOpened || f.Action == feedback.ActionReply || f.Action == feedback.ActionAlways
		negative := f.Action == feedback.ActionDismissed || f.Action == feedback.ActionNever
		if !positive && !negative {
			continue
		}
		evt, ok := byID[f.EventID]
		if !ok || !learnable(evt) {
			continue // gone from the recent window, critical, or not delivered: never learned from
		}
		if evt.DecisionState != nil && protectedByName[evt.DecisionState.MatchedRule] {
			continue
		}
		key := ScopeKey(f.Source, f.EventType, "")
		if prior, ok := byScope[key]; ok && prior.Status == AdjustmentRetired && prior.ResolvedAt != nil && !f.CreatedAt.After(*prior.ResolvedAt) {
			continue // describes the behaviour before it was retired
		}
		sc := scopes[key]
		if sc == nil {
			sc = &scope{source: f.Source, eventType: f.EventType, voters: map[string]*voter{}}
			scopes[key] = sc
		}
		v := sc.voters[f.UserID]
		if v == nil {
			v = &voter{}
			sc.voters[f.UserID] = v
		}
		w := weight(now.Sub(f.CreatedAt), t.cfg.HalfLife)
		v.n++
		if positive {
			v.pos += w
			sc.positive++
		} else {
			v.neg += w
			sc.negative++
		}
	}

	keys := make([]string, 0, len(scopes))
	for k := range scopes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	made := 0
	for _, k := range keys {
		sc := scopes[k]
		prior, hasPrior := byScope[k]
		if hasPrior && (prior.Status == AdjustmentActive || prior.Status == AdjustmentRejected) {
			continue // an admin already decided; a run never overrides that
		}
		if coveredByProtectedRule(rules, sc.source, sc.eventType) {
			continue
		}
		adj, ok := t.propose(sc, events, now)
		if !ok {
			continue
		}
		if hasPrior {
			adj.CreatedAt = prior.CreatedAt
			adj.Version = prior.Version
		}
		if err := t.adjustments.Save(ctx, adj); err != nil {
			return read, made, fmt.Errorf("save adjustment: %w", err)
		}
		made++
	}
	return read, made, nil
}

// propose applies the rule to one scope's evidence.
func (t *Tuner) propose(sc *scope, events []event.Event, now time.Time) (Adjustment, bool) {
	// Sums are taken in a fixed order (by person) and rounded to six decimals, so the result does not depend on
	// map order, and the Java service, which does the same, gets the very same numbers.
	people := make([]string, 0, len(sc.voters))
	for id := range sc.voters {
		people = append(people, id)
	}
	sort.Strings(people)
	var p, n float64
	signals := 0
	for _, id := range people {
		v := sc.voters[id]
		p += math.Min(round6(v.pos), PerPersonCap)
		n += math.Min(round6(v.neg), PerPersonCap)
		signals += v.n
	}
	p, n = round6(p), round6(n)
	if signals < t.cfg.MinSignals || len(people) < t.cfg.MinPeople {
		return Adjustment{}, false
	}
	delta := offset(p, n, t.cfg.MaxAdjustment)
	if delta == 0 {
		return Adjustment{}, false
	}
	count, word := sc.negative, "negative"
	if delta > 0 {
		count, word = sc.positive, "positive"
	}
	adj := Adjustment{
		ID: AdjustmentID(sc.source, sc.eventType, ""), Source: sc.source, EventType: sc.eventType, Delta: delta, Status: AdjustmentProposed,
		Evidence: TuneEvidence{
			Positive: sc.positive, Negative: sc.negative, People: len(people), Signals: signals,
			WindowDays: int(t.cfg.Lookback / (24 * time.Hour)),
			Summary:    fmt.Sprintf("%d of %d signals from %s were %s", count, signals, people2(len(people)), word),
		},
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	adj.Impact = impact(adj, events)
	return adj, true
}

// offset is the whole rule: max × (P − N) / (P + N + 2), rounded and clamped.
func offset(p, n float64, max int) int {
	d := int(math.Round(float64(max) * (p - n) / (p + n + smoothing)))
	if d > max {
		return max
	}
	if d < -max {
		return -max
	}
	return d
}

// weight halves every half-life, to six decimals so two implementations agree.
func weight(age, halfLife time.Duration) float64 {
	if age < 0 {
		age = 0
	}
	return round6(math.Pow(0.5, float64(age)/float64(halfLife)))
}

// round6 rounds to six decimals, half away from zero.
func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }

// learnable says whether an event's feedback may be learned from: it must be a
// non-critical event that was actually delivered (queued or sent). Events held for
// a digest, merged, delayed or over budget are left out, so a demoted event that can
// no longer be opened does not look worse and worse.
func learnable(evt event.Event) bool {
	if evt.AttentionClass != event.AttentionStandard && evt.AttentionClass != event.AttentionBackground {
		return false
	}
	if evt.DecisionState == nil {
		return false
	}
	return evt.DecisionState.Status == event.DecisionQueued || evt.DecisionState.Status == event.DecisionSent
}

// impact replays recent standard events of the scope through the heuristic with and without the adjustment.
func impact(adj Adjustment, events []event.Event) Impact {
	with := attention.NewTuning(time.Hour)
	with.Set(1, []attention.Adjustment{{ID: adj.ID, Source: adj.Source, EventType: adj.EventType, Repository: adj.Repository, Delta: adj.Delta, Summary: adj.Evidence.Summary}})
	before, after := attention.NewHeuristic(), attention.NewHeuristic().WithTuning(with)
	var out Impact
	for _, e := range events {
		if e.AttentionClass != event.AttentionStandard || !strings.EqualFold(e.Source, adj.Source) || !strings.EqualFold(e.EventType, adj.EventType) {
			continue
		}
		a, errA := before.Classify(context.Background(), e)
		b, errB := after.Classify(context.Background(), e)
		if errA != nil || errB != nil {
			continue
		}
		out.Replayed++
		if a.Class != b.Class {
			out.WouldChange++
		}
	}
	return out
}

// Approve makes a proposed adjustment active. It re-checks the guardrail against the rules as they are now.
func (t *Tuner) Approve(ctx context.Context, id, actor string) (Adjustment, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, err := t.adjustments.Get(ctx, id)
	if err != nil {
		return Adjustment{}, err
	}
	if a.Status != AdjustmentProposed {
		return Adjustment{}, ErrNotProposed
	}
	rules, err := t.rules.List(ctx)
	if err != nil {
		return Adjustment{}, fmt.Errorf("read rules: %w", err)
	}
	if coveredByProtectedRule(rules, a.Source, a.EventType) {
		return Adjustment{}, ErrTuneGuardrail
	}
	if a.Delta == 0 || a.Delta > attention.MaxTuningDelta || a.Delta < -attention.MaxTuningDelta {
		return Adjustment{}, ErrTuneGuardrail
	}
	max, err := t.adjustments.MaxVersion(ctx)
	if err != nil {
		return Adjustment{}, fmt.Errorf("read version: %w", err)
	}
	a, err = t.resolve(ctx, a, AdjustmentActive, actor, max+1)
	if err != nil {
		return Adjustment{}, err
	}
	t.refreshOrWarn(ctx, rules)
	return a, nil
}

// Reject refuses a proposed adjustment; it is not proposed again.
func (t *Tuner) Reject(ctx context.Context, id, actor string) (Adjustment, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, err := t.adjustments.Get(ctx, id)
	if err != nil {
		return Adjustment{}, err
	}
	if a.Status != AdjustmentProposed {
		return Adjustment{}, ErrNotProposed
	}
	return t.resolve(ctx, a, AdjustmentRejected, actor, a.Version)
}

// Retire switches an active adjustment off; scores return to what they were without it.
func (t *Tuner) Retire(ctx context.Context, id, actor string) (Adjustment, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, err := t.adjustments.Get(ctx, id)
	if err != nil {
		return Adjustment{}, err
	}
	if a.Status != AdjustmentActive {
		return Adjustment{}, ErrNotActive
	}
	max, err := t.adjustments.MaxVersion(ctx)
	if err != nil {
		return Adjustment{}, fmt.Errorf("read version: %w", err)
	}
	a, err = t.resolve(ctx, a, AdjustmentRetired, actor, max+1)
	if err != nil {
		return Adjustment{}, err
	}
	rules, rerr := t.rules.List(ctx)
	if rerr != nil {
		// Retiring must always succeed in the store; the snapshot catches up on its next refresh.
		t.logger.Warn("reading rules to refresh the learned adjustments failed", "error", rerr)
		return a, nil
	}
	t.refreshOrWarn(ctx, rules)
	return a, nil
}

// refreshOrWarn refreshes the snapshot after a decision was stored. A failure is not the admin's
// problem: the decision stands and the snapshot catches up on its next refresh.
func (t *Tuner) refreshOrWarn(ctx context.Context, rules []policy.Rule) {
	if err := t.refresh(ctx, rules); err != nil {
		t.logger.Warn("refreshing the learned adjustments failed; the next refresh will catch up", "error", err)
	}
}

func (t *Tuner) resolve(ctx context.Context, a Adjustment, status, actor string, version int) (Adjustment, error) {
	now := t.now().UTC()
	a.Status, a.ResolvedBy, a.ResolvedAt, a.UpdatedAt, a.Version = status, actor, &now, now, version
	if err := t.adjustments.Save(ctx, a); err != nil {
		return Adjustment{}, fmt.Errorf("save adjustment: %w", err)
	}
	return a, nil
}

// RefreshSnapshot reloads the in-memory set of approved adjustments the heuristic reads.
// An adjustment whose scope a critical or locked rule now covers is left out.
func (t *Tuner) RefreshSnapshot(ctx context.Context) error {
	rules, err := t.rules.List(ctx)
	if err != nil {
		return fmt.Errorf("read rules: %w", err)
	}
	return t.refresh(ctx, rules)
}

func (t *Tuner) refresh(ctx context.Context, rules []policy.Rule) error {
	if t.tuning == nil {
		return nil
	}
	active, err := t.adjustments.List(ctx, AdjustmentActive)
	if err != nil {
		return fmt.Errorf("read adjustments: %w", err)
	}
	version, err := t.adjustments.MaxVersion(ctx)
	if err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	set := make([]attention.Adjustment, 0, len(active))
	for _, a := range active {
		if coveredByProtectedRule(rules, a.Source, a.EventType) {
			continue
		}
		set = append(set, attention.Adjustment{ID: a.ID, Source: a.Source, EventType: a.EventType, Repository: a.Repository, Delta: a.Delta, Summary: a.Evidence.Summary})
	}
	t.tuning.Set(version, set)
	return nil
}

// AdjustmentID is the first 8 bytes of sha256 over the scope key, as hex. Both services compute it the same way.
func AdjustmentID(source, eventType, repository string) string {
	sum := sha256.Sum256([]byte(ScopeKey(source, eventType, repository)))
	return hex.EncodeToString(sum[:8])
}

// protected is the guardrail: a critical rule, or any locked rule, is never tuned around.
func protected(r policy.Rule) bool {
	return r.Locked || r.Then.AttentionClass == event.AttentionCritical
}

// coveredByProtectedRule reports whether a critical or locked rule applies to the source and event type
// (a repository restriction on the rule is ignored, to err on the side of protection).
func coveredByProtectedRule(rules []policy.Rule, source, eventType string) bool {
	probe := event.Event{Source: source, EventType: eventType}
	for _, r := range rules {
		if protected(r) && policy.Matches(policy.RuleCondition{Source: r.When.Source, EventType: r.When.EventType}, probe) {
			return true
		}
	}
	return false
}

func people2(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", n)
}

func newRunID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// redact keeps an error message short and drops any word that looks like an address with credentials (anything containing "://").
func redact(err error) string {
	words := strings.Fields(err.Error())
	kept := words[:0]
	for _, w := range words {
		if !strings.Contains(w, "://") {
			kept = append(kept, w)
		}
	}
	msg := strings.Join(kept, " ")
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}
