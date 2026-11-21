// Package telemetry is a small, dependency-free slice of distributed tracing: W3C
// Trace Context (the traceparent header) and span timing.
//
// A request keeps the trace it arrived with, or starts a new one, and every step that
// matters (the pipeline, rule evaluation, deduplication, attention, delivery) is a span
// with its own ID and duration. Spans are written as structured log records, so any log
// pipeline can read them, and the trace ID is the same one the caller sent, so they join
// the caller's own traces. With an Exporter (see OTLPExporter) sampled spans are also sent
// to an OpenTelemetry collector; the exporter never blocks the code that ends a span.
//
// A span carries only allow-listed attributes (see SetAttribute): names, ids, classes and
// status codes, never payloads, titles, recipients, keys or error text.
package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
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

// SpanKind says what a span is, as OpenTelemetry numbers it.
type SpanKind int

// Span kinds used here.
const (
	KindInternal SpanKind = 1
	KindServer   SpanKind = 2
)

// Attribute keys a span may carry. Anything else is ignored, so a careless caller cannot
// put a payload, a recipient or a secret into an exported span.
const (
	AttrHTTPMethod     = "http.request.method"
	AttrHTTPRoute      = "http.route"
	AttrHTTPStatusCode = "http.response.status_code"
	AttrEventSource    = "event.source"
	AttrEventType      = "event.type"
	AttrAttentionClass = "attention.class"
	AttrDecisionStatus = "decision.status"
	AttrRuleName       = "rule.name"
	AttrAgentTraceID   = "agent.trace_id"
)

var allowedAttributes = map[string]bool{
	AttrHTTPMethod: true, AttrHTTPRoute: true, AttrHTTPStatusCode: true, AttrEventSource: true, AttrEventType: true,
	AttrAttentionClass: true, AttrDecisionStatus: true, AttrRuleName: true, AttrAgentTraceID: true,
}

// maxAttributeRunes bounds an attribute value, so one span cannot grow without limit.
const maxAttributeRunes = 200

// FailedStatusMessage is the only status message an exported span carries: an error's own
// text can hold addresses, hosts or secrets, so it stays in the local log.
const FailedStatusMessage = "failed"

// Attribute is one allow-listed key and its value; IsInt marks an integer value.
type Attribute struct {
	Key   string
	Value string
	Int   int64
	IsInt bool
}

// SpanData is a finished span as an exporter receives it.
type SpanData struct {
	TraceID    TraceID
	SpanID     SpanID
	Parent     SpanID
	Name       string
	Kind       SpanKind
	Start, End time.Time
	Attributes []Attribute
	Failed     bool
}

// Exporter receives finished, sampled spans. Export must not block.
type Exporter interface {
	Export(SpanData)
}

// Tracer starts spans and writes them to a logger. A nil *Tracer is valid and does nothing,
// so instrumented code never has to check.
type Tracer struct {
	logger   *slog.Logger
	now      func() time.Time
	exporter Exporter
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

// WithExporter also sends every finished, sampled span to e.
func (t *Tracer) WithExporter(e Exporter) *Tracer {
	if t != nil {
		t.exporter = e
	}
	return t
}

// Span is one timed step. End it exactly once; further calls do nothing. Set attributes
// from the goroutine that owns the span, before End.
type Span struct {
	tracer *Tracer
	name   string
	kind   SpanKind
	sc     SpanContext
	parent SpanID
	start  time.Time
	ended  atomic.Bool
	ctx    context.Context
	attrs  []Attribute
}

// Start begins an internal span named name as a child of the span in ctx, or as the root of
// a new trace when ctx has none. It returns a context carrying the new span.
func (t *Tracer) Start(ctx context.Context, name string) (context.Context, *Span) {
	return t.start(ctx, name, KindInternal)
}

func (t *Tracer) start(ctx context.Context, name string, kind SpanKind) (context.Context, *Span) {
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
	s := &Span{tracer: t, name: name, kind: kind, sc: sc, parent: parent.SpanID, start: t.now(), ctx: ctx}
	return WithSpanContext(ctx, sc), s
}

// SetAttribute records key=value on the span when key is allow-listed and value is not empty;
// anything else is ignored. Long values are cut to maxAttributeRunes characters.
func (s *Span) SetAttribute(key, value string) {
	if s == nil || s.tracer == nil || !allowedAttributes[key] || value == "" {
		return
	}
	if utf8.RuneCountInString(value) > maxAttributeRunes {
		value = string([]rune(value)[:maxAttributeRunes])
	}
	s.attrs = append(s.attrs, Attribute{Key: key, Value: value})
}

// SetIntAttribute records an allow-listed integer attribute.
func (s *Span) SetIntAttribute(key string, value int64) {
	if s == nil || s.tracer == nil || !allowedAttributes[key] {
		return
	}
	s.attrs = append(s.attrs, Attribute{Key: key, Value: strconv.FormatInt(value, 10), Int: value, IsInt: true})
}

// Context is the span's own span context.
func (s *Span) Context() SpanContext { return s.sc }

// End finishes the span and logs its name, IDs, duration and, if it failed, the error. A
// sampled span also goes to the exporter, marked failed but without the error's text.
func (s *Span) End(err error) {
	if s == nil || s.tracer == nil || !s.ended.CompareAndSwap(false, true) {
		return
	}
	end := s.tracer.now()
	if s.tracer.exporter != nil && s.sc.Sampled {
		s.tracer.exporter.Export(SpanData{
			TraceID: s.sc.TraceID, SpanID: s.sc.SpanID, Parent: s.parent, Name: s.name, Kind: s.kind,
			Start: s.start, End: end, Attributes: s.attrs, Failed: err != nil,
		})
	}
	attrs := []slog.Attr{
		slog.String("name", s.name),
		slog.String("trace_id", hex.EncodeToString(s.sc.TraceID[:])),
		slog.String("span_id", hex.EncodeToString(s.sc.SpanID[:])),
		slog.Float64("duration_ms", float64(end.Sub(s.start).Microseconds())/1000),
	}
	if s.parent != (SpanID{}) {
		attrs = append(attrs, slog.String("parent_span_id", hex.EncodeToString(s.parent[:])))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	s.tracer.logger.LogAttrs(s.ctx, slog.LevelDebug, "span", attrs...)
}

// Middleware gives every request a server span named "<method> <route>". It continues a valid
// incoming traceparent (keeping its sampled flag), starts a new sampled trace otherwise, and
// returns the request's traceparent in the response so a caller can find the trace. route
// chooses a low-cardinality route label from the request; an empty label means the request is
// not traced at all (no span and no header), as for /health. A 5xx answer marks the span failed.
func (t *Tracer) Middleware(route func(*http.Request) string, next http.Handler) http.Handler {
	if t == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label := route(r)
		if label == "" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		if incoming, ok := ParseTraceparent(r.Header.Get(Header)); ok {
			ctx = WithSpanContext(ctx, incoming)
		}
		ctx, span := t.start(ctx, r.Method+" "+label, KindServer)
		span.SetAttribute(AttrHTTPMethod, r.Method)
		span.SetAttribute(AttrHTTPRoute, label)
		w.Header().Set(Header, span.Context().Traceparent())
		recorder := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r.WithContext(ctx))
		status := recorder.code()
		span.SetIntAttribute(AttrHTTPStatusCode, int64(status))
		var failed error
		if status >= http.StatusInternalServerError {
			failed = errServerError
		}
		span.End(failed)
	})
}

var errServerError = errors.New("server error")

// statusWriter remembers the status code a handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Flush lets streaming handlers (server-sent events) keep working through the recorder.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) code() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
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
