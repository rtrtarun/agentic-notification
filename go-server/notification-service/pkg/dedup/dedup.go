// Package dedup implements deterministic deduplication and threading.
// It is part of Layer 1 and never uses AI.
package dedup

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/example/notification-service/pkg/event"
)

// DefaultWindow is how long an identical event is treated as a duplicate.
const DefaultWindow = 10 * time.Minute

// Result describes what the Deduper decided for one event.
type Result struct {
	DedupKey  string
	ThreadID  string
	Duplicate bool
	// OriginalID is the first event seen with this key inside the window.
	OriginalID string
}

// Deduper assigns dedup keys and thread IDs and detects duplicates.
type Deduper interface {
	Check(evt event.Event) Result
}

// MemoryDeduper is an in-memory Deduper with a sliding time window.
type MemoryDeduper struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	seen   map[string]seenEntry
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
func (d *MemoryDeduper) Check(evt event.Event) Result {
	res := Result{DedupKey: DedupKey(evt), ThreadID: ThreadID(evt)}

	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	d.evict(now)

	if prev, ok := d.seen[res.DedupKey]; ok {
		res.Duplicate = true
		res.OriginalID = prev.id
		return res
	}
	d.seen[res.DedupKey] = seenEntry{id: evt.ID, at: now}
	return res
}

func (d *MemoryDeduper) evict(now time.Time) {
	for k, e := range d.seen {
		if now.Sub(e.at) > d.window {
			delete(d.seen, k)
		}
	}
}

// ThreadID groups related events about the same subject (e.g. one pull request).
func ThreadID(evt event.Event) string {
	parts := []string{evt.Source, evt.Repository}
	if subject := subjectOf(evt.Payload); subject != "" {
		parts = append(parts, subject)
	}
	return strings.Join(parts, ":")
}

// DedupKey identifies a repeat of the same thing: same thread, event type and action.
func DedupKey(evt event.Event) string {
	action, _ := evt.Payload["action"].(string)
	return strings.Join([]string{ThreadID(evt), evt.EventType, action}, "|")
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
