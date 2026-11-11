package attention

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
)

// MaxTuningDelta bounds any score offset the heuristic will apply, whatever a
// store says. Configuration allows less; this is the last line of defence.
const MaxTuningDelta = 50

// DefaultTuningMaxAge is how long a snapshot may go without a refresh before the
// heuristic stops using it and scores events exactly as it does without tuning.
const DefaultTuningMaxAge = 5 * time.Minute

// Adjustment is one approved score offset, as the heuristic sees it.
type Adjustment struct {
	ID         string
	Source     string
	EventType  string
	Repository string // empty means every repository
	Delta      int
	// Summary is the plain-language evidence, shown in the event's reasons.
	Summary string
}

// Tuning is an in-memory snapshot of the approved adjustments. The live path
// only reads it: a lookup in a map, with no I/O. If the snapshot was never
// loaded, or has not been refreshed within its maximum age, it answers "no
// adjustment" and events are scored as they would be without learning.
//
// A nil *Tuning is valid and always answers "no adjustment".
type Tuning struct {
	mu       sync.RWMutex
	version  int
	byScope  map[string]Adjustment
	loadedAt time.Time
	maxAge   time.Duration
	now      func() time.Time
}

// NewTuning returns an empty snapshot. A non-positive maxAge uses DefaultTuningMaxAge.
func NewTuning(maxAge time.Duration) *Tuning {
	if maxAge <= 0 {
		maxAge = DefaultTuningMaxAge
	}
	return &Tuning{maxAge: maxAge, now: time.Now}
}

// WithClock replaces the clock; tests use it.
func (t *Tuning) WithClock(now func() time.Time) *Tuning {
	t.now = now
	return t
}

// Set replaces the snapshot with the active adjustments at the given set version.
// Offsets are clamped to ±MaxTuningDelta.
func (t *Tuning) Set(version int, adjustments []Adjustment) {
	if t == nil {
		return
	}
	m := make(map[string]Adjustment, len(adjustments))
	for _, a := range adjustments {
		switch {
		case a.Delta > MaxTuningDelta:
			a.Delta = MaxTuningDelta
		case a.Delta < -MaxTuningDelta:
			a.Delta = -MaxTuningDelta
		}
		if a.Delta == 0 {
			continue
		}
		m[tuningKey(a.Source, a.EventType, a.Repository)] = a
	}
	t.mu.Lock()
	t.version, t.byScope, t.loadedAt = version, m, t.now()
	t.mu.Unlock()
}

// Version is the version of the set in force, or 0 when none is.
func (t *Tuning) Version() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.fresh() {
		return t.version
	}
	return 0
}

// Len is how many adjustments are in force.
func (t *Tuning) Len() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.fresh() {
		return 0
	}
	return len(t.byScope)
}

// lookup returns the adjustment for an event, preferring one for its repository
// over one for the whole event type.
func (t *Tuning) lookup(evt event.Event) (Adjustment, int, bool) {
	if t == nil {
		return Adjustment{}, 0, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.fresh() || len(t.byScope) == 0 {
		return Adjustment{}, 0, false
	}
	if evt.Repository != "" {
		if a, ok := t.byScope[tuningKey(evt.Source, evt.EventType, evt.Repository)]; ok {
			return a, t.version, true
		}
	}
	a, ok := t.byScope[tuningKey(evt.Source, evt.EventType, "")]
	return a, t.version, ok
}

// fresh reports whether the snapshot was loaded recently enough to use. Callers hold the lock.
func (t *Tuning) fresh() bool {
	return !t.loadedAt.IsZero() && t.now().Sub(t.loadedAt) <= t.maxAge
}

func tuningKey(source, eventType, repository string) string {
	return strings.ToLower(source) + "\x1f" + strings.ToLower(eventType) + "\x1f" + strings.ToLower(repository)
}

// TunedModel names the heuristic as tuned by the approved set with this version.
func TunedModel(version int) string { return fmt.Sprintf("%s+tuned-%d", ModelHeuristic, version) }
