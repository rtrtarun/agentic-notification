package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// OTLP export defaults. The interval, timeout and retry delay are fixed: they bound how much a
// collector can cost and are not worth a setting each.
const (
	DefaultExportBatch  = 100
	DefaultExportBuffer = 2048
	MaxExportBatch      = 1000
	MaxExportBuffer     = 100000
	exportInterval      = 2 * time.Second
	exportTimeout       = 5 * time.Second
	retryDelay          = time.Second
	tracesPath          = "/v1/traces"
	scopeName           = "agentpulse"
	statusCodeError     = 2
)

// OTLPConfig configures an OTLPExporter.
type OTLPConfig struct {
	// Endpoint is the collector's base URL, as OTEL_EXPORTER_OTLP_ENDPOINT; /v1/traces is added.
	Endpoint string
	// Headers are sent with every export (for example an API key of a hosted collector). They are
	// never logged.
	Headers        map[string]string
	ServiceName    string
	ServiceVersion string
	// BatchSize is the most spans one request carries; BufferSize the most spans waiting.
	BatchSize, BufferSize int
	Logger                *slog.Logger
	// Client, Interval and Sleep are for tests; zero values use the defaults.
	Client   *http.Client
	Interval time.Duration
	Sleep    func(time.Duration)
}

// OTLPExporter sends finished spans to an OpenTelemetry collector as OTLP/HTTP JSON.
//
// Export never blocks: a span goes into a bounded buffer, and when the buffer is full the span
// is dropped and counted. A background loop sends batches on an interval or when a batch fills.
// A request is retried once, and only after a network error, 429 or a 5xx; any other answer
// counts the batch as failed. Nothing here can slow or fail the request that ended the span.
type OTLPExporter struct {
	cfg      OTLPConfig
	url      string
	queue    chan SpanData
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	sent     atomic.Int64
	dropped  atomic.Int64
	failed   atomic.Int64
}

// ParseOTLPEndpoint checks a collector base URL: http or https, a host, and no user
// information, query or fragment (credentials go in the headers, which are never logged).
func ParseOTLPEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("must be an http or https URL such as http://localhost:4318")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("must not contain a user, password, query or fragment; put credentials in OTEL_EXPORTER_OTLP_HEADERS")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// NewOTLPExporter validates cfg and returns an exporter. Call Start to begin sending.
func NewOTLPExporter(cfg OTLPConfig) (*OTLPExporter, error) {
	base, err := ParseOTLPEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("OTLP endpoint %w", err)
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultExportBatch
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = DefaultExportBuffer
	}
	if cfg.BatchSize > cfg.BufferSize {
		return nil, errors.New("OTLP batch size must not be larger than the buffer")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: exportTimeout}
	}
	if cfg.Interval <= 0 {
		cfg.Interval = exportInterval
	}
	if cfg.Sleep == nil {
		cfg.Sleep = time.Sleep
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "notification-service"
	}
	return &OTLPExporter{
		cfg: cfg, url: base + tracesPath, queue: make(chan SpanData, cfg.BufferSize),
		stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

// Export queues a span, or drops and counts it when the buffer is full. It never blocks.
func (e *OTLPExporter) Export(s SpanData) {
	select {
	case e.queue <- s:
	default:
		e.dropped.Add(1)
	}
}

// Start runs the sending loop in the background until Shutdown.
func (e *OTLPExporter) Start() {
	go e.run()
}

// Shutdown stops the loop and sends what is still buffered, within ctx's deadline, then logs
// how many spans were sent, dropped and failed.
func (e *OTLPExporter) Shutdown(ctx context.Context) error {
	e.stopOnce.Do(func() { close(e.stop) })
	select {
	case <-e.done:
	case <-ctx.Done():
	}
	sent, dropped, failed := e.Stats()
	e.cfg.Logger.Info("span export stopped", "sent", sent, "dropped", dropped, "failed", failed)
	return nil
}

// Stats returns how many spans were sent, dropped because the buffer was full, and failed.
func (e *OTLPExporter) Stats() (sent, dropped, failed int64) {
	return e.sent.Load(), e.dropped.Load(), e.failed.Load()
}

func (e *OTLPExporter) run() {
	defer close(e.done)
	ticker := time.NewTicker(e.cfg.Interval)
	defer ticker.Stop()
	batch := make([]SpanData, 0, e.cfg.BatchSize)
	flush := func() {
		if len(batch) > 0 {
			e.send(batch)
			batch = make([]SpanData, 0, e.cfg.BatchSize)
		}
	}
	for {
		select {
		case s := <-e.queue:
			batch = append(batch, s)
			if len(batch) == e.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-e.stop:
			for {
				select {
				case s := <-e.queue:
					batch = append(batch, s)
					if len(batch) == e.cfg.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// send posts one batch, retrying once after a network error, 429 or 5xx.
func (e *OTLPExporter) send(batch []SpanData) {
	body, err := json.Marshal(EncodeOTLP(batch, e.cfg.ServiceName, e.cfg.ServiceVersion))
	if err != nil {
		e.failed.Add(int64(len(batch)))
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		status, err := e.post(body)
		switch {
		case err == nil && status >= 200 && status < 300:
			e.sent.Add(int64(len(batch)))
			return
		case attempt == 0 && retryable(status, err):
			e.cfg.Sleep(retryDelay)
			continue
		}
		e.failed.Add(int64(len(batch)))
		// The status and the kind of failure only: never the headers, which can hold credentials.
		e.cfg.Logger.Warn("span export failed", "spans", len(batch), "status", status, "network_error", err != nil)
		return
	}
}

func retryable(status int, err error) bool {
	return err != nil || status == http.StatusTooManyRequests || status >= 500
}

func (e *OTLPExporter) post(body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := e.cfg.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, nil
}

// EncodeOTLP builds the OTLP/HTTP JSON body for spans: ids as lower-case hex, times as
// nanoseconds since the epoch written as strings, kinds and status codes as numbers. The Java
// service writes the same shape.
func EncodeOTLP(spans []SpanData, serviceName, serviceVersion string) map[string]any {
	resource := []map[string]any{stringAttr("service.name", serviceName)}
	if serviceVersion != "" {
		resource = append(resource, stringAttr("service.version", serviceVersion))
	}
	out := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		span := map[string]any{
			"traceId":           hex.EncodeToString(s.TraceID[:]),
			"spanId":            hex.EncodeToString(s.SpanID[:]),
			"name":              s.Name,
			"kind":              int(s.Kind),
			"startTimeUnixNano": strconv.FormatInt(s.Start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(s.End.UnixNano(), 10),
		}
		if s.Parent != (SpanID{}) {
			span["parentSpanId"] = hex.EncodeToString(s.Parent[:])
		}
		attrs := make([]map[string]any, 0, len(s.Attributes))
		for _, a := range s.Attributes {
			if a.IsInt {
				attrs = append(attrs, map[string]any{"key": a.Key, "value": map[string]any{"intValue": strconv.FormatInt(a.Int, 10)}})
			} else {
				attrs = append(attrs, stringAttr(a.Key, a.Value))
			}
		}
		span["attributes"] = attrs
		if s.Failed {
			span["status"] = map[string]any{"code": statusCodeError, "message": FailedStatusMessage}
		}
		out = append(out, span)
	}
	return map[string]any{"resourceSpans": []map[string]any{{
		"resource":   map[string]any{"attributes": resource},
		"scopeSpans": []map[string]any{{"scope": map[string]any{"name": scopeName}, "spans": out}},
	}}}
}

func stringAttr(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}
