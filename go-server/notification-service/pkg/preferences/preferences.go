// Package preferences holds what a person asked for: quiet hours and muted
// sources. It is deterministic Layer 1 policy, with no AI.
//
// Preferences apply only to standard and background events. A critical event is
// never delayed, muted or downgraded, whatever is set here: Decide returns
// no adjustment for it, and a test pins that.
package preferences

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // embed the zone database so quiet hours work in minimal containers

	"github.com/example/notification-service/pkg/event"
)

// ErrInvalid is returned for preferences that cannot be stored. The message is safe to show.
var ErrInvalid = errors.New("invalid preferences")

const (
	maxMuted    = 50
	maxPattern  = 100
	clockLayout = "15:04"
)

// QuietHours is a daily window in which non-critical events are held back.
// The window may cross midnight (22:00 to 07:00).
type QuietHours struct {
	Enabled bool `json:"enabled"`
	// Start and End are 24-hour "HH:MM" clock times in Timezone.
	Start    string `json:"start"`
	End      string `json:"end"`
	Timezone string `json:"timezone"`
}

// Preferences are one workspace's choices.
type Preferences struct {
	QuietHours QuietHours `json:"quietHours"`
	// MutedSources are source names such as "github"; MutedRepositories accept * wildcards.
	MutedSources      []string  `json:"mutedSources"`
	MutedRepositories []string  `json:"mutedRepositories"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// Default returns the preferences before anyone changes them: nothing muted, quiet hours off.
func Default() Preferences {
	return Preferences{
		QuietHours:        QuietHours{Start: "22:00", End: "07:00", Timezone: "UTC"},
		MutedSources:      []string{},
		MutedRepositories: []string{},
	}
}

// Normalise validates and tidies preferences: trims and lower-cases sources,
// drops blanks and duplicates, and checks the clock times, zone and patterns.
func Normalise(p Preferences) (Preferences, error) {
	out := p
	out.QuietHours.Start = strings.TrimSpace(out.QuietHours.Start)
	out.QuietHours.End = strings.TrimSpace(out.QuietHours.End)
	out.QuietHours.Timezone = strings.TrimSpace(out.QuietHours.Timezone)
	if out.QuietHours.Timezone == "" {
		out.QuietHours.Timezone = "UTC"
	}

	qh := out.QuietHours
	if _, err := time.LoadLocation(qh.Timezone); err != nil {
		return p, fmt.Errorf("%w: unknown time zone", ErrInvalid)
	}
	start, err := time.Parse(clockLayout, qh.Start)
	if err != nil {
		return p, fmt.Errorf("%w: start must be a time like 22:00", ErrInvalid)
	}
	end, err := time.Parse(clockLayout, qh.End)
	if err != nil {
		return p, fmt.Errorf("%w: end must be a time like 07:00", ErrInvalid)
	}
	if qh.Enabled && start.Equal(end) {
		return p, fmt.Errorf("%w: quiet hours need different start and end times", ErrInvalid)
	}

	var err2 error
	if out.MutedSources, err2 = tidy(out.MutedSources, true, "source"); err2 != nil {
		return p, err2
	}
	if out.MutedRepositories, err2 = tidy(out.MutedRepositories, false, "repository"); err2 != nil {
		return p, err2
	}
	return out, nil
}

func tidy(list []string, lower bool, what string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range list {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if lower {
			v = strings.ToLower(v)
		}
		if len(v) > maxPattern {
			return nil, fmt.Errorf("%w: a muted %s is at most %d characters", ErrInvalid, what, maxPattern)
		}
		if _, err := path.Match(v, ""); err != nil {
			return nil, fmt.Errorf("%w: %q is not a valid %s pattern", ErrInvalid, v, what)
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) > maxMuted {
		return nil, fmt.Errorf("%w: at most %d muted %ss", ErrInvalid, maxMuted, what)
	}
	return out, nil
}

// Window reports whether now falls in the quiet window and, if so, when it ends.
func (q QuietHours) Window(now time.Time) (inside bool, until time.Time) {
	if !q.Enabled {
		return false, time.Time{}
	}
	loc, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return false, time.Time{}
	}
	start, errS := time.Parse(clockLayout, q.Start)
	end, errE := time.Parse(clockLayout, q.End)
	if errS != nil || errE != nil {
		return false, time.Time{}
	}
	local := now.In(loc)
	at := func(c time.Time, dayOffset int) time.Time {
		return time.Date(local.Year(), local.Month(), local.Day()+dayOffset, c.Hour(), c.Minute(), 0, 0, loc)
	}
	switch {
	case start.Before(end): // same-day window, for example 13:00 to 15:00
		s, e := at(start, 0), at(end, 0)
		if !local.Before(s) && local.Before(e) {
			return true, e
		}
	default: // crosses midnight, for example 22:00 to 07:00
		if !local.Before(at(start, 0)) { // evening part: ends tomorrow morning
			return true, at(end, 1)
		}
		if local.Before(at(end, 0)) { // morning part: ends this morning
			return true, at(end, 0)
		}
	}
	return false, time.Time{}
}

// Adjustment is what preferences change about one event.
type Adjustment struct {
	// AttentionClass is the class to use, which may be lower than the one rules chose.
	AttentionClass string
	// Status is the decision status to record.
	Status string
	Reason string
}

// Decide applies the preferences to an event the rules have already classified.
// It returns false when nothing changes. Critical events are never adjusted.
func Decide(p Preferences, evt event.Event, now time.Time) (Adjustment, bool) {
	if evt.AttentionClass == event.AttentionCritical {
		return Adjustment{}, false
	}
	if p.muted(evt) {
		return Adjustment{AttentionClass: event.AttentionBackground, Status: event.DecisionDigest, Reason: "muted in your preferences"}, true
	}
	if evt.AttentionClass == event.AttentionStandard {
		if inside, until := p.QuietHours.Window(now); inside {
			return Adjustment{
				AttentionClass: event.AttentionStandard,
				Status:         event.DecisionDelayed,
				Reason:         "quiet hours until " + until.Format(clockLayout) + " " + p.QuietHours.Timezone,
			}, true
		}
	}
	return Adjustment{}, false
}

func (p Preferences) muted(evt event.Event) bool {
	for _, s := range p.MutedSources {
		if strings.EqualFold(s, evt.Source) {
			return true
		}
	}
	for _, pattern := range p.MutedRepositories {
		if ok, _ := path.Match(pattern, evt.Repository); ok && evt.Repository != "" {
			return true
		}
	}
	return false
}

// Store keeps the preferences.
type Store interface {
	// Load returns the stored preferences and whether any were stored.
	Load(ctx context.Context) (Preferences, bool, error)
	Save(ctx context.Context, p Preferences) error
}

// Service reads and updates preferences. The pipeline reads them on every
// event, so the current value is cached and replaced atomically on update.
type Service struct {
	store Store
	now   func() time.Time
	mu    sync.RWMutex
	cur   Preferences
}

// NewService loads the stored preferences, or the defaults when there are none.
func NewService(ctx context.Context, store Store) (*Service, error) {
	s := &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
	cur, ok, err := store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load preferences: %w", err)
	}
	if !ok {
		cur = Default()
	}
	s.cur = cur
	return s, nil
}

// WithClock replaces the clock; for tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Get returns the current preferences.
func (s *Service) Get() Preferences {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Update validates, stores and activates new preferences.
func (s *Service) Update(ctx context.Context, in Preferences) (Preferences, error) {
	next, err := Normalise(in)
	if err != nil {
		return Preferences{}, err
	}
	next.UpdatedAt = s.now()
	if err := s.store.Save(ctx, next); err != nil {
		return Preferences{}, err
	}
	s.mu.Lock()
	s.cur = next
	s.mu.Unlock()
	return next, nil
}

// Decide applies the current preferences to an event.
func (s *Service) Decide(evt event.Event) (Adjustment, bool) {
	return Decide(s.Get(), evt, s.now())
}

// MemoryStore keeps preferences in process. Used without a database and in tests.
type MemoryStore struct {
	mu  sync.Mutex
	p   Preferences
	set bool
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

func (m *MemoryStore) Load(ctx context.Context) (Preferences, bool, error) {
	if err := ctx.Err(); err != nil {
		return Preferences{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.p, m.set, nil
}

func (m *MemoryStore) Save(ctx context.Context, p Preferences) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.p, m.set = p, true
	return nil
}
