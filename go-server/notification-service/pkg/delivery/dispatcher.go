package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"
)

// Dispatcher defaults.
const (
	DefaultMaxAttempts = 5
	DefaultBackoffBase = 30 * time.Second
	maxBackoff         = time.Hour
	sendTimeout        = 30 * time.Second
	leaseDuration      = 2 * time.Minute
	pollInterval       = 2 * time.Second
	batchSize          = 20
	maxErrorChars      = 300
)

// Dispatcher sends queued jobs. A failed job is retried with exponential
// backoff; after MaxAttempts, or on a permanent error, it is dead-lettered and
// stays visible until a person retries it.
type Dispatcher struct {
	store       Store
	targets     Targets
	providers   map[string]Provider
	dailyCaps   map[string]int
	maxAttempts int
	backoffBase time.Duration
	interval    time.Duration
	now         func() time.Time
	logger      *slog.Logger
}

// NewDispatcher builds a dispatcher. providers maps a channel ID to the provider that sends it.
func NewDispatcher(store Store, targets Targets, providers map[string]Provider, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{
		store: store, targets: targets, providers: providers, logger: logger,
		maxAttempts: DefaultMaxAttempts, backoffBase: DefaultBackoffBase, interval: pollInterval, now: time.Now,
	}
}

// WithRetryPolicy overrides the attempt limit and the first backoff step.
func (d *Dispatcher) WithRetryPolicy(maxAttempts int, base time.Duration) *Dispatcher {
	if maxAttempts > 0 {
		d.maxAttempts = maxAttempts
	}
	if base > 0 {
		d.backoffBase = base
	}
	return d
}

// WithDailyCap limits how many queued messages a channel sends per UTC day.
// It caps spend on SMS. It applies to queued (standard and background) jobs
// only; critical events are delivered inline and are never capped.
func (d *Dispatcher) WithDailyCap(channel string, max int) *Dispatcher {
	if max > 0 {
		if d.dailyCaps == nil {
			d.dailyCaps = map[string]int{}
		}
		d.dailyCaps[channel] = max
	}
	return d
}

// WithClock replaces the clock; for tests.
func (d *Dispatcher) WithClock(now func() time.Time) *Dispatcher {
	d.now = now
	return d
}

// Run processes the queue until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	t := time.NewTicker(d.interval)
	defer t.Stop()
	for {
		if _, err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
			d.logger.Warn("delivery dispatcher pass failed", "error", RedactError(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce claims and sends the jobs that are due, and returns how many it handled.
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	jobs, err := d.store.Claim(ctx, d.now(), leaseDuration, batchSize)
	if err != nil {
		return 0, fmt.Errorf("claim deliveries: %w", err)
	}
	for _, j := range jobs {
		d.process(ctx, j)
	}
	return len(jobs), nil
}

func (d *Dispatcher) process(ctx context.Context, j Job) {
	err := d.send(ctx, j)
	now := d.now()
	switch {
	case err == nil:
		if e := d.store.Complete(ctx, j.ID, now); e != nil {
			d.logger.Error("mark delivery sent failed", "id", j.ID, "error", e)
		}
		return
	case errors.Is(err, ErrPermanent) || j.Attempts >= d.maxAttempts:
		d.fail(ctx, j, err, nil, now)
		d.logger.Warn("delivery dead-lettered", "id", j.ID, "channel", j.Channel, "attempts", j.Attempts, "error", RedactError(err))
	default:
		retryAt := now.Add(d.backoff(j.Attempts))
		d.fail(ctx, j, err, &retryAt, now)
		d.logger.Warn("delivery failed; will retry", "id", j.ID, "channel", j.Channel, "attempts", j.Attempts, "error", RedactError(err))
	}
}

func (d *Dispatcher) fail(ctx context.Context, j Job, cause error, retryAt *time.Time, now time.Time) {
	if e := d.store.Fail(ctx, j.ID, RedactError(cause), retryAt, now); e != nil {
		d.logger.Error("record delivery failure failed", "id", j.ID, "error", e)
	}
}

func (d *Dispatcher) send(ctx context.Context, j Job) error {
	provider, ok := d.providers[j.Channel]
	if !ok {
		return fmt.Errorf("%w: no provider for channel %q", ErrPermanent, j.Channel)
	}
	req, ok := d.targets.Request(j.Channel, j.Title, j.Message, j.Group)
	if !ok {
		return fmt.Errorf("%w: channel %q is not configured", ErrPermanent, j.Channel)
	}
	if limit := d.dailyCaps[j.Channel]; limit > 0 {
		n, err := d.store.CountSent(ctx, j.Channel, startOfDay(d.now()))
		if err != nil {
			return err
		}
		if n >= limit {
			return fmt.Errorf("%w: daily limit of %d %s messages reached", ErrPermanent, limit, j.Channel)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	return provider.Send(ctx, req)
}

// backoff doubles from the base each attempt, up to an hour.
func (d *Dispatcher) backoff(attempt int) time.Duration {
	wait := d.backoffBase
	for i := 1; i < attempt && wait < maxBackoff; i++ {
		wait *= 2
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	return wait
}

func startOfDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

var urlPattern = regexp.MustCompile(`https?://[^\s"']+`)

// RedactError returns the error text with every URL reduced to its scheme and
// host (a webhook's path is its credential) and a bounded length.
func RedactError(err error) string {
	if err == nil {
		return ""
	}
	text := urlPattern.ReplaceAllStringFunc(err.Error(), func(u string) string {
		if i := indexAfterHost(u); i > 0 {
			return u[:i] + "/…"
		}
		return u
	})
	if r := []rune(text); len(r) > maxErrorChars {
		text = string(r[:maxErrorChars]) + "…"
	}
	return text
}

// indexAfterHost returns the index of the first "/", "?" or "#" after the host.
func indexAfterHost(u string) int {
	start := 0
	for i := 0; i+2 < len(u); i++ {
		if u[i] == ':' && u[i+1] == '/' && u[i+2] == '/' {
			start = i + 3
			break
		}
	}
	for i := start; i < len(u); i++ {
		if u[i] == '/' || u[i] == '?' || u[i] == '#' {
			return i
		}
	}
	return len(u)
}
