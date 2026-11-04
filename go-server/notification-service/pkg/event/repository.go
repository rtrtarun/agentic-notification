package event

import (
	"context"
	"sync"
	"time"
)

// Repository persists events. Save is idempotent on Event.ID.
type Repository interface {
	Save(ctx context.Context, event Event) error
	// Exists reports whether an event with this ID was already saved.
	Exists(ctx context.Context, id string) (bool, error)
	// List returns the most recent events, oldest first, capped at DefaultListLimit.
	List(ctx context.Context) ([]Event, error)
}

// Purger deletes events older than a cutoff, enforcing the retention policy.
type Purger interface {
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// DefaultListLimit caps List so one call cannot load an unbounded table.
const DefaultListLimit = 1000

type InMemoryRepository struct {
	mu     sync.Mutex
	events []Event
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{}
}

func (r *InMemoryRepository) Save(ctx context.Context, evt Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.events {
		if existing.ID == evt.ID {
			return nil
		}
	}
	if evt.CreatedAt.IsZero() {
		evt.CreatedAt = time.Now().UTC()
	}
	r.events = append(r.events, evt)
	return nil
}

func (r *InMemoryRepository) Exists(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.events {
		if existing.ID == id {
			return true, nil
		}
	}
	return false, nil
}

func (r *InMemoryRepository) List(ctx context.Context) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	events := r.events
	if len(events) > DefaultListLimit {
		events = events[len(events)-DefaultListLimit:]
	}
	out := make([]Event, len(events))
	copy(out, events)
	return out, nil
}
