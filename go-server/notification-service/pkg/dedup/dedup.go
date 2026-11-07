// Package dedup implements deterministic deduplication and threading.
// It is part of Layer 1 and never uses AI.
package dedup

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
)

// DefaultWindow is how long an identical event is treated as a duplicate.
const DefaultWindow = 10 * time.Minute

// maxMemoryEntries bounds MemoryDeduper. When full it stops recording new keys,
// which can only let a duplicate through, never suppress a distinct event.
const maxMemoryEntries = 100_000

// Result describes what the Deduper decided for one event.
type Result struct {
	DedupKey  string
	ThreadID  string
	Duplicate bool
	// OriginalID is the first event seen with this key inside the window.
	OriginalID string
}

// Deduper assigns dedup keys and thread IDs and detects duplicates.
//
// Implementations must fail open: when the backing store is unavailable, Check
// returns a Result with Duplicate false (plus the error) so no event is ever
// suppressed because of an infrastructure fault.
type Deduper interface {
	// Check records the event and reports whether it duplicates a recent one.
	Check(ctx context.Context, evt event.Event) (Result, error)
	// Forget removes the entry for key if it was recorded by event id. Callers use it
	// when processing failed after Check, so a retry is not mistaken for a duplicate.
	Forget(ctx context.Context, key, id string) error
}

// MemoryDeduper is an in-memory Deduper with a sliding time window. It only
// deduplicates within one process; use RedisDeduper when running several replicas.
type MemoryDeduper struct {
	mu        sync.Mutex
	window    time.Duration
	now       func() time.Time
	seen      map[string]seenEntry
	nextSweep time.Time
}

type seenEntry struct {
	id string
	at time.Time
}

// NewMemoryDeduper creates a deduper; a non-positive window uses DefaultWindow.
func NewMemoryDeduper(window time.Duration) *MemoryDeduper {
	if window <= 0 {
		window = DefaultWindow
	}
	return &MemoryDeduper{window: window, now: time.Now, seen: map[string]seenEntry{}}
}

// Check records the event and reports whether it duplicates a recent one.
func (d *MemoryDeduper) Check(_ context.Context, evt event.Event) (Result, error) {
	res := Result{DedupKey: DedupKey(evt), ThreadID: ThreadID(evt)}

	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	d.sweep(now)

	if prev, ok := d.seen[res.DedupKey]; ok && now.Sub(prev.at) <= d.window {
		res.Duplicate = true
		res.OriginalID = prev.id
		return res, nil
	}
	if len(d.seen) < maxMemoryEntries {
		d.seen[res.DedupKey] = seenEntry{id: evt.ID, at: now}
	}
	return res, nil
}

// Forget drops key if it was recorded by event id.
func (d *MemoryDeduper) Forget(_ context.Context, key, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, ok := d.seen[key]; ok && prev.id == id {
		delete(d.seen, key)
	}
	return nil
}

// sweep drops expired entries at most once per half window, so the cost is not
// paid on every event while the map stays bounded by the window.
func (d *MemoryDeduper) sweep(now time.Time) {
	if now.Before(d.nextSweep) && len(d.seen) < maxMemoryEntries {
		return
	}
	for k, e := range d.seen {
		if now.Sub(e.at) > d.window {
			delete(d.seen, k)
		}
	}
	d.nextSweep = now.Add(d.window / 2)
}

// ThreadID groups related events about the same subject (e.g. one pull request),
// or all the steps of one agent run.
func ThreadID(evt event.Event) string {
	if evt.TraceID != "" {
		return evt.Source + ":trace:" + evt.TraceID
	}
	parts := []string{evt.Source, evt.Repository}
	if subject := subjectOf(evt.Payload); subject != "" {
		parts = append(parts, subject)
	}
	return strings.Join(parts, ":")
}

// DedupKey identifies a repeat of the same thing: same thread, event type, action
// and, for pushes, the same head commit. Without the commit, every push to a
// repository inside the window would collapse into the first.
//
// Steps of an agent run are different: a retry or a repeated failure is a new fact,
// not a repeat, so two steps merge only when they share the run, the type, the parent
// step, the step kind and the attempt number.
func DedupKey(evt event.Event) string {
	if evt.TraceID != "" {
		md, _ := evt.Payload["metadata"].(map[string]interface{})
		kind, _ := md["step_kind"].(string)
		return strings.Join([]string{ThreadID(evt), evt.EventType, evt.ParentEventID, kind, fmt.Sprint(md["attempt"])}, "|")
	}
	action, _ := evt.Payload["action"].(string)
	head, _ := evt.Payload["after"].(string)
	return strings.Join([]string{ThreadID(evt), evt.EventType, action, head}, "|")
}

// subjectOf extracts an issue / pull request number from a payload, if any.
func subjectOf(payload map[string]interface{}) string {
	if n, ok := payload["number"].(float64); ok {
		return fmt.Sprintf("%d", int64(n))
	}
	for _, key := range []string{"pull_request", "issue"} {
		if obj, ok := payload[key].(map[string]interface{}); ok {
			if n, ok := obj["number"].(float64); ok {
				return fmt.Sprintf("%d", int64(n))
			}
		}
	}
	return ""
}
