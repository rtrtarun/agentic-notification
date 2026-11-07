// Package telemetry is a small, dependency-free slice of distributed tracing: W3C
// Trace Context (the traceparent header) and span timing.
//
// A request keeps the trace it arrived with, or starts a new one, and every step that
// matters (the pipeline, rule evaluation, deduplication, attention, delivery) is a span
// with its own ID and duration. Spans are written as structured log records, so any log
// pipeline can read them, and the trace ID is the same one the caller sent, so they join
// the caller's own traces. It does not export to an OpenTelemetry collector.
package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Header is the W3C Trace Context header.
const Header = "traceparent"

// TraceID and SpanID identify a trace and one span in it.
type (
	TraceID [16]byte
	SpanID  [8]byte
)

// SpanContext is what travels between services.
type SpanContext struct {
	TraceID TraceID
	SpanID  SpanID
	Sampled bool
}

// Valid reports whether both IDs are non-zero, as the specification requires.
func (sc SpanContext) Valid() bool { return sc.TraceID != TraceID{} && sc.SpanID != SpanID{} }

// Traceparent renders the header value, for example 00-<trace>-<span>-01.
func (sc SpanContext) Traceparent() string {
	flags := "00"
	if sc.Sampled {
		flags = "01"
	}
	return fmt.Sprintf("00-%s-%s-%s", hex.EncodeToString(sc.TraceID[:]), hex.EncodeToString(sc.SpanID[:]), flags)
}

// ParseTraceparent reads a version 00 header. Anything else, including all-zero IDs
// and upper-case hex, is rejected: a caller's malformed header must not become our trace.
func ParseTraceparent(h string) (SpanContext, bool) {
	parts := strings.Split(strings.TrimSpace(h), "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return SpanContext{}, false
	}
	for _, p := range parts[1:] {
		if strings.ToLower(p) != p {
			return SpanContext{}, false
		}
	}
	var sc SpanContext
	if _, err := hex.Decode(sc.TraceID[:], []byte(parts[1])); err != nil {
		return SpanContext{}, false
	}
	if _, err := hex.Decode(sc.SpanID[:], []byte(parts[2])); err != nil {
		return SpanContext{}, false
	}
	var flags [1]byte
	if _, err := hex.Decode(flags[:], []byte(parts[3])); err != nil {
		return SpanContext{}, false
	}
	sc.Sampled = flags[0]&1 == 1
	return sc, sc.Valid()
}

type ctxKey struct{}

// WithSpanContext stores a span context in ctx.
func WithSpanContext(ctx context.Context, sc SpanContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, sc)
}

// FromContext returns the span context in ctx, or the zero value when there is none.
func FromContext(ctx context.Context) SpanContext {
	sc, _ := ctx.Value(ctxKey{}).(SpanContext)
	return sc
}

// Tracer starts spans and writes them to a logger. A nil *Tracer is valid and does nothing,
// so instrumented code never has to check.
type Tracer struct {
	logger *slog.Logger
	now    func() time.Time
}

// NewTracer returns a tracer that logs finished spans at debug level.
func NewTracer(logger *slog.Logger) *Tracer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Tracer{logger: logger, now: time.Now}
}

// WithClock replaces the clock; tests use it.
func (t *Tracer) WithClock(now func() time.Time) *Tracer {
	if t != nil {
		t.now = now
	}
	return t
}

// Span is one timed step. End it exactly once; further calls do nothing.
type Span struct {
	tracer *Tracer
	name   string
	sc     SpanContext
	parent SpanID
	start  time.Time
	ended  atomic.Bool
	ctx    context.Context
}

// Start begins a span named name as a child of the span in ctx, or as the root of a new
// trace when ctx has none. It returns a context carrying the new span.
func (t *Tracer) Start(ctx context.Context, name string) (context.Context, *Span) {
	if t == nil {
		return ctx, &Span{}
	}
	parent := FromContext(ctx)
	sc := SpanContext{TraceID: parent.TraceID, SpanID: newSpanID(), Sampled: true}
	if parent.Valid() {
		sc.Sampled = parent.Sampled
	} else {
		sc.TraceID = newTraceID()
	}
	s := &Span{tracer: t, name: name, sc: sc, parent: parent.SpanID, start: t.now(), ctx: ctx}
	return WithSpanContext(ctx, sc), s
}

// Context is the span's own span context.
func (s *Span) Context() SpanContext { return s.sc }

// End finishes the span and logs its name, IDs, duration and, if it failed, the error.
func (s *Span) End(err error) {
	if s == nil || s.tracer == nil || !s.ended.CompareAndSwap(false, true) {
		return
	}
	attrs := []slog.Attr{
		slog.String("name", s.name),
		slog.String("trace_id", hex.EncodeToString(s.sc.TraceID[:])),
		slog.String("span_id", hex.EncodeToString(s.sc.SpanID[:])),
		slog.Float64("duration_ms", float64(s.tracer.now().Sub(s.start).Microseconds())/1000),
	}
	if s.parent != (SpanID{}) {
		attrs = append(attrs, slog.String("parent_span_id", hex.EncodeToString(s.parent[:])))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	s.tracer.logger.LogAttrs(s.ctx, slog.LevelDebug, "span", attrs...)
}

// Middleware gives every request a span. It continues a valid incoming traceparent, starts a
// new trace otherwise, and returns the request's traceparent in the response so a caller can
// find the trace. name chooses the span name from the request; keep it low-cardinality.
func (t *Tracer) Middleware(name func(*http.Request) string, next http.Handler) http.Handler {
	if t == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if incoming, ok := ParseTraceparent(r.Header.Get(Header)); ok {
			ctx = WithSpanContext(ctx, incoming)
		}
		ctx, span := t.Start(ctx, name(r))
		w.Header().Set(Header, span.Context().Traceparent())
		next.ServeHTTP(w, r.WithContext(ctx))
		span.End(nil)
	})
}

func newTraceID() TraceID {
	var id TraceID
	for id == (TraceID{}) {
		_, _ = rand.Read(id[:])
	}
	return id
}

func newSpanID() SpanID {
	var id SpanID
	for id == (SpanID{}) {
		_, _ = rand.Read(id[:])
	}
	return id
}
