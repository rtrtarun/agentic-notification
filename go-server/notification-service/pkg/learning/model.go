// Package learning turns stored feedback into suggestions a person can review.
//
// It is the bridge from Layer 4 (feedback) back to Layers 1 and 2: it reads what
// people dismissed, muted, opened, replied to and always-notified, and proposes
// small, reversible changes to rules or overrides. It never changes anything on
// its own; an admin applies or dismisses each suggestion, and applying is audited.
//
// Guardrail, enforced twice (when a suggestion is made and again when it is
// applied): a critical event, a critical rule or a locked rule is never the
// subject of a suggestion and can never be changed by one.
package learning

import (
	"context"
	"errors"
	"time"
)

// Kinds of suggestion.
const (
	// KindAdaptive lets Attention Intelligence refine a standard rule (sets adaptive: true).
	KindAdaptive = "adaptive"
	// KindDemote moves a standard rule's events to background.
	KindDemote = "demote"
	// KindPromote moves a background rule's events to standard.
	KindPromote = "promote"
	// KindOverride adds a scoped "never notify" or "always notify" override.
	KindOverride = "override"
)

// Statuses of a suggestion.
const (
	StatusPending   = "pending"
	StatusApplied   = "applied"
	StatusDismissed = "dismissed"
)

// Defaults for Config.
const (
	DefaultMinSignals = 5
	DefaultMinAlways  = 2
	DefaultLookback   = 30 * 24 * time.Hour
	// maxFeedbackRead bounds one analysis.
	maxFeedbackRead = 5000
)

// Errors the HTTP layer maps to status codes.
var (
	ErrNotFound   = errors.New("suggestion not found")
	ErrNotPending = errors.New("suggestion is no longer pending")
	// ErrGuardrail is returned when applying would touch a critical or locked rule.
	ErrGuardrail = errors.New("critical and locked rules can never be changed by a suggestion")
	ErrRuleGone  = errors.New("the rule this suggestion is about no longer exists")
)

// Evidence counts the signals behind a suggestion.
type Evidence struct {
	Dismissed int `json:"dismissed"`
	Never     int `json:"never"`
	Opened    int `json:"opened"`
	Reply     int `json:"reply"`
	Always    int `json:"always"`
}

// Negative is the number of signals that say "this was not worth it".
func (e Evidence) Negative() int { return e.Dismissed + e.Never }

// Positive is the number of signals that say "this mattered".
func (e Evidence) Positive() int { return e.Opened + e.Reply + e.Always }

// Suggestion is one reviewable proposal.
type Suggestion struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Source     string `json:"source"`
	EventType  string `json:"eventType"`
	Repository string `json:"repository,omitempty"`
	// RuleID and RuleName name the rule the suggestion would change; empty for an override.
	RuleID   string `json:"ruleId,omitempty"`
	RuleName string `json:"ruleName,omitempty"`
	// Summary says what people did; Proposed says what would change.
	Summary    string     `json:"summary"`
	Proposed   string     `json:"proposed"`
	Evidence   Evidence   `json:"evidence"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ResolvedBy string     `json:"resolvedBy,omitempty"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// Repository keeps suggestions. Implementations: MemoryRepository and PostgresRepository.
type Repository interface {
	// Save inserts or replaces a suggestion by ID.
	Save(ctx context.Context, s Suggestion) error
	// Get returns ErrNotFound when there is none.
	Get(ctx context.Context, id string) (Suggestion, error)
	// List returns suggestions with the status (all when empty), newest update first.
	List(ctx context.Context, status string) ([]Suggestion, error)
}
