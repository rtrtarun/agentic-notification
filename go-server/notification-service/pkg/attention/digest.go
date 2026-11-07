package attention

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
)

// ErrDigestNotFound is returned for an unknown digest id.
var ErrDigestNotFound = errors.New("digest not found")

const (
	// maxDigestEvents bounds one digest; the rest wait for the next.
	maxDigestEvents = 500
	maxHighlights   = 3
	// maxMessageChars keeps the delivered text readable in an email or a chat message.
	maxMessageChars = 1800
)

// Digest is a summary of what was held back, with the events it covers.
type Digest struct {
	ID         string        `json:"id"`
	From       time.Time     `json:"from"`
	To         time.Time     `json:"to"`
	Count      int           `json:"count"`
	Title      string        `json:"title"`
	Summary    string        `json:"summary"`
	Groups     []DigestGroup `json:"groups"`
	Highlights []Highlight   `json:"highlights"`
	// Model names what wrote it ("heuristic-v1"), for the audit trail.
	Model string `json:"model"`
	// UserID is set on a digest made for one person; it is empty for the workspace digest.
	UserID string `json:"userId,omitempty"`
	// Source is "heuristic" or "model", so a screen can say who wrote it without parsing Model.
	Source    string    `json:"source"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

// DigestGroup is the part of a digest about one source or repository.
type DigestGroup struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Count    int      `json:"count"`
	EventIDs []string `json:"eventIds"`
}

// Highlight is an event worth looking at first.
type Highlight struct {
	EventID string `json:"eventId"`
	Title   string `json:"title"`
	Reason  string `json:"reason"`
	Score   int    `json:"score"`
}

// DigestStore keeps digests. Implementations: MemoryDigestStore and PostgresDigestStore.
type DigestStore interface {
	Add(ctx context.Context, d Digest) error
	// List returns digests newest first.
	List(ctx context.Context, limit int) ([]Digest, error)
	// ListFor returns one person's digests newest first. List never includes them.
	ListFor(ctx context.Context, userID string, limit int) ([]Digest, error)
	Get(ctx context.Context, id string) (Digest, error)
}

// Events is the part of the event store a digest reads. event.Repository satisfies it.
type Events interface {
	List(ctx context.Context) ([]event.Event, error)
}

// Notifier hands a finished digest to delivery. It may be nil.
type Notifier interface {
	Notify(ctx context.Context, digestID, title, body string) error
}

// Generator writes digests from the events that were held back.
type Generator struct {
	events     Events
	store      DigestStore
	classifier Classifier
	summarizer Summarizer
	notifier   Notifier
	now        func() time.Time
	logger     *slog.Logger
	mu         sync.Mutex // one digest at a time, so two triggers cannot cover the same events
}

// NewGenerator builds a generator. The classifier is used to pick highlights and
// may be nil; the notifier may be nil, in which case digests are only stored.
func NewGenerator(events Events, store DigestStore, c Classifier, s Summarizer, n Notifier, logger *slog.Logger) *Generator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Generator{events: events, store: store, classifier: Guarded(c), summarizer: s, notifier: n, now: time.Now, logger: logger}
}

// WithClock replaces the clock; for tests.
func (g *Generator) WithClock(now func() time.Time) *Generator {
	g.now = now
	return g
}

// Generate covers everything held back since the previous digest: events whose
// outcome is digest, delayed or budget_exceeded, never critical ones and never merged repeats. It
// returns false and stores nothing when there is nothing to report. A failure to
// hand the digest to delivery is logged; the digest is already stored and can be read.
func (g *Generator) Generate(ctx context.Context, actor string) (Digest, bool, error) {
	return g.generate(ctx, actor, "", nil)
}

// GenerateFor makes a digest for one person: the same held events, minus the ones
// exclude says that person does not want (their mutes and "never notify" choices).
// It covers what was held since that person's previous digest, is stored and readable,
// and is never delivered: personal delivery needs a per-person target. A critical
// event is never held, so exclude cannot hide one.
func (g *Generator) GenerateFor(ctx context.Context, actor, userID string, exclude func(event.Event) bool) (Digest, bool, error) {
	if userID == "" {
		return Digest{}, false, errors.New("a personal digest needs a person")
	}
	return g.generate(ctx, actor, userID, exclude)
}

func (g *Generator) generate(ctx context.Context, actor, userID string, exclude func(event.Event) bool) (Digest, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	var latest []Digest
	var err error
	if userID == "" {
		latest, err = g.store.List(ctx, 1)
	} else {
		latest, err = g.store.ListFor(ctx, userID, 1)
	}
	if err != nil {
		return Digest{}, false, fmt.Errorf("read previous digest: %w", err)
	}
	var from time.Time
	if len(latest) > 0 {
		from = latest[0].To
	}
	to := g.now().UTC()

	all, err := g.events.List(ctx)
	if err != nil {
		return Digest{}, false, fmt.Errorf("read events: %w", err)
	}
	var held []event.Event
	for _, e := range all {
		if e.AttentionClass == event.AttentionCritical || e.MergedInto != "" || e.DecisionState == nil || (exclude != nil && exclude(e)) {
			continue
		}
		if s := e.DecisionState.Status; s != event.DecisionDigest && s != event.DecisionDelayed && s != event.DecisionBudgetExceeded {
			continue
		}
		if !e.CreatedAt.After(from) || e.CreatedAt.After(to) {
			continue
		}
		held = append(held, e)
	}
	if len(held) == 0 {
		return Digest{}, false, nil
	}
	sort.Slice(held, func(a, b int) bool { return held[a].CreatedAt.Before(held[b].CreatedAt) })
	if len(held) > maxDigestEvents {
		// Keep the oldest and move the window up to the last one kept, so the rest go in the next digest.
		held = held[:maxDigestEvents]
		to = held[len(held)-1].CreatedAt
	}

	sum, err := g.summarizer.Summarize(ctx, held)
	if err != nil {
		return Digest{}, false, fmt.Errorf("summarise: %w", err)
	}
	model := sum.Model
	if model == "" {
		model = ModelHeuristic
	}
	d := Digest{
		ID: newID(), From: from, To: to, Count: len(held), Title: sum.Title, Summary: sum.Text,
		Highlights: g.highlights(ctx, held), Model: model, Source: SourceOf(model), UserID: userID, CreatedBy: actor, CreatedAt: g.now().UTC(),
	}
	for _, grp := range sum.Groups {
		d.Groups = append(d.Groups, DigestGroup(grp))
	}
	if err := g.store.Add(ctx, d); err != nil {
		return Digest{}, false, fmt.Errorf("store digest: %w", err)
	}
	if g.notifier != nil && userID == "" {
		if err := g.notifier.Notify(ctx, d.ID, "Attention digest: "+d.Title, Message(d)); err != nil {
			g.logger.Warn("digest stored but could not be queued for delivery", "digest", d.ID, "error", err)
		}
	}
	return d, true, nil
}

// highlights picks the held events the classifier scores highest, so the digest
// leads with what is most likely to matter.
func (g *Generator) highlights(ctx context.Context, held []event.Event) []Highlight {
	if g.classifier == nil {
		return nil
	}
	var out []Highlight
	for _, e := range held {
		c, err := g.classifier.Classify(ctx, e)
		if err != nil || c.Score < baseScore { // only events at or above a neutral score are worth leading with
			continue
		}
		out = append(out, Highlight{EventID: e.ID, Title: eventTitle(e), Reason: c.Reason(), Score: c.Score})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	if len(out) > maxHighlights {
		out = out[:maxHighlights]
	}
	return out
}

func eventTitle(e event.Event) string {
	title := e.Source + " " + e.EventType
	if e.Repository != "" {
		title += " in " + e.Repository
	}
	return title
}

// Message renders a digest as plain text for email, chat or a text message.
func Message(d Digest) string {
	var b strings.Builder
	b.WriteString(d.Summary)
	if len(d.Highlights) > 0 {
		b.WriteString("\n\nLook at first:")
		for _, h := range d.Highlights {
			fmt.Fprintf(&b, "\n- %s (%s)", h.Title, h.Reason)
		}
	}
	if len(d.Groups) > 0 {
		b.WriteString("\n\nBy place:")
		for _, g := range d.Groups {
			fmt.Fprintf(&b, "\n- %s: %s", g.Title, g.Summary)
		}
	}
	if r := []rune(b.String()); len(r) > maxMessageChars {
		return string(r[:maxMessageChars-1]) + "…"
	}
	return b.String()
}

// Scheduler generates a digest on a fixed interval. With a zero interval it never runs.
type Scheduler struct {
	gen    *Generator
	every  time.Duration
	logger *slog.Logger
}

// NewScheduler builds a scheduler.
func NewScheduler(g *Generator, every time.Duration, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{gen: g, every: every, logger: logger}
}

// Run generates a digest every interval until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	if s.every <= 0 {
		return
	}
	t := time.NewTicker(s.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if d, made, err := s.gen.Generate(ctx, "schedule"); err != nil {
				s.logger.Error("scheduled digest failed", "error", err)
			} else if made {
				s.logger.Info("digest generated", "digest", d.ID, "events", d.Count)
			}
		}
	}
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// MemoryDigestStore is the in-process DigestStore used without a database and in tests.
type MemoryDigestStore struct {
	mu   sync.Mutex
	list []Digest
}

// NewMemoryDigestStore returns an empty store.
func NewMemoryDigestStore() *MemoryDigestStore { return &MemoryDigestStore{} }

// Add implements DigestStore.
func (m *MemoryDigestStore) Add(_ context.Context, d Digest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.list = append(m.list, d)
	return nil
}

// List implements DigestStore: workspace digests only, newest first.
func (m *MemoryDigestStore) List(_ context.Context, limit int) ([]Digest, error) {
	return m.newest("", limit), nil
}

// ListFor implements DigestStore.
func (m *MemoryDigestStore) ListFor(_ context.Context, userID string, limit int) ([]Digest, error) {
	if userID == "" {
		return nil, errors.New("a person is required")
	}
	return m.newest(userID, limit), nil
}

func (m *MemoryDigestStore) newest(userID string, limit int) []Digest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Digest, 0, len(m.list))
	for i := len(m.list) - 1; i >= 0; i-- {
		if m.list[i].UserID != userID {
			continue
		}
		out = append(out, m.list[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// Get implements DigestStore.
func (m *MemoryDigestStore) Get(_ context.Context, id string) (Digest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.list {
		if d.ID == id {
			return d, nil
		}
	}
	return Digest{}, ErrDigestNotFound
}
