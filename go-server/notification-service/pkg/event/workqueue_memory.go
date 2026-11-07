package event

import (
	"context"
	"time"
)

// workState is the in-memory counterpart of the attention_* columns.
type workState struct {
	state      string
	leaseUntil time.Time
	attempts   int
}

func (r *InMemoryRepository) markPending(id string) {
	if r.work == nil {
		r.work = map[string]*workState{}
	}
	r.work[id] = &workState{state: AttentionStatePending}
}

// ClaimPending implements WorkQueue.
func (r *InMemoryRepository) ClaimPending(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Event
	for _, e := range r.events {
		if len(out) >= limit {
			break
		}
		w := r.work[e.ID]
		if w == nil || !claimable(e) {
			continue
		}
		if w.state == AttentionStateDone || (w.state == AttentionStateProcessing && w.leaseUntil.After(now)) {
			continue
		}
		w.state, w.leaseUntil = AttentionStateProcessing, now.Add(lease)
		w.attempts++
		e.DecisionState = cloneState(e.DecisionState)
		out = append(out, e)
	}
	return out, nil
}

// ListDelayed implements WorkQueue.
func (r *InMemoryRepository) ListDelayed(ctx context.Context, limit int) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Event
	for _, e := range r.events {
		if len(out) >= limit {
			break
		}
		if e.AttentionClass != AttentionCritical && e.DecisionState != nil && e.DecisionState.Status == DecisionDelayed {
			e.DecisionState = cloneState(e.DecisionState)
			out = append(out, e)
		}
	}
	return out, nil
}

// UpdateDecision implements WorkQueue.
func (r *InMemoryRepository) UpdateDecision(ctx context.Context, id, class string, state DecisionState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.events {
		if r.events[i].ID != id {
			continue
		}
		if r.events[i].AttentionClass == AttentionCritical || class == AttentionCritical {
			return ErrCriticalImmutable
		}
		r.events[i].AttentionClass = class
		r.events[i].DecisionState = cloneState(&state)
		if w := r.work[id]; w != nil {
			w.state = AttentionStateDone
		}
		return nil
	}
	return nil
}
