package preferences

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/example/notification-service/pkg/event"
)

// ErrNoUser is returned when personal preferences are asked for without a person.
var ErrNoUser = errors.New("personal preferences need a signed-in person")

// PersonalStore keeps one person's preferences, keyed by user ID. A person who has
// saved none uses the workspace's.
type PersonalStore interface {
	// LoadFor returns the person's stored preferences and whether any were stored.
	LoadFor(ctx context.Context, userID string) (Preferences, bool, error)
	SaveFor(ctx context.Context, userID string, p Preferences) error
}

// WithPersonal enables per-person preferences.
func (s *Service) WithPersonal(ps PersonalStore) *Service {
	s.personal = ps
	return s
}

// GetFor returns the person's own preferences, or the workspace's when they have
// none, and says which. Preferences set by a person never change what the pipeline
// does for everyone: they shape that person's own digest.
func (s *Service) GetFor(ctx context.Context, userID string) (Preferences, bool, error) {
	if s.personal == nil || userID == "" {
		return s.Get(), false, nil
	}
	p, ok, err := s.personal.LoadFor(ctx, userID)
	if err != nil {
		return Preferences{}, false, fmt.Errorf("load personal preferences: %w", err)
	}
	if !ok {
		return s.Get(), false, nil
	}
	return p, true, nil
}

// UpdateFor validates and stores a person's preferences.
func (s *Service) UpdateFor(ctx context.Context, userID string, in Preferences) (Preferences, error) {
	if userID == "" || s.personal == nil {
		return Preferences{}, ErrNoUser
	}
	next, err := Normalise(in)
	if err != nil {
		return Preferences{}, err
	}
	next.UpdatedAt = s.now()
	if err := s.personal.SaveFor(ctx, userID, next); err != nil {
		return Preferences{}, err
	}
	return next, nil
}

// Muted reports whether the preferences mute an event's source or repository.
// It is false for a critical event: nothing mutes one.
func (p Preferences) Muted(evt event.Event) bool {
	if evt.AttentionClass == event.AttentionCritical {
		return false
	}
	return p.muted(evt)
}

// MemoryPersonalStore keeps personal preferences in process.
type MemoryPersonalStore struct {
	mu   sync.Mutex
	byID map[string]Preferences
}

// NewMemoryPersonalStore returns an empty store.
func NewMemoryPersonalStore() *MemoryPersonalStore {
	return &MemoryPersonalStore{byID: map[string]Preferences{}}
}

// LoadFor implements PersonalStore.
func (m *MemoryPersonalStore) LoadFor(ctx context.Context, userID string) (Preferences, bool, error) {
	if err := ctx.Err(); err != nil {
		return Preferences{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.byID[userID]
	return p, ok, nil
}

// SaveFor implements PersonalStore.
func (m *MemoryPersonalStore) SaveFor(ctx context.Context, userID string, p Preferences) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID[userID] = p
	return nil
}

// PostgresPersonalStore keeps personal preferences in the user_preferences table (see pkg/db).
type PostgresPersonalStore struct{ db DB }

// NewPostgresPersonalStore wraps a database handle.
func NewPostgresPersonalStore(db DB) *PostgresPersonalStore { return &PostgresPersonalStore{db: db} }

// LoadFor implements PersonalStore.
func (p *PostgresPersonalStore) LoadFor(ctx context.Context, userID string) (Preferences, bool, error) {
	var body []byte
	err := p.db.QueryRowContext(ctx, `SELECT body FROM user_preferences WHERE user_id = $1`, userID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return Preferences{}, false, nil
	}
	if err != nil {
		return Preferences{}, false, err
	}
	var out Preferences
	if err := json.Unmarshal(body, &out); err != nil {
		return Preferences{}, false, err
	}
	return out, true, nil
}

// SaveFor implements PersonalStore.
func (p *PostgresPersonalStore) SaveFor(ctx context.Context, userID string, prefs Preferences) error {
	body, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, body, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE SET body = EXCLUDED.body, updated_at = NOW()`, userID, body)
	return err
}
