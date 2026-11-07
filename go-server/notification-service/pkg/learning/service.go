package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/feedback"
	"github.com/agentpulse/notification-service/pkg/overrides"
	"github.com/agentpulse/notification-service/pkg/policy"
)

// FeedbackReader reads stored feedback. feedback.Store satisfies it.
type FeedbackReader interface {
	List(ctx context.Context, f feedback.Filter) ([]feedback.EventFeedback, error)
}

// EventReader reads recent events. event.Repository satisfies it.
type EventReader interface {
	List(ctx context.Context) ([]event.Event, error)
}

// RuleEditor reads and edits rules. *rules.Service satisfies it.
type RuleEditor interface {
	List(ctx context.Context) ([]policy.Rule, error)
	Update(ctx context.Context, id string, in policy.Rule) (policy.Rule, error)
}

// OverrideWriter creates overrides. *overrides.Service satisfies it.
type OverrideWriter interface {
	Upsert(ctx context.Context, o overrides.Override) (overrides.Override, error)
}

// Config tunes how much evidence a suggestion needs. Zero values use the defaults.
type Config struct {
	// MinSignals is the number of dismissed or never signals, with no positive signal, before demoting is suggested.
	MinSignals int
	// MinAlways is the number of "always notify" signals, with no negative signal, before promoting is suggested.
	MinAlways int
	// Lookback is how far back feedback is read.
	Lookback time.Duration
}

// Service analyses feedback and applies reviewed suggestions.
type Service struct {
	repo      Repository
	feedback  FeedbackReader
	events    EventReader
	rules     RuleEditor
	overrides OverrideWriter
	cfg       Config
	now       func() time.Time
	mu        sync.Mutex // one analysis or resolution at a time
}

// NewService wires the service. Every dependency is an interface, so each can be faked.
func NewService(repo Repository, fb FeedbackReader, ev EventReader, ru RuleEditor, ov OverrideWriter, cfg Config) *Service {
	if cfg.MinSignals <= 0 {
		cfg.MinSignals = DefaultMinSignals
	}
	if cfg.MinAlways <= 0 {
		cfg.MinAlways = DefaultMinAlways
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = DefaultLookback
	}
	return &Service{repo: repo, feedback: fb, events: ev, rules: ru, overrides: ov, cfg: cfg, now: time.Now}
}

// WithClock replaces the clock; tests use it.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// MinSignals reports the evidence threshold in use, for the screen to show.
func (s *Service) MinSignals() int { return s.cfg.MinSignals }

// group is the feedback about one kind of event handled by one rule.
type group struct {
	source, eventType, ruleName, class string
	repositories                       map[string]bool
	evidence                           Evidence
}

// Review refreshes suggestions from the latest feedback and returns the pending ones.
func (s *Service) Review(ctx context.Context) ([]Suggestion, error) {
	if err := s.Analyze(ctx); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, StatusPending)
}

// Analyze reads recent feedback and stores a pending suggestion wherever the
// evidence is clear. A suggestion that was already applied or dismissed is not made again.
func (s *Service) Analyze(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	since := s.now().Add(-s.cfg.Lookback)
	signals, err := s.feedback.List(ctx, feedback.Filter{Since: since, Limit: maxFeedbackRead})
	if err != nil {
		return fmt.Errorf("read feedback: %w", err)
	}
	if len(signals) == 0 {
		return nil
	}
	events, err := s.events.List(ctx)
	if err != nil {
		return fmt.Errorf("read events: %w", err)
	}
	byID := make(map[string]event.Event, len(events))
	for _, e := range events {
		byID[e.ID] = e
	}
	rules, err := s.rules.List(ctx)
	if err != nil {
		return fmt.Errorf("read rules: %w", err)
	}

	// Feedback from before a suggestion was applied describes the old behaviour,
	// so it must not argue for the next change to the same thing.
	applied, err := s.repo.List(ctx, StatusApplied)
	if err != nil {
		return fmt.Errorf("read applied suggestions: %w", err)
	}
	appliedAt := map[string]time.Time{}
	for _, a := range applied {
		if a.ResolvedAt != nil {
			k := scopeKey(a.Source, a.EventType, a.RuleName)
			if a.ResolvedAt.After(appliedAt[k]) {
				appliedAt[k] = *a.ResolvedAt
			}
		}
	}

	groups := map[string]*group{}
	for _, f := range signals {
		evt, ok := byID[f.EventID]
		if !ok || evt.AttentionClass == event.AttentionCritical {
			continue // gone from the recent window, or critical: never learned from
		}
		if evt.AttentionClass != event.AttentionStandard && evt.AttentionClass != event.AttentionBackground {
			continue
		}
		rule := ""
		if evt.DecisionState != nil {
			rule = evt.DecisionState.MatchedRule
		}
		if cutoff, ok := appliedAt[scopeKey(f.Source, f.EventType, rule)]; ok && !f.CreatedAt.After(cutoff) {
			continue
		}
		key := strings.Join([]string{f.Source, f.EventType, rule, evt.AttentionClass}, "\x00")
		g := groups[key]
		if g == nil {
			g = &group{source: f.Source, eventType: f.EventType, ruleName: rule, class: evt.AttentionClass, repositories: map[string]bool{}}
			groups[key] = g
		}
		if f.Repository != "" {
			g.repositories[f.Repository] = true
		}
		switch f.Action {
		case feedback.ActionDismissed:
			g.evidence.Dismissed++
		case feedback.ActionNever:
			g.evidence.Never++
		case feedback.ActionOpened:
			g.evidence.Opened++
		case feedback.ActionReply:
			g.evidence.Reply++
		case feedback.ActionAlways:
			g.evidence.Always++
		}
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if sug, ok := s.suggest(groups[k], rules); ok {
			if err := s.store(ctx, sug); err != nil {
				return err
			}
		}
	}
	return nil
}

// suggest decides whether a group of feedback justifies a suggestion.
func (s *Service) suggest(g *group, rules []policy.Rule) (Suggestion, bool) {
	rule, hasRule := findByName(rules, g.ruleName)
	if hasRule && !changeable(rule) {
		return Suggestion{}, false // locked or critical: never the subject of a suggestion
	}
	repo := ""
	if len(g.repositories) == 1 {
		for r := range g.repositories {
			repo = r
		}
	}
	e := g.evidence
	what := describe(g.source, g.eventType)

	var sug Suggestion
	switch {
	case g.class == event.AttentionStandard && e.Negative() >= s.cfg.MinSignals && e.Positive() == 0:
		sug = Suggestion{Source: g.source, EventType: g.eventType, Repository: repo, Evidence: e,
			Summary: fmt.Sprintf("People dismissed or muted %s %d times and never opened or replied to one.", what, e.Negative())}
		switch {
		case hasRule && rule.Then.AttentionClass == event.AttentionStandard && !rule.Then.Adaptive:
			sug.Kind, sug.RuleID, sug.RuleName = KindAdaptive, rule.ID, rule.Name
			sug.Proposed = fmt.Sprintf("Let Attention refine the rule %q, so quiet events like this can move to the digest.", rule.Name)
		case hasRule && rule.Then.AttentionClass == event.AttentionStandard:
			sug.Kind, sug.RuleID, sug.RuleName = KindDemote, rule.ID, rule.Name
			sug.Proposed = fmt.Sprintf("Change the rule %q from standard to background.", rule.Name)
		default:
			sug.Kind = KindOverride
			sug.Proposed = fmt.Sprintf("Add a \"never notify\" override for %s%s.", what, inRepo(repo))
		}
	case g.class == event.AttentionBackground && e.Always >= s.cfg.MinAlways && e.Negative() == 0:
		sug = Suggestion{Source: g.source, EventType: g.eventType, Repository: repo, Evidence: e,
			Summary: fmt.Sprintf("People asked to always be told about %s %d times.", what, e.Always)}
		if hasRule && rule.Then.AttentionClass == event.AttentionBackground {
			sug.Kind, sug.RuleID, sug.RuleName = KindPromote, rule.ID, rule.Name
			sug.Proposed = fmt.Sprintf("Change the rule %q from background to standard.", rule.Name)
		} else {
			sug.Kind = KindOverride
			sug.Proposed = fmt.Sprintf("Add an \"always notify\" override for %s%s.", what, inRepo(repo))
		}
	default:
		return Suggestion{}, false
	}
	sug.ID = suggestionID(sug)
	sug.Status = StatusPending
	return sug, true
}

// store saves a new suggestion, refreshes the evidence of a pending one, and
// leaves an applied or dismissed one alone.
func (s *Service) store(ctx context.Context, sug Suggestion) error {
	now := s.now().UTC()
	existing, err := s.repo.Get(ctx, sug.ID)
	switch {
	case err == nil && existing.Status != StatusPending:
		return nil
	case err == nil:
		sug.CreatedAt = existing.CreatedAt
	case !errors.Is(err, ErrNotFound):
		return fmt.Errorf("read suggestion: %w", err)
	default:
		sug.CreatedAt = now
	}
	sug.UpdatedAt = now
	if err := s.repo.Save(ctx, sug); err != nil {
		return fmt.Errorf("save suggestion: %w", err)
	}
	return nil
}

// Apply makes the change a pending suggestion proposes and marks it applied.
// It re-checks the guardrail against the rule as it is now.
func (s *Service) Apply(ctx context.Context, id, actor string) (Suggestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sug, err := s.pending(ctx, id)
	if err != nil {
		return Suggestion{}, err
	}
	switch sug.Kind {
	case KindAdaptive, KindDemote, KindPromote:
		if err := s.changeRule(ctx, sug); err != nil {
			return Suggestion{}, err
		}
	case KindOverride:
		mode := overrides.ModeNever
		if sug.Evidence.Always > 0 && sug.Evidence.Negative() == 0 {
			mode = overrides.ModeAlways
		}
		if _, err := s.overrides.Upsert(ctx, overrides.Override{
			Source: sug.Source, EventType: sug.EventType, Repository: sug.Repository, Mode: mode,
			CreatedBy: actor, Note: "from a learning suggestion",
		}); err != nil {
			return Suggestion{}, fmt.Errorf("create override: %w", err)
		}
	default:
		return Suggestion{}, fmt.Errorf("unknown suggestion kind %q", sug.Kind)
	}
	return s.resolve(ctx, sug, StatusApplied, actor)
}

// Dismiss marks a pending suggestion as not wanted; it is not made again.
func (s *Service) Dismiss(ctx context.Context, id, actor string) (Suggestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sug, err := s.pending(ctx, id)
	if err != nil {
		return Suggestion{}, err
	}
	return s.resolve(ctx, sug, StatusDismissed, actor)
}

func (s *Service) pending(ctx context.Context, id string) (Suggestion, error) {
	sug, err := s.repo.Get(ctx, id)
	if err != nil {
		return Suggestion{}, err
	}
	if sug.Status != StatusPending {
		return Suggestion{}, ErrNotPending
	}
	return sug, nil
}

func (s *Service) resolve(ctx context.Context, sug Suggestion, status, actor string) (Suggestion, error) {
	now := s.now().UTC()
	sug.Status, sug.ResolvedBy, sug.ResolvedAt, sug.UpdatedAt = status, actor, &now, now
	if err := s.repo.Save(ctx, sug); err != nil {
		return Suggestion{}, fmt.Errorf("save suggestion: %w", err)
	}
	return sug, nil
}

func (s *Service) changeRule(ctx context.Context, sug Suggestion) error {
	rules, err := s.rules.List(ctx)
	if err != nil {
		return fmt.Errorf("read rules: %w", err)
	}
	var rule policy.Rule
	found := false
	for _, r := range rules {
		if r.ID == sug.RuleID {
			rule, found = r, true
		}
	}
	if !found {
		return ErrRuleGone
	}
	if !changeable(rule) {
		return ErrGuardrail
	}
	switch sug.Kind {
	case KindAdaptive:
		rule.Then.Adaptive = true
	case KindDemote:
		rule.Then.AttentionClass = event.AttentionBackground
	case KindPromote:
		rule.Then.AttentionClass = event.AttentionStandard
	}
	if rule.Then.AttentionClass == event.AttentionCritical {
		return ErrGuardrail // belt and braces: no path here may produce a critical rule
	}
	if _, err := s.rules.Update(ctx, rule.ID, rule); err != nil {
		return fmt.Errorf("update rule: %w", err)
	}
	return nil
}

// changeable is the guardrail: a rule that is locked or critical, or that
// protects critical routing, can never be the subject of a suggestion.
func changeable(r policy.Rule) bool {
	return !r.Locked && !r.Disabled && r.Then.AttentionClass != event.AttentionCritical
}

func findByName(rules []policy.Rule, name string) (policy.Rule, bool) {
	if name == "" {
		return policy.Rule{}, false
	}
	for _, r := range rules {
		if r.Name == name {
			return r, true
		}
	}
	return policy.Rule{}, false
}

func suggestionID(s Suggestion) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{s.Kind, s.Source, s.EventType, s.Repository, s.RuleName}, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func describe(source, eventType string) string {
	switch {
	case source != "" && eventType != "":
		return source + " " + eventType + " notifications"
	case source != "":
		return source + " notifications"
	}
	return "these notifications"
}

func inRepo(repo string) string {
	if repo == "" {
		return ""
	}
	return " in " + repo
}

func scopeKey(source, eventType, rule string) string {
	return strings.Join([]string{source, eventType, rule}, "\x00")
}
