package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// collector is a fake OTLP endpoint that answers with the given statuses in turn and records
// what it received.
type collector struct {
	mu       sync.Mutex
	statuses []int
	bodies   []string
	headers  []http.Header
	paths    []string
	calls    atomic.Int32
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	n := int(c.calls.Add(1)) - 1
	c.bodies = append(c.bodies, string(body))
	c.headers = append(c.headers, r.Header.Clone())
	c.paths = append(c.paths, r.URL.Path)
	status := http.StatusOK
	if n < len(c.statuses) {
		status = c.statuses[n]
	}
	w.WriteHeader(status)
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newExporter(t *testing.T, url string, batch, buffer int) *OTLPExporter {
	t.Helper()
	e, err := NewOTLPExporter(OTLPConfig{
		Endpoint: url, Headers: map[string]string{"X-Collector-Key": "s3cret-key"}, ServiceName: "svc", ServiceVersion: "0.39.0",
		BatchSize: batch, BufferSize: buffer, Logger: quietLogger(), Interval: 20 * time.Millisecond, Sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func sampleSpan(name string) SpanData {
	return SpanData{
		TraceID: TraceID{0xab, 1}, SpanID: SpanID{0xcd, 2}, Parent: SpanID{0xef, 3}, Name: name, Kind: KindServer,
		Start: time.Unix(1700000000, 123), End: time.Unix(1700000001, 456),
		Attributes: []Attribute{{Key: AttrHTTPRoute, Value: "/events"}, {Key: AttrHTTPStatusCode, Value: "200", Int: 200, IsInt: true}},
	}
}

func TestOTLPJSONFollowsTheEncodingRules(t *testing.T) {
	failed := sampleSpan("GET /events")
	failed.Failed = true
	root := sampleSpan("pipeline.Handle")
	root.Parent = SpanID{}
	raw, err := json.Marshal(EncodeOTLP([]SpanData{failed, root}, "svc", "0.39.0"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		`"traceId":"ab010000000000000000000000000000"`, `"spanId":"cd02000000000000"`, `"parentSpanId":"ef03000000000000"`,
		`"startTimeUnixNano":"1700000000000000123"`, `"endTimeUnixNano":"1700000001000000456"`, `"kind":2`,
		`{"key":"http.route","value":{"stringValue":"/events"}}`, `{"key":"http.response.status_code","value":{"intValue":"200"}}`,
		`"status":{"code":2,"message":"failed"}`, `{"key":"service.name","value":{"stringValue":"svc"}}`,
		`{"key":"service.version","value":{"stringValue":"0.39.0"}}`, `"scope":{"name":"agentpulse"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in\n%s", want, body)
		}
	}
	if strings.Count(body, "parentSpanId") != 1 {
		t.Fatalf("a root span has no parentSpanId:\n%s", body)
	}
	if strings.Count(body, `"status"`) != 1 {
		t.Fatalf("only a failed span carries a status:\n%s", body)
	}
}

func TestSpansAreBatchedAndSentWithHeadersAndContentType(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	e := newExporter(t, srv.URL+"/", 2, 10)
	e.Start()
	for i := 0; i < 3; i++ {
		e.Export(sampleSpan("s"))
	}
	if err := e.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	sent, dropped, failedN := e.Stats()
	if sent != 3 || dropped != 0 || failedN != 0 {
		t.Fatalf("stats %d %d %d", sent, dropped, failedN)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) != 2 {
		t.Fatalf("3 spans in batches of 2 make 2 requests, got %d", len(c.bodies))
	}
	if c.paths[0] != "/v1/traces" || c.headers[0].Get("Content-Type") != "application/json" || c.headers[0].Get("X-Collector-Key") != "s3cret-key" {
		t.Fatalf("path %s headers %v", c.paths[0], c.headers[0])
	}
}

func TestOnlyNetworkErrorsTooManyRequestsAndServerErrorsAreRetriedOnce(t *testing.T) {
	cases := []struct {
		name     string
		statuses []int
		calls    int32
		sent     int64
		failed   int64
	}{
		{"503 then success", []int{503, 200}, 2, 1, 0},
		{"429 then success", []int{429, 200}, 2, 1, 0},
		{"500 twice", []int{500, 502}, 2, 0, 1},
		{"400 is never retried", []int{400, 200}, 1, 0, 1},
		{"401 is never retried", []int{401, 200}, 1, 0, 1},
	}
	for _, tc := range cases {
		c := &collector{statuses: tc.statuses}
		srv := httptest.NewServer(c)
		e := newExporter(t, srv.URL, 1, 4)
		e.send([]SpanData{sampleSpan("s")})
		sent, _, failedN := e.Stats()
		if c.calls.Load() != tc.calls || sent != tc.sent || failedN != tc.failed {
			t.Errorf("%s: calls %d sent %d failed %d", tc.name, c.calls.Load(), sent, failedN)
		}
		srv.Close()
	}

	// A network error is retried once, then counted as failed.
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	var slept int
	e, err := NewOTLPExporter(OTLPConfig{Endpoint: addr, Logger: quietLogger(), Sleep: func(time.Duration) { slept++ }})
	if err != nil {
		t.Fatal(err)
	}
	e.send([]SpanData{sampleSpan("s"), sampleSpan("t")})
	if _, _, failedN := e.Stats(); failedN != 2 || slept != 1 {
		t.Fatalf("failed %d slept %d", failedN, slept)
	}
	if !retryable(0, errors.New("dial")) || retryable(404, nil) || !retryable(504, nil) {
		t.Fatal("retry rule")
	}
}

func TestExportNeverBlocksAndAFullBufferDropsAndCounts(t *testing.T) {
	e := newExporter(t, "http://127.0.0.1:1", 1, 2) // never started: nothing drains the buffer
	start := time.Now()
	for i := 0; i < 10; i++ {
		e.Export(sampleSpan("s"))
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("Export must not block")
	}
	if _, dropped, _ := e.Stats(); dropped != 8 {
		t.Fatalf("dropped %d, want 8", dropped)
	}
}

func TestAHangingCollectorNeverHoldsUpExportOrShutdown(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	e := newExporter(t, srv.URL, 1, 10)
	e.Start()
	start := time.Now()
	for i := 0; i < 5; i++ {
		e.Export(sampleSpan("s"))
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("Export waited for the collector")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start = time.Now()
	_ = e.Shutdown(ctx)
	if time.Since(start) > time.Second {
		t.Fatal("Shutdown must give up at its deadline")
	}
}

func TestEndpointsAreCheckedAndCredentialsBelongInHeaders(t *testing.T) {
	for _, bad := range []string{"", "localhost:4318", "ftp://x", "http://user:pw@host:4318", "http://host:4318?k=v", "http://host#x"} {
		if _, err := ParseOTLPEndpoint(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	got, err := ParseOTLPEndpoint(" https://collector.example.com:4318/ ")
	if err != nil || got != "https://collector.example.com:4318" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := NewOTLPExporter(OTLPConfig{Endpoint: "http://h", BatchSize: 5, BufferSize: 2}); err == nil {
		t.Fatal("a batch larger than the buffer is refused")
	}
	e, err := NewOTLPExporter(OTLPConfig{Endpoint: "http://h"})
	if err != nil || e.cfg.BatchSize != DefaultExportBatch || e.cfg.BufferSize != DefaultExportBuffer || e.cfg.ServiceName != "notification-service" {
		t.Fatalf("defaults %+v %v", e, err)
	}
}

// captured records exported spans in memory.
type captured struct {
	mu    sync.Mutex
	spans []SpanData
}

func (c *captured) Export(s SpanData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spans = append(c.spans, s)
}

func TestOnlySampledSpansAreExportedAndAttributesAreAllowListed(t *testing.T) {
	out := &captured{}
	tr := NewTracer(quietLogger()).WithExporter(out)

	unsampled := WithSpanContext(context.Background(), SpanContext{TraceID: TraceID{1}, SpanID: SpanID{1}, Sampled: false})
	_, s := tr.Start(unsampled, "quiet")
	s.End(nil)
	if len(out.spans) != 0 {
		t.Fatal("a caller that asked for no sampling gets no exported span")
	}

	_, s = tr.Start(context.Background(), "pipeline.Handle")
	s.SetAttribute(AttrEventType, "push")
	s.SetAttribute("payload.password", "hunter2")
	s.SetAttribute(AttrRuleName, "")
	s.SetAttribute(AttrAgentTraceID, strings.Repeat("é", 300))
	s.SetIntAttribute("http.request.body", 7)
	s.End(errors.New("smtp: auth failed for ops@example.com with password hunter2"))
	if len(out.spans) != 1 {
		t.Fatalf("spans %d", len(out.spans))
	}
	got := out.spans[0]
	if !got.Failed || got.Kind != KindInternal || len(got.Attributes) != 2 {
		t.Fatalf("span %+v", got)
	}
	if n := len([]rune(got.Attributes[1].Value)); n != maxAttributeRunes {
		t.Fatalf("a long value is cut to %d characters, got %d", maxAttributeRunes, n)
	}
	raw, _ := json.Marshal(EncodeOTLP(out.spans, "svc", ""))
	for _, secret := range []string{"hunter2", "ops@example.com", "smtp", "password"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%q leaked into the export:\n%s", secret, raw)
		}
	}
	var nilSpan *Span
	nilSpan.SetAttribute(AttrEventType, "x")
	nilSpan.SetIntAttribute(AttrHTTPStatusCode, 1)
}

func TestTheServerSpanNamesTheRouteRecordsTheStatusAndSkipsUntracedRequests(t *testing.T) {
	out := &captured{}
	tr := NewTracer(quietLogger()).WithExporter(out)
	h := tr.Middleware(func(r *http.Request) string {
		if r.URL.Path == "/health" {
			return ""
		}
		return "/api/rules"
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/rules/boom" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
		w.(http.Flusher).Flush()
	}))
	for _, path := range []string{"/api/rules/7", "/api/rules/boom", "/health"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, nil))
		if (rr.Header().Get(Header) == "") != (path == "/health") {
			t.Fatalf("%s: traceparent %q", path, rr.Header().Get(Header))
		}
	}
	if len(out.spans) != 2 {
		t.Fatalf("spans %d", len(out.spans))
	}
	ok, boom := out.spans[0], out.spans[1]
	if ok.Name != "POST /api/rules" || ok.Kind != KindServer || ok.Failed || boom.Failed != true {
		t.Fatalf("%+v %+v", ok, boom)
	}
	want := map[string]string{AttrHTTPMethod: "POST", AttrHTTPRoute: "/api/rules", AttrHTTPStatusCode: "200"}
	for _, a := range ok.Attributes {
		if want[a.Key] != a.Value {
			t.Errorf("%s = %s", a.Key, a.Value)
		}
		delete(want, a.Key)
	}
	if len(want) != 0 {
		t.Fatalf("missing %v", want)
	}
	sw := &statusWriter{ResponseWriter: httptest.NewRecorder()}
	if sw.code() != http.StatusOK || sw.Unwrap() == nil {
		t.Fatal("status writer defaults")
	}
}
