package learning

import (
	"context"
	"sort"
	"sync"
)

// MemoryRepository is the in-process Repository used without a database and in tests.
type MemoryRepository struct {
	mu   sync.Mutex
	list map[string]Suggestion
}

// NewMemoryRepository returns an empty repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{list: map[string]Suggestion{}}
}

// Save implements Repository.
func (m *MemoryRepository) Save(_ context.Context, s Suggestion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.list[s.ID] = s
	return nil
}

// Get implements Repository.
func (m *MemoryRepository) Get(_ context.Context, id string) (Suggestion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.list[id]
	if !ok {
		return Suggestion{}, ErrNotFound
	}
	return s, nil
}

// List implements Repository.
func (m *MemoryRepository) List(_ context.Context, status string) ([]Suggestion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Suggestion, 0, len(m.list))
	for _, s := range m.list {
		if status == "" || s.Status == status {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].UpdatedAt.Equal(out[b].UpdatedAt) {
			return out[a].UpdatedAt.After(out[b].UpdatedAt)
		}
		return out[a].ID < out[b].ID
	})
	return out, nil
}
