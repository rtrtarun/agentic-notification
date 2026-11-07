package auth

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Roles. An admin changes rules, channels, preferences and users; a member
// works the inbox.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Account states. A person who signs up starts as pending and can do nothing
// until an admin approves the account. An empty status means active, so
// accounts created before this field existed keep working.
const (
	StatusActive   = "active"
	StatusPending  = "pending"
	StatusRejected = "rejected"
)

// Errors the HTTP layer maps to status codes.
var (
	ErrNotFound   = errors.New("not found")
	ErrEmailTaken = errors.New("an account with that email already exists")
)

// User is a person who can sign in. PasswordHash is empty for accounts that
// only sign in through an identity provider, and is never serialised.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"`
	Disabled     bool      `json:"disabled"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Active reports whether the account may sign in: it is not disabled and has
// been approved (an empty status counts as approved).
func (u User) Active() bool {
	return !u.Disabled && (u.Status == "" || u.Status == StatusActive)
}

// Session is a signed-in browser. Hash is the SHA-256 of the cookie token, so a
// database leak does not hand out live sessions.
type Session struct {
	Hash      string    `json:"-"`
	UserID    string    `json:"-"`
	CSRF      string    `json:"-"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// AuditEntry records one security-relevant action. Details never hold secrets.
type AuditEntry struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target,omitempty"`
	IP     string    `json:"ip,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// Store keeps users, sessions and the audit trail. Implementations: MemoryStore and PostgresStore.
type Store interface {
	CreateUser(ctx context.Context, u User) error
	UserByID(ctx context.Context, id string) (User, error)
	UserByEmail(ctx context.Context, email string) (User, error)
	UserByIdentity(ctx context.Context, issuer, subject string) (User, error)
	LinkIdentity(ctx context.Context, userID, issuer, subject string) error
	ListUsers(ctx context.Context) ([]User, error)
	UpdateUser(ctx context.Context, u User) error
	CountUsers(ctx context.Context) (int, error)
	// CountActiveAdmins counts enabled admins, so the last one can never be removed.
	CountActiveAdmins(ctx context.Context) (int, error)

	CreateSession(ctx context.Context, s Session) error
	SessionByHash(ctx context.Context, hash string) (Session, error)
	DeleteSession(ctx context.Context, hash string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error

	AddAudit(ctx context.Context, e AuditEntry) error
	ListAudit(ctx context.Context, limit int) ([]AuditEntry, error)
}

// NormalizeEmail lowercases and trims an address for storage and lookup.
func NormalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// MemoryStore is the in-process Store used without a database and in tests.
type MemoryStore struct {
	mu         sync.Mutex
	users      map[string]User
	identities map[string]string // issuer + "\x00" + subject -> user id
	sessions   map[string]Session
	audit      []AuditEntry
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: map[string]User{}, identities: map[string]string{}, sessions: map[string]Session{}}
}

// CreateUser implements Store.
func (m *MemoryStore) CreateUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, other := range m.users {
		if other.Email == u.Email {
			return ErrEmailTaken
		}
	}
	m.users[u.ID] = u
	return nil
}

// UserByID implements Store.
func (m *MemoryStore) UserByID(_ context.Context, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		return u, nil
	}
	return User{}, ErrNotFound
}

// UserByEmail implements Store.
func (m *MemoryStore) UserByEmail(_ context.Context, email string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

// UserByIdentity implements Store.
func (m *MemoryStore) UserByIdentity(_ context.Context, issuer, subject string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.identities[issuer+"\x00"+subject]; ok {
		return m.users[id], nil
	}
	return User{}, ErrNotFound
}

// LinkIdentity implements Store.
func (m *MemoryStore) LinkIdentity(_ context.Context, userID, issuer, subject string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return ErrNotFound
	}
	m.identities[issuer+"\x00"+subject] = userID
	return nil
}

// ListUsers implements Store.
func (m *MemoryStore) ListUsers(_ context.Context) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Email < out[b].Email })
	return out, nil
}

// UpdateUser implements Store.
func (m *MemoryStore) UpdateUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.ID]; !ok {
		return ErrNotFound
	}
	m.users[u.ID] = u
	return nil
}

// CountUsers implements Store.
func (m *MemoryStore) CountUsers(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

// CountActiveAdmins implements Store.
func (m *MemoryStore) CountActiveAdmins(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, u := range m.users {
		if u.Role == RoleAdmin && !u.Disabled {
			n++
		}
	}
	return n, nil
}

// CreateSession implements Store.
func (m *MemoryStore) CreateSession(_ context.Context, s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.Hash] = s
	return nil
}

// SessionByHash implements Store.
func (m *MemoryStore) SessionByHash(_ context.Context, hash string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[hash]; ok {
		return s, nil
	}
	return Session{}, ErrNotFound
}

// DeleteSession implements Store.
func (m *MemoryStore) DeleteSession(_ context.Context, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, hash)
	return nil
}

// DeleteUserSessions implements Store.
func (m *MemoryStore) DeleteUserSessions(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, h)
		}
	}
	return nil
}

// DeleteExpiredSessions implements Store.
func (m *MemoryStore) DeleteExpiredSessions(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, s := range m.sessions {
		if !s.ExpiresAt.After(now) {
			delete(m.sessions, h)
		}
	}
	return nil
}

// maxMemoryAudit bounds the in-memory audit trail.
const maxMemoryAudit = 5000

// AddAudit implements Store.
func (m *MemoryStore) AddAudit(_ context.Context, e AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, e)
	if len(m.audit) > maxMemoryAudit {
		m.audit = m.audit[len(m.audit)-maxMemoryAudit:]
	}
	return nil
}

// ListAudit implements Store; newest first.
func (m *MemoryStore) ListAudit(_ context.Context, limit int) ([]AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditEntry, 0, len(m.audit))
	for i := len(m.audit) - 1; i >= 0; i-- {
		out = append(out, m.audit[i])
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
