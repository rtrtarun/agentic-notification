package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/delivery"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/overrides"
	"github.com/agentpulse/notification-service/pkg/policy"
)

func adaptiveRules(adaptive bool) []policy.Rule {
	return []policy.Rule{
		{Name: "sec", When: policy.RuleCondition{Source: "github", EventType: "security_advisory"},
			Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "security"}},
		{Name: "github", When: policy.RuleCondition{Source: "github"},
			Then: policy.RuleOutcome{AttentionClass: "standard", Reason: "default routing", Adaptive: adaptive}},
	}
}

func noisy(id string) event.Event {
	return event.Event{ID: id, Source: "github", EventType: "star", Repository: "acme/web"}
}

func attentionPipeline(rules []policy.Rule, c attention.Classifier) (*Pipeline, *recordingProvider) {
	p, _, provider := newTestPipeline(rules)
	return p.WithClassifier(c), provider
}

func TestAnAdaptiveRuleLetsAttentionMoveNoiseToTheDigest(t *testing.T) {
	p, _ := attentionPipeline(adaptiveRules(true), attention.NewHeuristic())
	res, err := p.Handle(context.Background(), noisy("e1"))
	if err != nil {
		t.Fatal(err)
	}
	st := res.DecisionState
	if res.AttentionClass != event.AttentionBackground || st.Status != event.DecisionDigest {
		t.Fatalf("class %s status %s", res.AttentionClass, st.Status)
	}
	if st.ModelVersion != attention.ModelHeuristic || st.Confidence <= 0 || !strings.Contains(st.Reason, "default routing; attention scored") || !strings.Contains(st.Reason, "social activity") {
		t.Fatalf("the decision must say who refined it and why: %+v", st)
	}
	if st.MatchedRule != "github" {
		t.Fatalf("the matched rule must stay visible: %q", st.MatchedRule)
	}
}

func TestARuleThatIsNotAdaptiveIsNeverSecondGuessed(t *testing.T) {
	spy := &spyClassifier{}
	p, _ := attentionPipeline(adaptiveRules(false), spy)
	res, _ := p.Handle(context.Background(), noisy("e1"))
	if res.AttentionClass != event.AttentionStandard || res.DecisionState.ModelVersion != "" || spy.calls != 0 {
		t.Fatalf("an explicit rule must stand: class %s, classifier called %d times", res.AttentionClass, spy.calls)
	}
}

func TestAnEventNoRuleDecidedIsAdaptive(t *testing.T) {
	p, _ := attentionPipeline(nil, attention.NewHeuristic())
	res, _ := p.Handle(context.Background(), noisy("e1"))
	if res.AttentionClass != event.AttentionBackground || !strings.Contains(res.DecisionState.Reason, "no matching rule") {
		t.Fatalf("class %s reason %q", res.AttentionClass, res.DecisionState.Reason)
	}
}

type spyClassifier struct {
	calls int
	out   attention.Classification
	err   error
}

func (s *spyClassifier) Classify(context.Context, event.Event) (attention.Classification, error) {
	s.calls++
	return s.out, s.err
}

func TestACriticalEventNeverReachesTheClassifier(t *testing.T) {
	spy := &spyClassifier{out: attention.Classification{Class: event.AttentionBackground, Score: 1, Confidence: 1}}
	p, provider := attentionPipeline(adaptiveRules(true), spy)
	res, err := p.Handle(context.Background(), event.Event{ID: "c1", Source: "github", EventType: "security_advisory", Repository: "a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if spy.calls != 0 {
		t.Fatalf("the classifier saw a critical event %d times", spy.calls)
	}
	if res.AttentionClass != event.AttentionCritical || len(provider.sent) != 1 || res.DecisionState.ModelVersion != "" {
		t.Fatalf("critical must be untouched: %s, sent %d, model %q", res.AttentionClass, len(provider.sent), res.DecisionState.ModelVersion)
	}
}

func TestACriticalRuleCannotBeAdaptiveEvenIfFlagged(t *testing.T) {
	rules := []policy.Rule{{Name: "sec", When: policy.RuleCondition{Source: "github"},
		Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "x", Adaptive: true}}}
	spy := &spyClassifier{}
	p, _ := attentionPipeline(rules, spy)
	p.Handle(context.Background(), noisy("e1"))
	if spy.calls != 0 {
		t.Fatal("a critical rule's adaptive flag must be ignored")
	}
}

func TestAClassifierCannotEscalateOrBreakTheEvent(t *testing.T) {
	for name, spy := range map[string]*spyClassifier{
		"escalates":  {out: attention.Classification{Class: event.AttentionCritical, Score: 99, Confidence: 1}},
		"errors":     {err: errors.New("model down")},
		"nonsense":   {out: attention.Classification{Class: "urgent", Score: 5, Confidence: 1}},
		"bad number": {out: attention.Classification{Class: event.AttentionBackground, Score: 500, Confidence: 1}},
	} {
		p, provider := attentionPipeline(adaptiveRules(true), spy)
		res, err := p.Handle(context.Background(), noisy("e-"+name))
		if err != nil {
			t.Fatalf("%s: a classifier problem must never fail an event: %v", name, err)
		}
		if res.AttentionClass != event.AttentionStandard || len(provider.sent) != 0 || res.DecisionState.ModelVersion != "" {
			t.Errorf("%s: the rule's decision must stand: class %s", name, res.AttentionClass)
		}
	}
}

func TestAnOverrideOutranksAttention(t *testing.T) {
	spy := &spyClassifier{out: attention.Classification{Class: event.AttentionBackground, Score: 5, Confidence: 1}}
	p, store, _ := outboxPipeline(map[string][]string{"standard": {delivery.ChannelSlack}})
	p = p.WithClassifier(spy)
	ov, _ := overrides.NewService(context.Background(), overrides.NewMemoryStore())
	ov.Upsert(context.Background(), overrides.Override{Mode: overrides.ModeAlways, Source: "github", EventType: "star", CreatedBy: "ann"})
	p.WithOverrides(ov)

	res, _ := p.Handle(context.Background(), noisy("e1"))
	if spy.calls != 0 || res.AttentionClass != event.AttentionStandard || len(jobs(t, store)) != 1 {
		t.Fatalf("a person's always-notify must beat judgement: class %s, classifier calls %d, jobs %d", res.AttentionClass, spy.calls, len(jobs(t, store)))
	}
}

func TestBackgroundEventsAreHeldForTheDigestUnlessRouted(t *testing.T) {
	rules := []policy.Rule{{Name: "quiet", When: policy.RuleCondition{Source: "github"}, Then: policy.RuleOutcome{AttentionClass: "background", Reason: "quiet"}}}
	p, _, _ := newTestPipeline(rules)
	res, _ := p.Handle(context.Background(), noisy("e1"))
	if res.DecisionState.Status != event.DecisionDigest {
		t.Fatalf("a background event with nowhere to go is held for the digest, got %s", res.DecisionState.Status)
	}

	routed, store, _ := outboxPipeline(map[string][]string{"background": {delivery.ChannelSlack}})
	routed.policyEngine = policy.NewEngine(rules)
	res, _ = routed.Handle(context.Background(), noisy("e2"))
	if res.DecisionState.Status != event.DecisionQueued || len(jobs(t, store)) != 1 {
		t.Fatalf("a routed background event is delivered: status %s", res.DecisionState.Status)
	}
}
