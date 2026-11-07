package attention

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/example/notification-service/pkg/event"
)

// Defaults for a Worker.
const (
	DefaultWorkerInterval = 2 * time.Second
	DefaultWorkerBatch    = 25
	// DefaultWorkerLease is how long a claimed event belongs to one worker. If the worker
	// dies, the lease expires and another worker picks the event up.
	DefaultWorkerLease = time.Minute
)

// Processor does the actual deciding for an event the worker claims. The pipeline
// implements it, so the rules for classification, preferences, budgets and
// delivery live in one place whether an event is decided at ingest or later.
type Processor interface {
	// ProcessPending decides on a queued event and queues its delivery if it will interrupt.
	ProcessPending(ctx context.Context, evt event.Event) (event.Event, error)
	// ReleaseDelayed lets an event held for quiet hours go out once they have ended. It
	// reports whether the event changed.
	ReleaseDelayed(ctx context.Context, evt event.Event) (event.Event, bool, error)
}

// Worker drains the attention work queue: events saved as queued and pending, and
// events held for quiet hours. It never touches a critical event; the queue does not
// return one, Process refuses one, and the store refuses to change one.
type Worker struct {
	queue    event.WorkQueue
	proc     Processor
	logger   *slog.Logger
	interval time.Duration
	batch    int
	lease    time.Duration
	now      func() time.Time
}

// NewWorker returns a worker over a queue and a processor.
func NewWorker(queue event.WorkQueue, proc Processor, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{queue: queue, proc: proc, logger: logger, interval: DefaultWorkerInterval, batch: DefaultWorkerBatch, lease: DefaultWorkerLease, now: time.Now}
}

// WithInterval sets how often the queue is polled; non-positive keeps the default.
func (w *Worker) WithInterval(d time.Duration) *Worker {
	if d > 0 {
		w.interval = d
	}
	return w
}

// WithClock replaces the clock; tests use it.
func (w *Worker) WithClock(now func() time.Time) *Worker {
	w.now = now
	return w
}

// Run polls until ctx is cancelled. It owns no goroutines of its own, so the
// caller decides where it runs and waits for it to return.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if _, err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("attention worker pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce makes one pass: it decides on claimed events, then releases delayed events
// whose quiet hours are over. It returns how many events it changed.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	changed := 0
	claimed, err := w.queue.ClaimPending(ctx, w.now(), w.lease, w.batch)
	if err != nil {
		return 0, err
	}
	var firstErr error
	for _, evt := range claimed {
		if evt.AttentionClass == event.AttentionCritical {
			// The queue must never return one; if it does, leave it exactly as it is.
			w.logger.Error("critical event reached the attention worker; ignoring it", "event_id", evt.ID)
			continue
		}
		out, err := w.proc.ProcessPending(ctx, evt)
		if err != nil {
			w.logger.Warn("attention processing failed; the event will be retried", "event_id", evt.ID, "error", err)
			firstErr = keepFirst(firstErr, err)
			continue
		}
		if err := w.queue.UpdateDecision(ctx, out.ID, out.AttentionClass, *out.DecisionState); err != nil {
			w.logger.Warn("storing the decision failed; the event will be retried", "event_id", evt.ID, "error", err)
			firstErr = keepFirst(firstErr, err)
			continue
		}
		changed++
	}

	delayed, err := w.queue.ListDelayed(ctx, w.batch)
	if err != nil {
		return changed, errors.Join(firstErr, err)
	}
	for _, evt := range delayed {
		if evt.AttentionClass == event.AttentionCritical {
			w.logger.Error("critical event reached the attention worker's delayed list; ignoring it", "event_id", evt.ID)
			continue
		}
		out, released, err := w.proc.ReleaseDelayed(ctx, evt)
		if err != nil {
			w.logger.Warn("releasing a delayed event failed", "event_id", evt.ID, "error", err)
			firstErr = keepFirst(firstErr, err)
			continue
		}
		if !released {
			continue
		}
		if err := w.queue.UpdateDecision(ctx, out.ID, out.AttentionClass, *out.DecisionState); err != nil {
			w.logger.Warn("storing the release failed; it will be retried", "event_id", evt.ID, "error", err)
			firstErr = keepFirst(firstErr, err)
			continue
		}
		changed++
	}
	return changed, firstErr
}

func keepFirst(current, next error) error {
	if current != nil {
		return current
	}
	return next
}
