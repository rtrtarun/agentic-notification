package learning

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/feedback"
	"github.com/agentpulse/notification-service/pkg/overrides"
	"github.com/agentpulse/notification-service/pkg/policy"
)

var now = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type fakeFeedback struct {
	list  []feedback.EventFeedback
	since time.Time
}

func (f *fakeFeedback) List(_ context.Context, filter feedback.Filter) ([]feedback.EventFeedback, error) {
	f.since = filter.Since
	return f.list, nil
}

type fakeEvents struct{ list []event.Event }

func (f *fakeEvents) List(context.Context) ([]event.Event, error) { return f.list, nil }

type fakeRules struct {
	list    []policy.Rule
	updates []policy.Rule
}

func (f *fakeRules) List(context.Context) ([]policy.Rule, error) {
	return append([]policy.Rule(nil), f.list...), nil
}

func (f *fakeRules) Update(_ context.Context, id string, in policy.Rule) (policy.Rule, error) {
	for i := range f.list {
		if f.list[i].ID == id {
			f.list[i] = in
			f.updates = append(f.updates, in)
			return in, nil
		}
	}
	return policy.Rule{}, errors.New("no such rule")
}

type fakeOverrides struct{ made []overrides.Override }

func (f *fakeOverrides) Upsert(_ context.Context, o overrides.Override) (overrides.Override, error) {
	f.made = append(f.made, o)
	return o, nil
}

type rig struct {
	svc *Service
	fb  *fakeFeedback
	ev  *fakeEvents
	ru  *fakeRules
	ov  *fakeOverrides
}

func newRig(rules ...policy.Rule) *rig {
	r := &rig{fb: &fakeFeedback{}, ev: &fakeEvents{}, ru: &fakeRules{list: rules}, ov: &fakeOverrides{}}
	r.svc = NewService(NewMemoryRepository(), r.fb, r.ev, r.ru, r.ov, Config{}).WithClock(func() time.Time { return now })
	return r
}

func standardRule(name string, adaptive bool) policy.Rule {
	return policy.Rule{ID: "r-" + name, Name: name, When: policy.RuleCondition{Source: "github"}, Then: policy.RuleOutcome{AttentionClass: event.AttentionStandard, Reason: "x", Adaptive: adaptive}}
}

func backgroundRule(name string) policy.Rule {
	return policy.Rule{ID: "r-" + name, Name: name, Then: policy.RuleOutcome{AttentionClass: event.AttentionBackground, Reason: "x"}}
}

// signals adds n pieces of feedback of one action about distinct events of one kind.
func (r *rig) signals(n int, action, class, rule, eventType, repo string) {
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-%s-%s-%d", eventType, action, rule, len(r.ev.list))
		r.ev.list = append(r.ev.list, event.Event{
			ID: id, Source: "github", EventType: eventType, Repository: repo, AttentionClass: class,
			DecisionState: &event.DecisionState{MatchedRule: rule},
		})
		r.fb.list = append(r.fb.list, feedback.EventFeedback{
			ID: "fb-" + id, EventID: id, UserID: "alice", Action: action, Source: "github", EventType: eventType, Repository: repo, CreatedAt: now,
		})
	}
}

func (r *rig) review(t *testing.T) []Suggestion {
	t.Helper()
	list, err := r.svc.Review(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestDismissedStandardRuleSuggestsLettingAttentionRefineIt(t *testing.T) {
	r := newRig(standardRule("repo-activity", false))
	r.signals(3, feedback.ActionDismissed, event.AttentionStandard, "repo-activity", "star", "acme/web")
	r.signals(2, feedback.ActionNever, event.AttentionStandard, "repo-activity", "star", "acme/web")
	got := r.review(t)
	if len(got) != 1 {
		t.Fatalf("want one suggestion, got %+v", got)
	}
	s := got[0]
	if s.Kind != KindAdaptive || s.RuleID != "r-repo-activity" || s.Status != StatusPending || s.Evidence.Dismissed != 3 || s.Evidence.Never != 2 {
		t.Fatalf("suggestion = %+v", s)
	}
	if s.Source != "github" || s.EventType != "star" || s.Repository != "acme/web" || s.Summary == "" || s.Proposed == "" {
		t.Fatalf("suggestion fields = %+v", s)
	}
}

func TestAnAlreadyAdaptiveRuleSuggestsDemotion(t *testing.T) {
	r := newRig(standardRule("repo-activity", true))
	r.signals(5, feedback.ActionDismissed, event.AttentionStandard, "repo-activity", "star", "")
	got := r.review(t)
	if len(got) != 1 || got[0].Kind != KindDemote {
		t.Fatalf("got %+v", got)
	}
}

func TestNoMatchingRuleSuggestsAScopedNeverOverride(t *testing.T) {
	r := newRig()
	r.signals(5, feedback.ActionDismissed, event.AttentionStandard, "", "fork", "acme/web")
	got := r.review(t)
	if len(got) != 1 || got[0].Kind != KindOverride || got[0].RuleID != "" || got[0].Repository != "acme/web" {
		t.Fatalf("got %+v", got)
	}
}

func TestRepositoryIsOnlyKeptWhenEveryReportCameFromOne(t *testing.T) {
	r := newRig()
	r.signals(3, feedback.ActionDismissed, event.AttentionStandard, "", "fork", "acme/web")
	r.signals(3, feedback.ActionDismissed, event.AttentionStandard, "", "fork", "acme/api")
	got := r.review(t)
	if len(got) != 1 || got[0].Repository != "" || got[0].Evidence.Dismissed != 6 {
		t.Fatalf("got %+v", got)
	}
}

func TestOneOpenedSignalOrTooLittleEvidenceMeansNoSuggestion(t *testing.T) {
	r := newRig(standardRule("repo-activity", false))
	r.signals(10, feedback.ActionDismissed, event.AttentionStandard, "repo-activity", "star", "")
	r.signals(1, feedback.ActionOpened, event.AttentionStandard, "repo-activity", "star", "")
	if got := r.review(t); len(got) != 0 {
		t.Fatalf("someone opened one, so nothing should be suggested: %+v", got)
	}
	few := newRig(standardRule("repo-activity", false))
	few.signals(DefaultMinSignals-1, feedback.ActionDismissed, event.AttentionStandard, "repo-activity", "star", "")
	if got := few.review(t); len(got) != 0 {
		t.Fatalf("too little evidence: %+v", got)
	}
}

func TestAlwaysSignalsOnABackgroundRuleSuggestPromotion(t *testing.T) {
	r := newRig(backgroundRule("quiet"))
	r.signals(2, feedback.ActionAlways, event.AttentionBackground, "quiet", "push", "")
	got := r.review(t)
	if len(got) != 1 || got[0].Kind != KindPromote || got[0].RuleID != "r-quiet" {
		t.Fatalf("got %+v", got)
	}

	none := newRig()
	none.signals(2, feedback.ActionAlways, event.AttentionBackground, "", "push", "")
	if got := none.review(t); len(got) != 1 || got[0].Kind != KindOverride {
		t.Fatalf("without a rule an always override is suggested: %+v", got)
	}

	mixed := newRig(backgroundRule("quiet"))
	mixed.signals(3, feedback.ActionAlways, event.AttentionBackground, "quiet", "push", "")
	mixed.signals(1, feedback.ActionDismissed, event.AttentionBackground, "quiet", "push", "")
	if got := mixed.review(t); len(got) != 0 {
		t.Fatalf("a dismissal contradicts the always signals: %+v", got)
	}
}

// ---- the guardrail ----

func TestLockedAndCriticalRulesNeverGetSuggestions(t *testing.T) {
	locked := standardRule("locked-one", false)
	locked.Locked = true
	critical := policy.Rule{ID: "r-crit", Name: "security", Then: policy.RuleOutcome{AttentionClass: event.AttentionCritical, Reason: "x"}}
	r := newRig(locked, critical)
	r.signals(20, feedback.ActionDismissed, event.AttentionStandard, "locked-one", "star", "")
	r.signals(20, feedback.ActionDismissed, event.AttentionStandard, "security", "advisory", "")
	if got := r.review(t); len(got) != 0 {
		t.Fatalf("a locked or critical rule must never be suggested for: %+v", got)
	}
}

func TestFeedbackAboutCriticalEventsIsIgnored(t *testing.T) {
	r := newRig()
	r.signals(30, feedback.ActionDismissed, event.AttentionCritical, "security", "advisory", "")
	r.signals(30, feedback.ActionNever, event.AttentionCritical, "", "outage", "")
	if got := r.review(t); len(got) != 0 {
		t.Fatalf("critical events must never be learned from: %+v", got)
	}
}

func TestApplyRefusesARuleThatBecameLockedOrCritical(t *testing.T) {
	r := newRig(standardRule("repo-activity", false))
	r.signals(5, feedback.ActionDismissed, event.AttentionStandard, "repo-activity", "star", "")
	sug := r.review(t)[0]

	r.ru.list[0].Locked = true // someone locked it after the suggestion was made
	if _, err := r.svc.Apply(context.Background(), sug.ID, "admin"); !errors.Is(err, ErrGuardrail) {
		t.Fatalf("got %v, want ErrGuardrail", err)
	}
	r.ru.list[0].Locked = false
	r.ru.list[0].Then.AttentionClass = event.AttentionCritical
	if _, err := r.svc.Apply(context.Background(), sug.ID, "admin"); !errors.Is(err, ErrGuardrail) {
		t.Fatalf("got %v, want ErrGuardrail", err)
	}
	if len(r.ru.updates) != 0 {
		t.Fatalf("no rule may be written: %+v", r.ru.updates)
	}
	if got, _ := r.svc.repo.Get(context.Background(), sug.ID); got.Status != StatusPending {
		t.Fatalf("a refused suggestion stays pending, got %s", got.Status)
	}
}

func TestApplyNeverProducesACriticalRule(t *testing.T) {
	r := newRig(standardRule("a", true), backgroundRule("b"))
	r.signals(5, feedback.ActionDismissed, event.AttentionStandard, "a", "star", "")
	r.signals(2, feedback.ActionAlways, event.AttentionBackground, "b", "push", "")
	for _, s := range r.review(t) {
		if _, err := r.svc.Apply(context.Background(), s.ID, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range r.ru.updates {
		if u.Then.AttentionClass == event.AttentionCritical {
			t.Fatalf("a suggestion produced a critical rule: %+v", u)
		}
	}
	if len(r.ru.updates) != 2 {
		t.Fatalf("both suggestions should change a rule: %+v", r.ru.updates)
	}
}

// ---- applying and dismissing ----

func TestApplyAdaptiveChangesTheRuleAndIsRecorded(t *testing.T) {
	r := newRig(standardRule("repo-activity", false))
	r.signals(5, feedback.ActionDismissed, event.AttentionStandard, "repo-activity", "star", "")
	sug := r.review(t)[0]
	done, err := r.svc.Apply(context.Background(), sug.ID, "admin@corp.com")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusApplied || done.ResolvedBy != "admin@corp.com" || done.ResolvedAt == nil {
		t.Fatalf("not recorded: %+v", done)
	}
	if !r.ru.list[0].Then.Adaptive || r.ru.list[0].Then.AttentionClass != event.AttentionStandard {
		t.Fatalf("rule not updated as proposed: %+v", r.ru.list[0])
	}
	if _, err := r.svc.Apply(context.Background(), sug.ID, "admin"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("applying twice: %v", err)
	}
	if again := r.review(t); len(again) != 0 {
		t.Fatalf("an applied suggestion must not come back: %+v", again)
	}
}

func TestApplyDemoteAndPromoteChangeTheClass(t *testing.T) {
	r := newRig(standardRule("a", true), backgroundRule("b"))
	r.signals(5, feedback.ActionDismissed, event.AttentionStandard, "a", "star", "")
	r.signals(2, feedback.ActionAlways, event.AttentionBackground, "b", "push", "")
	for _, s := range r.review(t) {
		if _, err := r.svc.Apply(context.Background(), s.ID, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	if r.ru.list[0].Then.AttentionClass != event.AttentionBackground || r.ru.list[1].Then.AttentionClass != event.AttentionStandard {
		t.Fatalf("classes not changed: %+v", r.ru.list)
	}
}

func TestApplyOverrideCreatesAScopedOverrideNamingTheAdmin(t *testing.T) {
	r := newRig()
	r.signals(5, feedback.ActionNever, event.AttentionStandard, "", "fork", "acme/web")
	sug := r.review(t)[0]
	if _, err := r.svc.Apply(context.Background(), sug.ID, "admin@corp.com"); err != nil {
		t.Fatal(err)
	}
	if len(r.ov.made) != 1 {
		t.Fatalf("override not created: %+v", r.ov.made)
	}
	o := r.ov.made[0]
	if o.Mode != overrides.ModeNever || o.Source != "github" || o.EventType != "fork" || o.Repository != "acme/web" || o.CreatedBy != "admin@corp.com" {
		t.Fatalf("override = %+v", o)
	}

	always := newRig()
	always.signals(2, feedback.ActionAlways, event.AttentionBackground, "", "push", "")
	if _, err := always.svc.Apply(context.Background(), always.review(t)[0].ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if always.ov.made[0].Mode != overrides.ModeAlways {
		t.Fatalf("mode = %s", always.ov.made[0].Mode)
	}
}

func TestApplyWhenTheRuleWasDeleted(t *testing.T) {
	r := newRig(standardRule("repo-activity", false))
	r.signals(5, feedback.ActionDismissed, event.AttentionStandard, "repo-activity", "star", "")
	sug := r.review(t)[0]
	r.ru.list = nil
	if _, err := r.svc.Apply(context.Background(), sug.ID, "admin"); !errors.Is(err, ErrRuleGone) {
		t.Fatalf("got %v", err)
	}
}

func TestDismissedSuggestionsAreNotMadeAgainButPendingEvidenceRefreshes(t *testing.T) {
	r := newRig(standardRule("repo-activity", false))
	r.signals(5, feedback.ActionDismissed, event.AttentionStandard, "repo-activity", "star", "")
	first := r.review(t)[0]

	r.signals(3, feedback.ActionDismissed, event.AttentionStandard, "repo-activity", "star", "")
	refreshed := r.review(t)
	if len(refreshed) != 1 || refreshed[0].ID != first.ID || refreshed[0].Evidence.Dismissed != 8 || !refreshed[0].CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("pending evidence should refresh in place: %+v", refreshed)
	}

	done, err := r.svc.Dismiss(context.Background(), first.ID, "admin")
	if err != nil || done.Status != StatusDismissed || done.ResolvedBy != "admin" {
		t.Fatalf("dismiss: %+v %v", done, err)
	}
	if again := r.review(t); len(again) != 0 {
		t.Fatalf("a dismissed suggestion must not return: %+v", again)
	}
	if _, err := r.svc.Dismiss(context.Background(), first.ID, "admin"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("dismissing twice: %v", err)
	}
	if len(r.ru.updates) != 0 {
		t.Fatal("dismissing must change nothing")
	}
}

func TestUnknownSuggestionAndLookback(t *testing.T) {
	r := newRig()
	if _, err := r.svc.Apply(context.Background(), "nope", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("apply: %v", err)
	}
	if _, err := r.svc.Dismiss(context.Background(), "nope", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dismiss: %v", err)
	}
	r.signals(1, feedback.ActionOpened, event.AttentionStandard, "", "star", "")
	r.review(t)
	if want := now.Add(-DefaultLookback); !r.fb.since.Equal(want) {
		t.Fatalf("lookback = %v, want %v", r.fb.since, want)
	}
	if r.svc.MinSignals() != DefaultMinSignals {
		t.Fatalf("MinSignals = %d", r.svc.MinSignals())
	}
}

func TestFeedbackWithoutAKnownEventIsSkipped(t *testing.T) {
	r := newRig()
	for i := 0; i < 10; i++ {
		r.fb.list = append(r.fb.list, feedback.EventFeedback{ID: fmt.Sprint(i), EventID: "gone", Action: feedback.ActionDismissed, Source: "github", EventType: "star"})
	}
	if got := r.review(t); len(got) != 0 {
		t.Fatalf("feedback about events no longer stored cannot be classified: %+v", got)
	}
}

func TestRepositoryContract(t *testing.T) { repositoryContract(t, NewMemoryRepository()) }

func TestDescriptionsReadNaturally(t *testing.T) {
	cases := []struct{ source, typ, want string }{
		{"github", "star", "github star notifications"},
		{"github", "", "github notifications"},
		{"", "", "these notifications"},
	}
	for _, c := range cases {
		if got := describe(c.source, c.typ); got != c.want {
			t.Errorf("describe(%q,%q) = %q, want %q", c.source, c.typ, got, c.want)
		}
	}
	if inRepo("") != "" || inRepo("acme/web") != " in acme/web" {
		t.Error("inRepo wrong")
	}
}

func TestApplyRefusesAnUnknownKind(t *testing.T) {
	r := newRig()
	if err := r.svc.repo.Save(context.Background(), Suggestion{ID: "x", Kind: "teleport", Status: StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Apply(context.Background(), "x", "admin"); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
	if got, _ := r.svc.repo.Get(context.Background(), "x"); got.Status != StatusPending {
		t.Fatalf("a refused suggestion stays pending, got %s", got.Status)
	}
}

func TestEvidenceTotals(t *testing.T) {
	e := Evidence{Dismissed: 2, Never: 3, Opened: 1, Reply: 4, Always: 5}
	if e.Negative() != 5 || e.Positive() != 10 {
		t.Fatalf("negative %d positive %d", e.Negative(), e.Positive())
	}
}
