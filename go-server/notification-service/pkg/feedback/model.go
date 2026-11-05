// Package feedback is Layer 4: it records what people do with a notification
// (opened, dismissed, undo, always notify, never notify, replies) as stored,
// attributed signals, and turns "always" and "never" into visible overrides.
// Feedback never changes a critical event.
package feedback

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Actions.
const (
	ActionOpened    = "opened"
	ActionDismissed = "dismissed"
	ActionUndo      = "undo"
	ActionAlways    = "always"
	ActionNever     = "never"
	ActionReply     = "reply"
)

// Scopes say how widely "always" or "never" applies.
const (
	ScopeType       = "type"       // this source and event type (the default)
	ScopeRepository = "repository" // everything from this repository
	ScopeSource     = "source"     // everything from this source
)

// MaxReasonLength bounds a reply or note.
const MaxReasonLength = 1000

// Errors the HTTP layer maps to status codes.
var (
	ErrInvalid       = errors.New("invalid feedback")
	ErrEventNotFound = errors.New("event not found")
)

// EventFeedback is one signal. Source, EventType and Repository are copied from
// the event at the time, so learning can read them without joining.
type EventFeedback struct {
	ID         string    `json:"id"`
	EventID    string    `json:"eventId"`
	UserID     string    `json:"userId,omitempty"`
	Action     string    `json:"action"`
	Scope      string    `json:"scope,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	OverrideID string    `json:"overrideId,omitempty"`
	Source     string    `json:"source,omitempty"`
	EventType  string    `json:"eventType,omitempty"`
	Repository string    `json:"repository,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Filter narrows List. Zero fields match everything.
type Filter struct {
	EventID string
	UserID  string
	Since   time.Time
	Limit   int
}

// Store keeps feedback. Implementations: MemoryStore and PostgresStore.
type Store interface {
	Add(ctx context.Context, f EventFeedback) error
	// List returns matching feedback, newest first.
	List(ctx context.Context, f Filter) ([]EventFeedback, error)
}

// MaxInMemory caps MemoryStore; the oldest entries are dropped first.
const MaxInMemory = 10_000

// MemoryStore is the in-process Store used without a database and in tests.
type MemoryStore struct {
	mu   sync.Mutex
	list []EventFeedback
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// Add implements Store.
func (m *MemoryStore) Add(_ context.Context, f EventFeedback) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.list) >= MaxInMemory {
		m.list = append(m.list[:0], m.list[1:]...)
	}
	m.list = append(m.list, f)
	return nil
}

// List implements Store.
func (m *MemoryStore) List(_ context.Context, f Filter) ([]EventFeedback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EventFeedback
	for i := len(m.list) - 1; i >= 0; i-- {
		x := m.list[i]
		if (f.EventID != "" && x.EventID != f.EventID) || (f.UserID != "" && x.UserID != f.UserID) || x.CreatedAt.Before(f.Since) {
			continue
		}
		out = append(out, x)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}
