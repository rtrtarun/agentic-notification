package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	goodTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	goodSpan  = "00f067aa0ba902b7"
	good      = "00-" + goodTrace + "-" + goodSpan + "-01"
)

func TestParseTraceparentAcceptsAWellFormedHeaderAndRoundTrips(t *testing.T) {
	sc, ok := ParseTraceparent(good)
	if !ok || !sc.Sampled || sc.Traceparent() != good {
		t.Fatalf("got %+v %v %s", sc, ok, sc.Traceparent())
	}
	if unsampled, ok := ParseTraceparent("00-" + goodTrace + "-" + goodSpan + "-00"); !ok || unsampled.Sampled || !strings.HasSuffix(unsampled.Traceparent(), "-00") {
		t.Fatalf("unsampled = %+v %v", unsampled, ok)
	}
	if _, ok := ParseTraceparent("  " + good + "  "); !ok {
		t.Fatal("surrounding whitespace should be tolerated")
	}
}

func TestParseTraceparentRejectsEverythingElse(t *testing.T) {
	for name, h := range map[string]string{
		"empty":          "",
		"wrong version":  "ff-" + goodTrace + "-" + goodSpan + "-01",
		"future version": "01-" + goodTrace + "-" + goodSpan + "-01",
		"too few parts":  "00-" + goodTrace + "-" + goodSpan,
		"too many parts": good + "-extra",
		"short trace id": "00-abc-" + goodSpan + "-01",
		"short span id":  "00-" + goodTrace + "-abc-01",
		"zero trace id":  "00-00000000000000000000000000000000-" + goodSpan + "-01",
		"zero span id":   "00-" + goodTrace + "-0000000000000000-01",
		"upper case":     "00-" + strings.ToUpper(goodTrace) + "-" + goodSpan + "-01",
		"not hex trace":  "00-" + strings.Repeat("g", 32) + "-" + goodSpan + "-01",
		"not hex span":   "00-" + goodTrace + "-" + strings.Repeat("z", 16) + "-01",
		"not hex flags":  "00-" + goodTrace + "-" + goodSpan + "-zz",
		"long flags":     "00-" + goodTrace + "-" + goodSpan + "-001",
	} {
		if _, ok := ParseTraceparent(h); ok {
			t.Errorf("%s: accepted %q", name, h)
		}
	}
}

func recorder() (*Tracer, *bytes.Buffer, *time.Time) {
	var buf bytes.Buffer
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewTracer(logger).WithClock(func() time.Time { return now }), &buf, &now
}

func TestAChildSpanSharesTheTraceAndNamesItsParent(t *testing.T) {
	tr, buf, now := recorder()
	ctx, root := tr.Start(context.Background(), "pipeline.Handle")
	ctx2, child := tr.Start(ctx, "policy.Evaluate")
	if root.Context().TraceID != child.Context().TraceID || root.Context().SpanID == child.Context().SpanID || !root.Context().Valid() {
		t.Fatalf("root %+v child %+v", root.Context(), child.Context())
	}
	if FromContext(ctx2).SpanID != child.Context().SpanID {
		t.Fatal("the returned context must carry the new span")
	}
	*now = now.Add(12*time.Millisecond + 500*time.Microsecond)
	child.End(nil)
	root.End(errors.New("boom"))
	out := buf.String()
	if strings.Count(out, "msg=span") != 2 {
		t.Fatalf("two spans expected:\n%s", out)
	}
	for _, want := range []string{"name=policy.Evaluate", "name=pipeline.Handle", "duration_ms=12.5", "parent_span_id=" + hexOf(root.Context().SpanID), "error=boom", "trace_id=" + hexOf16(root.Context().TraceID)} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Count(out, "parent_span_id") != 1 {
		t.Fatalf("a root span has no parent:\n%s", out)
	}
}

func hexOf(id SpanID) string {
	return strings.TrimSpace(strings.Split(SpanContext{SpanID: id}.Traceparent(), "-")[2])
}
func hexOf16(id TraceID) string { return strings.Split(SpanContext{TraceID: id}.Traceparent(), "-")[1] }

func TestEndingASpanTwiceLogsItOnce(t *testing.T) {
	tr, buf, _ := recorder()
	_, s := tr.Start(context.Background(), "x")
	s.End(nil)
	s.End(errors.New("late"))
	if strings.Count(buf.String(), "msg=span") != 1 || strings.Contains(buf.String(), "late") {
		t.Fatalf("log:\n%s", buf.String())
	}
}

func TestANilTracerAndSpanAreHarmless(t *testing.T) {
	var tr *Tracer
	ctx, span := tr.Start(context.Background(), "x")
	span.End(errors.New("ignored"))
	var nilSpan *Span
	nilSpan.End(nil)
	if FromContext(ctx).Valid() {
		t.Fatal("a nil tracer must not invent a trace")
	}
	if got := tr.Middleware(func(*http.Request) string { return "x" }, http.NotFoundHandler()); got == nil {
		t.Fatal("Middleware on a nil tracer must return the handler")
	}
	if tr.WithClock(time.Now) != nil {
		t.Fatal("WithClock on nil returns nil")
	}
}

func TestMiddlewareContinuesACallersTraceAndEchoesTheRequestsSpan(t *testing.T) {
	tr, buf, _ := recorder()
	var seen SpanContext
	h := tr.Middleware(func(r *http.Request) string { return r.Method + " /x" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = FromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(Header, good)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	echoed, ok := ParseTraceparent(rr.Header().Get(Header))
	if !ok || hexOf16(echoed.TraceID) != goodTrace || hexOf(echoed.SpanID) == goodSpan || echoed.SpanID != seen.SpanID {
		t.Fatalf("response traceparent = %q (handler saw %+v)", rr.Header().Get(Header), seen)
	}
	if !strings.Contains(buf.String(), "parent_span_id="+goodSpan) || !strings.Contains(buf.String(), "name=\"GET /x\"") {
		t.Fatalf("the request span should be a child of the caller's:\n%s", buf.String())
	}
}

func TestMiddlewareStartsANewTraceForAMissingOrBrokenHeader(t *testing.T) {
	tr, _, _ := recorder()
	h := tr.Middleware(func(*http.Request) string { return "x" }, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, header := range []string{"", "garbage", "00-" + strings.Repeat("0", 32) + "-" + goodSpan + "-01"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set(Header, header)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		sc, ok := ParseTraceparent(rr.Header().Get(Header))
		if !ok || hexOf16(sc.TraceID) == goodTrace || sc.TraceID == (TraceID{}) {
			t.Errorf("header %q: response %q must carry a fresh, valid trace", header, rr.Header().Get(Header))
		}
	}
}

func TestAnUnsampledCallersDecisionIsKept(t *testing.T) {
	tr, _, _ := recorder()
	ctx := WithSpanContext(context.Background(), SpanContext{TraceID: TraceID{1}, SpanID: SpanID{2}, Sampled: false})
	_, s := tr.Start(ctx, "x")
	if s.Context().Sampled {
		t.Fatal("a child of an unsampled span stays unsampled")
	}
}
