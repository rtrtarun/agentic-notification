// Package rules lets people manage the Layer 1 policy rules while the service
// runs: list, create, edit, disable, delete, reorder and dry-run them. It owns
// persistence and validation; evaluation stays in package policy, and AI is not
// involved anywhere here.
//
// Locked rules protect critical routing. They cannot be edited, disabled or
// removed, and they always run before unlocked rules, so a broad rule added
// later can never capture a security event ahead of them.
package rules

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/policy"
)

// Errors the HTTP layer maps to status codes.
var (
	ErrNotFound = errors.New("rule not found")
	ErrLocked   = errors.New("this rule is locked: it protects critical routing")
	ErrInvalid  = errors.New("invalid rule")
)

// Limits on what a rule may contain.
const (
	MaxRules       = 100
	maxName        = 80
	maxDescription = 300
	maxReason      = 200
	maxPattern     = 100
)

// Store keeps the ordered rule list.
type Store interface {
	List(ctx context.Context) ([]policy.Rule, error)
	// Replace stores exactly this list, in this order.
	Replace(ctx context.Context, rules []policy.Rule) error
}

// Applier receives the rule list after every change. *policy.Engine satisfies it.
type Applier interface {
	SetRules(rules []policy.Rule)
}

// Service manages the rule list.
type Service struct {
	store   Store
	applier Applier
	newID   func() string
	mu      sync.Mutex // serialises read-modify-write cycles
}

// NewService loads the stored rules, seeds them from seed when the store is
// empty, adds any locked seed rule that is missing, and hands the result to the engine.
func NewService(ctx context.Context, store Store, applier Applier, seed []policy.Rule) (*Service, error) {
	s := &Service{store: store, applier: applier, newID: randomID}
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, err := store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}
	merged := mergeSeed(stored, seed, s.newID)
	if len(merged) != len(stored) || !sameOrder(merged, stored) {
		if err := store.Replace(ctx, merged); err != nil {
			return nil, fmt.Errorf("seed rules: %w", err)
		}
	}
	applier.SetRules(merged)
	return s, nil
}

// WithIDGenerator replaces the ID source; for tests.
func (s *Service) WithIDGenerator(f func() string) *Service {
	s.newID = f
	return s
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("rules: no randomness available: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// mergeSeed returns the stored rules, or the seed when nothing is stored, with
// IDs assigned, locked rules first, and any missing locked seed rule added.
func mergeSeed(stored, seed []policy.Rule, newID func() string) []policy.Rule {
	list := stored
	if len(list) == 0 {
		list = append([]policy.Rule(nil), seed...)
	} else {
		have := map[string]bool{}
		for _, r := range stored {
			have[strings.ToLower(r.Name)] = true
		}
		for _, r := range seed {
			if r.Locked && !have[strings.ToLower(r.Name)] {
				list = append(list, r)
			}
		}
	}
	used := map[string]bool{}
	for i := range list {
		if list[i].ID == "" || used[list[i].ID] {
			list[i].ID = newID()
		}
		used[list[i].ID] = true
	}
	return lockedFirst(list)
}

// lockedFirst keeps relative order within each group.
func lockedFirst(list []policy.Rule) []policy.Rule {
	out := make([]policy.Rule, 0, len(list))
	for _, r := range list {
		if r.Locked {
			out = append(out, r)
		}
	}
	for _, r := range list {
		if !r.Locked {
			out = append(out, r)
		}
	}
	return out
}

func sameOrder(a, b []policy.Rule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

// List returns the rules in evaluation order.
func (s *Service) List(ctx context.Context) ([]policy.Rule, error) {
	return s.store.List(ctx)
}

// Create appends an unlocked rule after the existing ones.
func (s *Service) Create(ctx context.Context, in policy.Rule) (policy.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.List(ctx)
	if err != nil {
		return policy.Rule{}, err
	}
	if len(list) >= MaxRules {
		return policy.Rule{}, fmt.Errorf("%w: at most %d rules", ErrInvalid, MaxRules)
	}
	rule, err := normalise(in, list, "")
	if err != nil {
		return policy.Rule{}, err
	}
	rule.ID = s.newID()
	if err := s.save(ctx, append(list, rule)); err != nil {
		return policy.Rule{}, err
	}
	return rule, nil
}

// Update replaces an unlocked rule's content. The ID and position stay.
func (s *Service) Update(ctx context.Context, id string, in policy.Rule) (policy.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.List(ctx)
	if err != nil {
		return policy.Rule{}, err
	}
	idx := indexOf(list, id)
	if idx < 0 {
		return policy.Rule{}, ErrNotFound
	}
	if list[idx].Locked {
		return policy.Rule{}, ErrLocked
	}
	rule, err := normalise(in, list, id)
	if err != nil {
		return policy.Rule{}, err
	}
	rule.ID = id
	list[idx] = rule
	if err := s.save(ctx, list); err != nil {
		return policy.Rule{}, err
	}
	return rule, nil
}

// Delete removes an unlocked rule.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	idx := indexOf(list, id)
	if idx < 0 {
		return ErrNotFound
	}
	if list[idx].Locked {
		return ErrLocked
	}
	return s.save(ctx, append(list[:idx], list[idx+1:]...))
}

// Reorder sets the evaluation order. ids must name every rule exactly once and
// every locked rule must come before every unlocked one.
func (s *Service) Reorder(ctx context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	if len(ids) != len(list) {
		return fmt.Errorf("%w: send every rule id exactly once", ErrInvalid)
	}
	byID := make(map[string]policy.Rule, len(list))
	for _, r := range list {
		byID[r.ID] = r
	}
	out := make([]policy.Rule, 0, len(list))
	seenUnlocked := false
	for _, id := range ids {
		r, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: unknown or repeated rule id", ErrInvalid)
		}
		delete(byID, id)
		if r.Locked && seenUnlocked {
			return fmt.Errorf("%w: locked rules must stay above all other rules", ErrInvalid)
		}
		seenUnlocked = seenUnlocked || !r.Locked
		out = append(out, r)
	}
	return s.save(ctx, out)
}

func (s *Service) save(ctx context.Context, list []policy.Rule) error {
	if err := s.store.Replace(ctx, list); err != nil {
		return err
	}
	s.applier.SetRules(list)
	return nil
}

func indexOf(list []policy.Rule, id string) int {
	for i, r := range list {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// normalise validates a rule from the API. ownID is the ID being edited, so a
// rule may keep its own name.
func normalise(in policy.Rule, existing []policy.Rule, ownID string) (policy.Rule, error) {
	r := in
	r.Locked = false // only seeds and the rules file can lock a rule
	r.Name = strings.TrimSpace(r.Name)
	r.Description = strings.TrimSpace(r.Description)
	r.Then.Reason = strings.TrimSpace(r.Then.Reason)
	r.When.Source = strings.TrimSpace(r.When.Source)
	r.When.EventType = strings.TrimSpace(r.When.EventType)
	r.When.Repository = strings.TrimSpace(r.When.Repository)

	if r.Name == "" || utf8.RuneCountInString(r.Name) > maxName {
		return r, fmt.Errorf("%w: name is required and at most %d characters", ErrInvalid, maxName)
	}
	for _, other := range existing {
		if other.ID != ownID && strings.EqualFold(other.Name, r.Name) {
			return r, fmt.Errorf("%w: a rule named %q already exists", ErrInvalid, r.Name)
		}
	}
	if utf8.RuneCountInString(r.Description) > maxDescription {
		return r, fmt.Errorf("%w: description is at most %d characters", ErrInvalid, maxDescription)
	}
	switch r.Then.AttentionClass {
	case event.AttentionCritical, event.AttentionStandard, event.AttentionBackground:
	default:
		return r, fmt.Errorf("%w: attentionClass must be critical, standard or background", ErrInvalid)
	}
	if r.Then.Adaptive && r.Then.AttentionClass == event.AttentionCritical {
		return r, fmt.Errorf("%w: a critical rule cannot be adaptive; critical events never reach Attention Intelligence", ErrInvalid)
	}
	if r.Then.Reason == "" {
		r.Then.Reason = "matched rule " + r.Name
	}
	if utf8.RuneCountInString(r.Then.Reason) > maxReason {
		return r, fmt.Errorf("%w: reason is at most %d characters", ErrInvalid, maxReason)
	}
	for field, pattern := range map[string]string{"source": r.When.Source, "eventType": r.When.EventType, "repository": r.When.Repository} {
		if utf8.RuneCountInString(pattern) > maxPattern {
			return r, fmt.Errorf("%w: %s is at most %d characters", ErrInvalid, field, maxPattern)
		}
		if err := policy.ValidatePattern(pattern); err != nil {
			return r, fmt.Errorf("%w: %s is not a valid pattern", ErrInvalid, field)
		}
	}
	return r, nil
}

// Sample is an event described by the three fields rules look at.
type Sample struct {
	Source     string `json:"source"`
	EventType  string `json:"eventType"`
	Repository string `json:"repository"`
}

// Step is one rule's part in a dry run.
type Step struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Disabled bool   `json:"disabled,omitempty"`
	Matched  bool   `json:"matched"`
	// Winner is the rule that decided the outcome: the first enabled match.
	Winner bool `json:"winner,omitempty"`
}

// Result is what a dry run decided.
type Result struct {
	Matched        bool   `json:"matched"`
	AttentionClass string `json:"attentionClass"`
	Reason         string `json:"reason"`
	MatchedRule    string `json:"matchedRule,omitempty"`
	// Bypassed is true for critical outcomes: they never reach the AI layer.
	Bypassed bool   `json:"aiBypassed"`
	Steps    []Step `json:"steps"`
}

// Test dry-runs the sample through the current rules. Nothing is stored or delivered.
func (s *Service) Test(ctx context.Context, in Sample) (Result, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return Result{}, err
	}
	return Evaluate(list, in), nil
}

// Evaluate is the pure dry run behind Test. An unmatched event falls back to
// standard, exactly as the pipeline does.
func Evaluate(list []policy.Rule, in Sample) Result {
	evt := event.Event{Source: in.Source, EventType: in.EventType, Repository: in.Repository}
	res := Result{AttentionClass: event.AttentionStandard, Reason: "no matching rule, default standard", Steps: make([]Step, 0, len(list))}
	for _, r := range list {
		step := Step{ID: r.ID, Name: r.Name, Disabled: r.Disabled}
		step.Matched = !r.Disabled && policy.Matches(r.When, evt)
		if step.Matched && !res.Matched {
			step.Winner = true
			res.Matched = true
			res.AttentionClass, res.Reason, res.MatchedRule = r.Then.AttentionClass, r.Then.Reason, r.Name
		}
		res.Steps = append(res.Steps, step)
	}
	res.Bypassed = res.AttentionClass == event.AttentionCritical
	return res
}
