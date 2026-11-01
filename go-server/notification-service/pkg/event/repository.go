package event

import (
	"context"
	"sync"
)

type Repository interface {
	Save(ctx context.Context, event Event) error
	List(ctx context.Context) ([]Event, error)
}

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
	r.events = append(r.events, evt)
	return nil
}

func (r *InMemoryRepository) List(ctx context.Context) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out, nil
}
