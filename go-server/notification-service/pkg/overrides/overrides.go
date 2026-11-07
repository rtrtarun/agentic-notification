// Package overrides holds the "always notify" and "never notify" choices people
// make from feedback. They are plain, visible rules in the deterministic
// layer: an override matches by source, event type and repository, the most
// specific one wins, and none of them can ever touch a critical event.
package overrides

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/notification-service/pkg/event"
)

// Modes.
const (
	ModeAlways = "always"
	ModeNever  = "never"
)

const maxFieldLength = 200

// Errors the HTTP layer maps to status codes.
var (
	ErrInvalid  = errors.New("invalid override")
	ErrNotFound = errors.New("override not found")
)

// Override says what to do with non-critical events that match. An empty field matches anything.
type Override struct {
	ID         string    `json:"id"`
	Source     string    `json:"source,omitempty"`
	EventType  string    `json:"eventType,omitempty"`
	Repository string    `json:"repository,omitempty"` // may contain * ("acme/*")
	Mode       string    `json:"mode"`
	CreatedBy  string    `json:"createdBy"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Decision is what an override decided for one event.
type Decision struct {
	Mode       string
	OverrideID string
	// Reason is a plain sentence that names the override, shown in the event's decision.
	Reason string
}

// Store keeps overrides. Implementations: MemoryStore and PostgresStore.
type Store interface {
	Load(ctx context.Context) ([]Override, error)
	// Save inserts an override or, when one with the same scope exists, replaces its mode.
	Save(ctx context.Context, o Override) (Override, error)
	Delete(ctx context.Context, id string) error
}

// Service serves decisions from a cached copy, so deciding costs no query. The
// cache is per process: with several replicas a change reaches the others on restart.
type Service struct {
	store Store
	mu    sync.RWMutex
	list  []Override
	now   func() time.Time
}

// NewService loads the stored overrides.
func NewService(ctx context.Context, store Store) (*Service, error) {
	list, err := store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load overrides: %w", err)
	}
	return &Service{store: store, list: list, now: time.Now}, nil
}

// List returns every override, newest first.
func (s *Service) List() []Override {
	s.mu.RLock()
	out := append([]Override(nil), s.list...)
	s.mu.RUnlock()
	sort.SliceStable(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out
}

// Upsert validates and saves an override. The same scope replaces the old one.
func (s *Service) Upsert(ctx context.Context, o Override) (Override, error) {
	o.Source, o.EventType, o.Repository = strings.TrimSpace(o.Source), strings.TrimSpace(o.EventType), strings.TrimSpace(o.Repository)
	o.Note = strings.TrimSpace(o.Note)
	if err := validate(o); err != nil {
		return Override{}, err
	}
	o.ID, o.CreatedAt = newID(), s.now().UTC()
	saved, err := s.store.Save(ctx, o)
	if err != nil {
		return Override{}, err
	}
	s.mu.Lock()
	replaced := false
	for i := range s.list {
		if scopeKey(s.list[i]) == scopeKey(saved) {
			s.list[i], replaced = saved, true
		}
	}
	if !replaced {
		s.list = append(s.list, saved)
	}
	s.mu.Unlock()
	return saved, nil
}

// Delete removes an override.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	for i := range s.list {
		if s.list[i].ID == id {
			s.list = append(s.list[:i], s.list[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return nil
}

// Decide returns the override that applies to a non-critical event. It never
// answers for a critical one. The most specific match wins; when two are
// equally specific, "always" wins, because missing something is worse than one extra message.
func (s *Service) Decide(evt event.Event) (Decision, bool) {
	if evt.AttentionClass == event.AttentionCritical {
		return Decision{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *Override
	for i := range s.list {
		o := &s.list[i]
		if !matches(*o, evt) {
			continue
		}
		if best == nil || specificity(*o) > specificity(*best) || (specificity(*o) == specificity(*best) && o.Mode == ModeAlways && best.Mode != ModeAlways) {
			best = o
		}
	}
	if best == nil {
		return Decision{}, false
	}
	return Decision{Mode: best.Mode, OverrideID: best.ID, Reason: fmt.Sprintf("%s notify override for %s", best.Mode, Describe(*best))}, true
}

// Describe names an override's scope in a few words.
func Describe(o Override) string {
	var parts []string
	if o.Source != "" {
		parts = append(parts, o.Source)
	}
	if o.EventType != "" {
		parts = append(parts, o.EventType)
	}
	if o.Repository != "" {
		parts = append(parts, "repository "+o.Repository)
	}
	return strings.Join(parts, " / ")
}

func validate(o Override) error {
	if o.Mode != ModeAlways && o.Mode != ModeNever {
		return fmt.Errorf("%w: mode must be always or never", ErrInvalid)
	}
	if o.Source == "" && o.EventType == "" && o.Repository == "" {
		return fmt.Errorf("%w: choose a source, an event type or a repository", ErrInvalid)
	}
	for _, v := range []string{o.Source, o.EventType, o.Repository, o.Note} {
		if len(v) > maxFieldLength {
			return fmt.Errorf("%w: values must be at most %d characters", ErrInvalid, maxFieldLength)
		}
	}
	if o.Repository != "" {
		if _, err := path.Match(strings.ToLower(o.Repository), "x"); err != nil {
			return fmt.Errorf("%w: the repository pattern is malformed", ErrInvalid)
		}
	}
	return nil
}

func scopeKey(o Override) string {
	return strings.ToLower(o.Source) + scopeSep + strings.ToLower(o.EventType) + scopeSep + strings.ToLower(o.Repository)
}

// ScopeKey is the identity of an override's scope, for stores that enforce uniqueness.
func ScopeKey(o Override) string { return scopeKey(o) }

func specificity(o Override) int {
	n := 0
	for _, v := range []string{o.Source, o.EventType, o.Repository} {
		if v != "" {
			n++
		}
	}
	return n
}

func matches(o Override, evt event.Event) bool {
	if o.Source != "" && !strings.EqualFold(o.Source, evt.Source) {
		return false
	}
	if o.EventType != "" && !strings.EqualFold(o.EventType, evt.EventType) {
		return false
	}
	if o.Repository != "" {
		ok, err := path.Match(strings.ToLower(o.Repository), strings.ToLower(evt.Repository))
		if err != nil || !ok {
			return false
		}
	}
	return true
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// MemoryStore is the in-process Store used without a database and in tests.
type MemoryStore struct {
	mu   sync.Mutex
	list []Override
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// Load implements Store.
func (m *MemoryStore) Load(context.Context) ([]Override, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Override(nil), m.list...), nil
}

// Save implements Store.
func (m *MemoryStore) Save(_ context.Context, o Override) (Override, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.list {
		if scopeKey(m.list[i]) == scopeKey(o) {
			o.ID, o.CreatedAt = m.list[i].ID, m.list[i].CreatedAt
			m.list[i] = o
			return o, nil
		}
	}
	m.list = append(m.list, o)
	return o, nil
}

// Delete implements Store.
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.list {
		if m.list[i].ID == id {
			m.list = append(m.list[:i], m.list[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// scopeSep separates the parts of a scope key. It must be valid text: PostgreSQL
// rejects a NUL byte, which an earlier version used, so no override could be saved there.
const scopeSep = "\x1f"

// NeverFor reports whether a particular person asked never to be notified about a
// non-critical event. It looks only at "never" choices that person made, so one
// person's choice never changes another's digest. A critical event is never covered.
func (s *Service) NeverFor(createdBy string, evt event.Event) bool {
	if evt.AttentionClass == event.AttentionCritical || createdBy == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, o := range s.list {
		if o.CreatedBy == createdBy && o.Mode == ModeNever && matches(o, evt) {
			return true
		}
	}
	return false
}
