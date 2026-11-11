package learning

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryAdjustments is the in-process AdjustmentRepository used without a database and in tests.
type MemoryAdjustments struct {
	mu   sync.Mutex
	list map[string]Adjustment
}

// NewMemoryAdjustments returns an empty repository.
func NewMemoryAdjustments() *MemoryAdjustments {
	return &MemoryAdjustments{list: map[string]Adjustment{}}
}

// Save implements AdjustmentRepository.
func (m *MemoryAdjustments) Save(_ context.Context, a Adjustment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.list[a.ID] = a
	return nil
}

// Get implements AdjustmentRepository.
func (m *MemoryAdjustments) Get(_ context.Context, id string) (Adjustment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.list[id]
	if !ok {
		return Adjustment{}, ErrAdjustmentNotFound
	}
	return a, nil
}

// List implements AdjustmentRepository.
func (m *MemoryAdjustments) List(_ context.Context, status string) ([]Adjustment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Adjustment, 0, len(m.list))
	for _, a := range m.list {
		if status == "" || a.Status == status {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(x, y int) bool {
		if !out[x].UpdatedAt.Equal(out[y].UpdatedAt) {
			return out[x].UpdatedAt.After(out[y].UpdatedAt)
		}
		return out[x].ID < out[y].ID
	})
	return out, nil
}

// MaxVersion implements AdjustmentRepository.
func (m *MemoryAdjustments) MaxVersion(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	max := 0
	for _, a := range m.list {
		if a.Version > max {
			max = a.Version
		}
	}
	return max, nil
}

// MemoryRuns is the in-process RunRepository used without a database and in tests.
type MemoryRuns struct {
	mu   sync.Mutex
	list []Run // oldest first
}

// NewMemoryRuns returns an empty repository.
func NewMemoryRuns() *MemoryRuns { return &MemoryRuns{} }

// Start implements RunRepository.
func (m *MemoryRuns) Start(_ context.Context, r Run, staleBefore time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.list {
		if m.list[i].Status != RunRunning {
			continue
		}
		if m.list[i].StartedAt.Before(staleBefore) {
			now := r.StartedAt
			m.list[i].Status, m.list[i].FinishedAt, m.list[i].Error = RunFailed, &now, abandonedRun
			continue
		}
		return ErrRunInProgress
	}
	m.list = append(m.list, r)
	return nil
}

// abandonedRun is the error recorded on a run that never finished.
const abandonedRun = "abandoned: the run did not finish"

// Finish implements RunRepository.
func (m *MemoryRuns) Finish(_ context.Context, r Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.list {
		if m.list[i].ID == r.ID {
			m.list[i] = r
			return nil
		}
	}
	return nil
}

// List implements RunRepository.
func (m *MemoryRuns) List(_ context.Context, limit int) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Run, 0, len(m.list))
	for i := len(m.list) - 1; i >= 0; i-- {
		out = append(out, m.list[i])
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].StartedAt.After(out[b].StartedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
