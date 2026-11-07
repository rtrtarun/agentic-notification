package event

import (
	"context"
	"errors"
	"time"
)

// Attention work states, stored per event. An event is "pending" from the moment
// it is saved until the attention worker has decided what to do with it.
const (
	AttentionStatePending    = "pending"
	AttentionStateProcessing = "processing"
	AttentionStateDone       = "done"
)

// ErrCriticalImmutable is returned when something tries to change a critical event's decision.
var ErrCriticalImmutable = errors.New("a critical event's decision can never be changed after delivery")

// WorkQueue is the optional part of a repository the asynchronous attention
// worker needs. InMemoryRepository and PostgresRepository implement it.
//
// A critical event is never part of the work queue: ClaimPending never returns one
// and UpdateDecision refuses to change one, whatever its stored state.
type WorkQueue interface {
	// ClaimPending leases up to limit standard or background events that are queued
	// and waiting for attention, oldest first, and counts the attempt. A lease that
	// has expired makes the event claimable again, so a crashed worker loses nothing.
	ClaimPending(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Event, error)
	// ListDelayed returns up to limit non-critical events held until quiet hours end, oldest first.
	ListDelayed(ctx context.Context, limit int) ([]Event, error)
	// UpdateDecision stores the decision made for a non-critical event and marks its
	// attention work done. It returns ErrCriticalImmutable for a critical event.
	UpdateDecision(ctx context.Context, id, class string, state DecisionState) error
}

// claimable reports whether an event may be handed to the attention worker.
func claimable(e Event) bool {
	return (e.AttentionClass == AttentionStandard || e.AttentionClass == AttentionBackground) &&
		e.DecisionState != nil && e.DecisionState.Status == DecisionQueued
}

// cloneState copies a decision so a stored event never shares memory with a caller.
func cloneState(s *DecisionState) *DecisionState {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}
