package delivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// Channel IDs used by routing, the queue and the console.
const (
	ChannelEmail = "email"
	ChannelSlack = "slack"
	ChannelTeams = "teams"
	ChannelSMS   = "sms"
)

// Status is where a job is in its life.
type Status string

const (
	// StatusPending waits for its time; a job that failed once and will retry is pending too.
	StatusPending Status = "pending"
	// StatusSending is leased to a worker. A lease that expires makes the job claimable again.
	StatusSending Status = "sending"
	StatusSent    Status = "sent"
	// StatusDead has used all attempts or can never succeed. A person can retry it.
	StatusDead Status = "dead"
)

// Errors the HTTP layer maps to status codes.
var (
	ErrNotFound     = errors.New("delivery not found")
	ErrNotRetryable = errors.New("only a dead delivery can be retried")
)

// Job is one delivery of one event through one channel. It holds the text but
// never the target: addresses and webhook URLs are resolved from configuration
// when the job is sent, so secrets never reach the database or the console.
type Job struct {
	ID            string    `json:"id"`
	EventID       string    `json:"eventId"`
	Channel       string    `json:"channel"`
	Class         string    `json:"class"`
	Title         string    `json:"title"`
	Message       string    `json:"-"`
	Group         string    `json:"group,omitempty"`
	Status        Status    `json:"status"`
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"nextAttemptAt"`
	LeaseUntil    time.Time `json:"-"`
	LastError     string    `json:"lastError,omitempty"`
	// Direct marks a delivery made inline (critical events) and only logged here.
	Direct    bool      `json:"direct,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Store keeps the delivery queue. Implementations: MemoryStore and PostgresStore.
type Store interface {
	// Enqueue adds a pending job. It reports false, and changes nothing, when a
	// job for the same event and channel exists, so a redelivered webhook or a
	// retry never sends twice.
	Enqueue(ctx context.Context, j Job) (bool, error)
	// Record stores or replaces the log entry of an inline delivery.
	Record(ctx context.Context, j Job) error
	// Claim leases up to limit jobs that are due and counts the attempt.
	Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Job, error)
	Complete(ctx context.Context, id string, now time.Time) error
	// Fail records an error. A nil retryAt moves the job to dead.
	Fail(ctx context.Context, id, errText string, retryAt *time.Time, now time.Time) error
	// Requeue moves a dead job back to pending, due now, with its attempts reset.
	Requeue(ctx context.Context, id string, now time.Time) error
	// List returns the newest jobs first.
	List(ctx context.Context, limit int) ([]Job, error)
	Counts(ctx context.Context) (map[Status]int, error)
	// CountSent counts jobs sent through channel since the given time.
	CountSent(ctx context.Context, channel string, since time.Time) (int, error)
}

// NewJobID returns a random identifier.
func NewJobID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// MemoryStore is the in-process Store used without a database and in tests.
type MemoryStore struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{jobs: map[string]*Job{}} }

func (m *MemoryStore) find(eventID, channel string) *Job {
	for _, j := range m.jobs {
		if j.EventID == eventID && j.Channel == channel {
			return j
		}
	}
	return nil
}

// Enqueue implements Store.
func (m *MemoryStore) Enqueue(_ context.Context, j Job) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.find(j.EventID, j.Channel) != nil {
		return false, nil
	}
	cp := j
	m.jobs[j.ID] = &cp
	return true, nil
}

// Record implements Store.
func (m *MemoryStore) Record(_ context.Context, j Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old := m.find(j.EventID, j.Channel); old != nil {
		delete(m.jobs, old.ID)
		j.CreatedAt = old.CreatedAt
	}
	cp := j
	m.jobs[j.ID] = &cp
	return nil
}

// Claim implements Store.
func (m *MemoryStore) Claim(_ context.Context, now time.Time, lease time.Duration, limit int) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []*Job
	for _, j := range m.jobs {
		pending := j.Status == StatusPending && !j.NextAttemptAt.After(now)
		expired := j.Status == StatusSending && !j.LeaseUntil.After(now)
		if pending || expired {
			due = append(due, j)
		}
	}
	// Ties are broken by ID so the order never depends on map iteration.
	sort.Slice(due, func(a, b int) bool {
		if !due[a].NextAttemptAt.Equal(due[b].NextAttemptAt) {
			return due[a].NextAttemptAt.Before(due[b].NextAttemptAt)
		}
		return due[a].ID < due[b].ID
	})
	if len(due) > limit {
		due = due[:limit]
	}
	out := make([]Job, 0, len(due))
	for _, j := range due {
		j.Status, j.Attempts, j.LeaseUntil, j.UpdatedAt = StatusSending, j.Attempts+1, now.Add(lease), now
		out = append(out, *j)
	}
	return out, nil
}

// Complete implements Store.
func (m *MemoryStore) Complete(_ context.Context, id string, now time.Time) error {
	return m.update(id, func(j *Job) {
		j.Status, j.LastError, j.UpdatedAt = StatusSent, "", now
	})
}

// Fail implements Store.
func (m *MemoryStore) Fail(_ context.Context, id, errText string, retryAt *time.Time, now time.Time) error {
	return m.update(id, func(j *Job) {
		j.LastError, j.UpdatedAt = errText, now
		if retryAt == nil {
			j.Status = StatusDead
			return
		}
		j.Status, j.NextAttemptAt = StatusPending, *retryAt
	})
}

// Requeue implements Store.
func (m *MemoryStore) Requeue(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if j.Status != StatusDead {
		return ErrNotRetryable
	}
	j.Status, j.Attempts, j.NextAttemptAt, j.UpdatedAt = StatusPending, 0, now, now
	return nil
}

func (m *MemoryStore) update(id string, fn func(*Job)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	fn(j)
	return nil
}

// List implements Store.
func (m *MemoryStore) List(_ context.Context, limit int) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Counts implements Store.
func (m *MemoryStore) Counts(_ context.Context) (map[Status]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[Status]int{}
	for _, j := range m.jobs {
		out[j.Status]++
	}
	return out, nil
}

// CountSent implements Store.
func (m *MemoryStore) CountSent(_ context.Context, channel string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, j := range m.jobs {
		if j.Channel == channel && j.Status == StatusSent && !j.UpdatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}
