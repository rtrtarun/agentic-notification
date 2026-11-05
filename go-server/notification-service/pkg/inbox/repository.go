package inbox

import (
	"context"
	"sort"
	"sync"
)

// Repository stores per-event state and the trail of user actions.
type Repository interface {
	// All returns every stored state, keyed by event ID.
	All(ctx context.Context) (map[string]State, error)
	// Get returns the state for an event; an untouched event yields a zero State with EventID set.
	Get(ctx context.Context, eventID string) (State, error)
	Save(ctx context.Context, s State) error
	AppendTrail(ctx context.Context, eventID string, entry TrailEntry) error
	Trail(ctx context.Context, eventID string) ([]TrailEntry, error)
	// RecentTrail returns the newest user actions across all events, newest first.
	RecentTrail(ctx context.Context, limit int) ([]TrailRow, error)
}

// MemoryRepository keeps state in process. Used without a database and in tests.
type MemoryRepository struct {
	mu     sync.Mutex
	states map[string]State
	trails map[string][]TrailEntry
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{states: map[string]State{}, trails: map[string][]TrailEntry{}}
}

func (r *MemoryRepository) All(ctx context.Context) (map[string]State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]State, len(r.states))
	for k, v := range r.states {
		out[k] = v
	}
	return out, nil
}

func (r *MemoryRepository) Get(ctx context.Context, id string) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.states[id]; ok {
		return s, nil
	}
	return State{EventID: id}, nil
}

func (r *MemoryRepository) Save(ctx context.Context, s State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[s.EventID] = s
	return nil
}

func (r *MemoryRepository) AppendTrail(ctx context.Context, id string, e TrailEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trails[id] = append(r.trails[id], e)
	return nil
}

func (r *MemoryRepository) Trail(ctx context.Context, id string) ([]TrailEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TrailEntry(nil), r.trails[id]...), nil
}

func (r *MemoryRepository) RecentTrail(ctx context.Context, limit int) ([]TrailRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []TrailRow
	for id, entries := range r.trails {
		for _, e := range entries {
			rows = append(rows, TrailRow{EventID: id, TrailEntry: e})
		}
	}
	sortTrailRows(rows)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func sortTrailRows(rows []TrailRow) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].At.After(rows[j].At) })
}
