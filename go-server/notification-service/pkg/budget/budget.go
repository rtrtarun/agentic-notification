// Package budget caps how many times people are interrupted.
//
// "Notifications are cheap; human attention is expensive." A budget counts the
// standard notifications sent to a scope in a rolling window. When it is used
// up, further standard events are held for the next digest instead of
// interrupting again: held, never dropped.
//
// A budget applies to standard events only. It is never consulted for a
// critical event, which is delivered at once whatever has been spent.
package budget

import (
	"context"
	"sync"
	"time"
)

const (
	// DefaultPerHour is the number of standard interruptions allowed per scope per window.
	DefaultPerHour = 10
	// DefaultWindow is the rolling window the cap applies to.
	DefaultWindow = time.Hour
	// ScopeWorkspace is the scope used until per-person budgets arrive.
	ScopeWorkspace = "workspace"
)

// Result is the outcome of asking for one interruption.
type Result struct {
	// Allowed is true when the interruption fits in the budget and has been counted.
	Allowed bool
	// Used and Limit describe the window after this request.
	Used, Limit int
	// ResetAt is when the oldest counted interruption leaves the window, freeing a slot.
	// It is zero when the request was allowed.
	ResetAt time.Time
}

// Budget decides whether one more interruption fits. Implementations are safe for concurrent use.
type Budget interface {
	// Take counts one interruption for scope if the budget allows it. On an error the
	// caller must fail open: an unavailable store must never suppress a notification.
	Take(ctx context.Context, scope string) (Result, error)
}

// MemoryBudget keeps the window in process memory. It is correct for one replica;
// use RedisBudget when several replicas must share one cap.
type MemoryBudget struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu     sync.Mutex
	counts map[string][]time.Time
}

// NewMemoryBudget returns a budget of limit interruptions per window. Non-positive values use the defaults.
func NewMemoryBudget(limit int, window time.Duration) *MemoryBudget {
	if limit <= 0 {
		limit = DefaultPerHour
	}
	if window <= 0 {
		window = DefaultWindow
	}
	return &MemoryBudget{limit: limit, window: window, now: time.Now, counts: map[string][]time.Time{}}
}

// WithClock replaces the clock; tests use it.
func (m *MemoryBudget) WithClock(now func() time.Time) *MemoryBudget {
	m.now = now
	return m
}

// Take implements Budget.
func (m *MemoryBudget) Take(ctx context.Context, scope string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	cutoff := now.Add(-m.window)
	kept := m.counts[scope][:0]
	for _, t := range m.counts[scope] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= m.limit {
		m.counts[scope] = kept
		return Result{Allowed: false, Used: len(kept), Limit: m.limit, ResetAt: kept[0].Add(m.window)}, nil
	}
	kept = append(kept, now)
	m.counts[scope] = kept
	return Result{Allowed: true, Used: len(kept), Limit: m.limit}, nil
}
