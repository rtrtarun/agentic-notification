// Package llm puts a language model behind the Attention Intelligence
// interfaces (Classifier, Summarizer, Explainer).
//
// A model is an optional replacement for the built-in heuristic, never a
// requirement and never trusted:
//
//   - Critical events are never sent to a model; the heuristic path handles them.
//   - Only event facts a person would see in a title are sent, with secrets,
//     emails and long tokens redacted. Comment bodies and raw payloads are not sent.
//   - Every answer is validated. A model can only answer standard or background,
//     with a score in range; anything else is discarded.
//   - If the model is slow, down, over its rate cap, behind an open circuit
//     breaker or returns something invalid, the heuristic answers instead and the
//     event is handled as it would have been without a model.
package llm

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/event"
)

// Completer sends one prompt to a language model and returns its text.
// Implementations: ollama.Completer and claude.Client.
type Completer interface {
	// Name identifies the model, such as "ollama:llama3.2" or "claude:claude-haiku-5-5".
	Name() string
	Complete(ctx context.Context, system, user string, wantJSON bool) (string, error)
}

// Options tune how a model is called. Zero values use the defaults.
type Options struct {
	// Timeout bounds one model call (default 5s).
	Timeout time.Duration
	// MaxCallsPerMinute caps calls so a burst of events cannot run up a bill (default 60).
	MaxCallsPerMinute int
	// FailureThreshold is how many failures in a row open the breaker (default 3).
	FailureThreshold int
	// Cooldown is how long the breaker stays open before trying again (default 30s).
	Cooldown time.Duration
	Logger   *slog.Logger
	// Now is the clock; tests replace it.
	Now func() time.Time
}

const (
	defaultTimeout   = 5 * time.Second
	defaultMaxCalls  = 60
	defaultThreshold = 3
	defaultCooldown  = 30 * time.Second
)

// Errors that mean "use the heuristic instead". They are expected, so they are logged quietly.
var (
	ErrBreakerOpen = errors.New("model circuit breaker is open")
	ErrRateLimited = errors.New("model call rate cap reached")
)

// Model implements attention.Classifier, Summarizer, Explainer and SourcedExplainer.
type Model struct {
	completer Completer
	fallback  *attention.Heuristic
	opts      Options

	mu          sync.Mutex
	failures    int
	openUntil   time.Time
	calls       []time.Time // starts of calls in the last minute
	totalCalls  int
	fallbacks   int
	lastError   string
	lastErrorAt time.Time
}

// New wraps a Completer. fallback is the heuristic that answers when the model cannot.
func New(c Completer, fallback *attention.Heuristic, opts Options) *Model {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.MaxCallsPerMinute <= 0 {
		opts.MaxCallsPerMinute = defaultMaxCalls
	}
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = defaultThreshold
	}
	if opts.Cooldown <= 0 {
		opts.Cooldown = defaultCooldown
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if fallback == nil {
		fallback = attention.NewHeuristic()
	}
	return &Model{completer: c, fallback: fallback, opts: opts}
}

// Name is the model's name.
func (m *Model) Name() string { return m.completer.Name() }

// Status is a snapshot for GET /api/attention/status. It holds no prompts and no event data.
type Status struct {
	Provider    string     `json:"provider"`
	Model       string     `json:"model"`
	Breaker     string     `json:"breaker"`
	Calls       int        `json:"calls"`
	Fallbacks   int        `json:"fallbacks"`
	LastError   string     `json:"lastError,omitempty"`
	LastErrorAt *time.Time `json:"lastErrorAt,omitempty"`
}

// Status reports how the model has been doing.
func (m *Model) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	provider, _, _ := strings.Cut(m.completer.Name(), ":")
	s := Status{Provider: provider, Model: m.completer.Name(), Breaker: m.breakerState(), Calls: m.totalCalls, Fallbacks: m.fallbacks, LastError: m.lastError}
	if !m.lastErrorAt.IsZero() {
		t := m.lastErrorAt
		s.LastErrorAt = &t
	}
	return s
}

// breakerState must be called with mu held.
func (m *Model) breakerState() string {
	switch {
	case m.failures < m.opts.FailureThreshold:
		return "closed"
	case m.opts.Now().Before(m.openUntil):
		return "open"
	default:
		return "half-open"
	}
}

// admit decides whether a call may go to the model right now.
func (m *Model) admit() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.opts.Now()
	if m.failures >= m.opts.FailureThreshold && now.Before(m.openUntil) {
		return ErrBreakerOpen
	}
	cutoff := now.Add(-time.Minute)
	kept := m.calls[:0]
	for _, t := range m.calls {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	m.calls = kept
	if len(m.calls) >= m.opts.MaxCallsPerMinute {
		return ErrRateLimited
	}
	m.calls = append(m.calls, now)
	m.totalCalls++
	return nil
}

// record notes the outcome of a call that was admitted.
func (m *Model) record(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.failures = 0
		return
	}
	m.failures++
	if m.failures >= m.opts.FailureThreshold {
		m.openUntil = m.opts.Now().Add(m.opts.Cooldown)
	}
	m.lastError = truncate(err.Error(), 200)
	m.lastErrorAt = m.opts.Now()
}

func (m *Model) noteFallback(what string, err error) {
	m.mu.Lock()
	m.fallbacks++
	m.mu.Unlock()
	level := slog.LevelWarn
	if errors.Is(err, ErrBreakerOpen) || errors.Is(err, ErrRateLimited) {
		level = slog.LevelDebug
	}
	m.opts.Logger.Log(context.Background(), level, "attention model unavailable, using heuristic", "task", what, "model", m.completer.Name(), "error", err)
}

// ask runs one model call, then validates its text with parse. A transport
// failure or an invalid answer counts against the breaker; a caller that gave
// up (cancelled context) does not.
func ask[T any](ctx context.Context, m *Model, system, user string, wantJSON bool, parse func(string) (T, error)) (T, error) {
	var zero T
	if err := m.admit(); err != nil {
		return zero, err
	}
	cctx, cancel := context.WithTimeout(ctx, m.opts.Timeout)
	defer cancel()
	text, err := m.completer.Complete(cctx, system, user, wantJSON)
	if err == nil {
		var out T
		if out, err = parse(text); err == nil {
			m.record(nil)
			return out, nil
		}
	}
	if ctx.Err() == nil {
		m.record(err)
	}
	return zero, err
}

// Classify asks the model to classify a standard or background event.
func (m *Model) Classify(ctx context.Context, evt event.Event) (attention.Classification, error) {
	if evt.AttentionClass == event.AttentionCritical {
		return attention.Classification{}, attention.ErrCritical
	}
	out, err := ask(ctx, m, classifySystem, "<event>\n"+facts(evt)+"\n</event>", true, parseClassification)
	if err != nil {
		if ctx.Err() != nil {
			return attention.Classification{}, ctx.Err()
		}
		m.noteFallback("classify", err)
		return m.fallback.Classify(ctx, evt)
	}
	out.Model = m.completer.Name()
	return out, nil
}

// Summarize asks the model to title and describe the groups the heuristic built.
// The groups, counts and event IDs always come from the heuristic, so a model
// cannot invent or drop an event; it only supplies wording.
func (m *Model) Summarize(ctx context.Context, events []event.Event) (attention.Summary, error) {
	base, err := m.fallback.Summarize(ctx, events)
	if err != nil || len(base.Groups) == 0 {
		return base, err
	}
	out, err := ask(ctx, m, summarizeSystem, summarizePrompt(base, events), true, func(text string) (wording, error) {
		return parseWording(text, base)
	})
	if err != nil {
		if ctx.Err() != nil {
			return attention.Summary{}, ctx.Err()
		}
		m.noteFallback("summarize", err)
		return base, nil
	}
	base.Title, base.Text, base.Model = out.title, out.text, m.completer.Name()
	for i := range base.Groups {
		if s, ok := out.groups[base.Groups[i].Key]; ok {
			base.Groups[i].Summary = s
		}
	}
	return base, nil
}

// Explain implements attention.Explainer.
func (m *Model) Explain(ctx context.Context, evt event.Event) (string, error) {
	text, _, err := m.ExplainSourced(ctx, evt)
	return text, err
}

// ExplainSourced rewrites the heuristic's explanation in friendlier words. A
// critical event, or one with no recorded decision, is explained by the
// heuristic alone: nothing about it is sent to a model.
func (m *Model) ExplainSourced(ctx context.Context, evt event.Event) (string, string, error) {
	base, err := m.fallback.Explain(ctx, evt)
	if err != nil {
		return "", "", err
	}
	if evt.AttentionClass == event.AttentionCritical || evt.DecisionState == nil {
		return base, attention.ModelHeuristic, nil
	}
	user := "<event>\n" + facts(evt) + "\n</event>\n<explanation>\n" + redact(base) + "\n</explanation>"
	out, err := ask(ctx, m, explainSystem, user, false, parseExplanation)
	if err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		m.noteFallback("explain", err)
		return base, attention.ModelHeuristic, nil
	}
	return out, m.completer.Name(), nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
