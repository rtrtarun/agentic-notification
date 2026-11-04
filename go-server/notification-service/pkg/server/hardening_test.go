package server

import (
	"bytes"
	"context"
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

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/metrics"
	"github.com/example/notification-service/pkg/notification"
	"github.com/example/notification-service/pkg/pipeline"
	"github.com/example/notification-service/pkg/policy"
)

const testAPIKey = "test-key"

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type countingNotifier struct{ calls atomic.Int32 }

func (c *countingNotifier) Send(context.Context, notification.NotificationRequest) error {
	c.calls.Add(1)
	return nil
}

func apiRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-API-Key", testAPIKey)
	return req
}

func newAPIServer(cfg config.Config, svc notification.NotificationService) *Server {
	cfg.APIKey = testAPIKey
	return &Server{cfg: &cfg, logger: quietLogger(), service: svc}
}

func TestNotifyRejectsOversizedBody(t *testing.T) {
	svc := &countingNotifier{}
	s := newAPIServer(config.Config{MaxBodyBytes: 64}, svc)

	big := `{"agent":{"name":"a"},"title":"t","message":"` + strings.Repeat("x", 200) + `"}`
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, apiRequest(http.MethodPost, "/notify", big))

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
	if svc.calls.Load() != 0 {
		t.Fatal("oversized request must not reach the service")
	}
}

func TestFeedbackRejectsOversizedBody(t *testing.T) {
	s := newAPIServer(config.Config{MaxBodyBytes: 32}, &countingNotifier{})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, apiRequest(http.MethodPost, "/feedback", `{"eventId":"`+strings.Repeat("x", 100)+`"}`))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
}

func TestNotifyRejectsMalformedJSON(t *testing.T) {
	s := newAPIServer(config.Config{MaxBodyBytes: 1024}, &countingNotifier{})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, apiRequest(http.MethodPost, "/notify", `{not json`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// The full /notify path with the production validation wrapper: caller-supplied
// internal URLs and header-injection titles must be rejected with 400, and the
// delivery layer must never see them.
func TestNotifyRejectsSSRFAndInjectionBeforeDelivery(t *testing.T) {
	inner := &countingNotifier{}
	svc := notification.NewValidatingService(inner, notification.Limits{
		AllowedWebhookHosts: []string{"hooks.slack.com"},
		AllowedEmailDomains: []string{"example.com"},
	})
	s := newAPIServer(config.Config{MaxBodyBytes: 4096}, svc)

	tests := []struct{ name, body string }{
		{"cloud metadata url", `{"agent":{"name":"a"},"title":"t","slackWebhookUrls":["https://169.254.169.254/latest/meta-data"]}`},
		{"plain http", `{"agent":{"name":"a"},"title":"t","slackWebhookUrls":["http://hooks.slack.com/x"]}`},
		{"localhost", `{"agent":{"name":"a"},"title":"t","teamsWebhookUrls":["https://localhost/x"]}`},
		{"foreign recipient", `{"agent":{"name":"a"},"title":"t","emailRecipients":["x@evil.com"]}`},
		{"subject injection", `{"agent":{"name":"a"},"title":"hi\r\nBcc: x@evil.com"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.Handler().ServeHTTP(rr, apiRequest(http.MethodPost, "/notify", tt.body))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", rr.Code, rr.Body.String())
			}
		})
	}
	if inner.calls.Load() != 0 {
		t.Fatal("rejected requests must never reach delivery")
	}

	ok := httptest.NewRecorder()
	s.Handler().ServeHTTP(ok, apiRequest(http.MethodPost, "/notify",
		`{"agent":{"name":"a"},"title":"t","slackWebhookUrls":["https://hooks.slack.com/services/T/B/x"],"emailRecipients":["ops@example.com"]}`))
	if ok.Code != http.StatusAccepted || inner.calls.Load() != 1 {
		t.Fatalf("valid request: status=%d calls=%d", ok.Code, inner.calls.Load())
	}
}

func TestNotifyCountsAcceptedNotifications(t *testing.T) {
	s := newAPIServer(config.Config{MaxBodyBytes: 4096}, &countingNotifier{})
	s.metrics = metrics.NewRegistry()
	s.Handler().ServeHTTP(httptest.NewRecorder(),
		apiRequest(http.MethodPost, "/notify", `{"agent":{"name":"a"},"title":"t"}`))
	if s.metrics.Notifications() != 1 {
		t.Fatalf("Notifications() = %d, want 1", s.metrics.Notifications())
	}
}

// --- webhook idempotency ---------------------------------------------------

type webhookHarness struct {
	server   *Server
	repo     *event.InMemoryRepository
	provider *recordingNotifier
}

type recordingNotifier struct {
	mu   sync.Mutex
	sent []notification.NotificationRequest
}

func (r *recordingNotifier) Send(_ context.Context, req notification.NotificationRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, req)
	return nil
}
func (r *recordingNotifier) Name() string { return "recording" }
func (r *recordingNotifier) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func newWebhookHarness(rules []policy.Rule) *webhookHarness {
	repo := event.NewInMemoryRepository()
	provider := &recordingNotifier{}
	return &webhookHarness{
		repo:     repo,
		provider: provider,
		server: &Server{
			cfg:    &config.Config{AllowUnsignedWebhooks: true, MaxBodyBytes: 4096},
			logger: quietLogger(),
			eventPipeline: pipeline.New(policy.NewEngine(rules), delivery.NewService(provider), repo,
				pipeline.Config{DefaultEmailRecipients: []string{"ops@example.com"}}),
		},
	}
}

func (h *webhookHarness) post(eventType, deliveryID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(body))
	req.Header.Set(githubEventHeader, eventType)
	if deliveryID != "" {
		req.Header.Set(githubDeliveryHdr, deliveryID)
	}
	rr := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rr, req)
	return rr
}

func TestWebhookRedeliveryIsAcknowledgedWithoutSecondNotification(t *testing.T) {
	h := newWebhookHarness([]policy.Rule{{
		Name: "sec", When: policy.RuleCondition{Source: "github", EventType: "security_advisory"},
		Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "vuln"},
	}})

	first := h.post("security_advisory", "guid-1", `{"repository":{"full_name":"o/r"}}`)
	second := h.post("security_advisory", "guid-1", `{"repository":{"full_name":"o/r"}}`)

	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d (%s)", first.Code, first.Body.String())
	}
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "duplicate") {
		t.Fatalf("redelivery should be acknowledged as duplicate, got %d %s", second.Code, second.Body.String())
	}
	if h.provider.count() != 1 {
		t.Fatalf("critical alert sent %d times, want exactly 1", h.provider.count())
	}
}

func TestWebhookDistinctDeliveriesAreBothProcessed(t *testing.T) {
	h := newWebhookHarness(nil)
	a := h.post("star", "guid-a", `{}`)
	b := h.post("star", "guid-b", `{}`)
	if a.Code != http.StatusAccepted || b.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d", a.Code, b.Code)
	}
	if stored, _ := h.repo.List(context.Background()); len(stored) != 2 {
		t.Fatalf("expected 2 stored events, got %d", len(stored))
	}
}

func TestWebhookWithoutDeliveryIDGetsUniqueIDs(t *testing.T) {
	h := newWebhookHarness(nil)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		rr := h.post("star", "", `{}`)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d", rr.Code)
		}
	}
	stored, _ := h.repo.List(context.Background())
	for _, e := range stored {
		if seen[e.ID] {
			t.Fatalf("duplicate event id %s", e.ID)
		}
		seen[e.ID] = true
	}
	if len(stored) != 50 {
		t.Fatalf("expected 50 events, got %d", len(stored))
	}
}

func TestWebhookRejectsInvalidEventType(t *testing.T) {
	h := newWebhookHarness(nil)
	for _, bad := range []string{"", "Push", "pull request", "a\r\nb", strings.Repeat("a", 65), "../x"} {
		rr := h.post(bad, "g", `{}`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("event type %q: status = %d, want 400", bad, rr.Code)
		}
	}
}

func TestWebhookReplacesUnsafeDeliveryID(t *testing.T) {
	h := newWebhookHarness(nil)
	h.post("star", "bad id with spaces\r\n", `{}`)
	stored, _ := h.repo.List(context.Background())
	if len(stored) != 1 || strings.ContainsAny(stored[0].ID, " \r\n") {
		t.Fatalf("unsafe delivery id leaked into event id: %+v", stored)
	}
}

func TestWebhookOversizedPayloadReturns413(t *testing.T) {
	h := newWebhookHarness(nil)
	h.server.cfg.MaxBodyBytes = 32
	rr := h.post("star", "g", `{"x":"`+strings.Repeat("a", 100)+`"}`)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
}

func TestWebhookBodyIsDecodedOnceFromVerifiedBytes(t *testing.T) {
	h := newWebhookHarness(nil)
	rr := h.post("star", "g", `{"repository":{"full_name":"o/r"}}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rr.Code)
	}
	stored, _ := h.repo.List(context.Background())
	if stored[0].Repository != "o/r" {
		t.Fatalf("repository = %q", stored[0].Repository)
	}
}

func TestWebhookNullBodyIsHandled(t *testing.T) {
	h := newWebhookHarness(nil)
	if rr := h.post("ping", "g", `null`); rr.Code != http.StatusAccepted {
		t.Fatalf("null payload status = %d", rr.Code)
	}
}

// --- retention -------------------------------------------------------------

type fakePurger struct {
	mu      sync.Mutex
	cutoffs []time.Time
	deleted int64
	err     error
}

func (f *fakePurger) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, cutoff)
	return f.deleted, f.err
}

func (f *fakePurger) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cutoffs)
}

func TestRetentionPurgesAtStartupAndStopsOnCancel(t *testing.T) {
	var logs bytes.Buffer
	purger := &fakePurger{deleted: 3}
	s := &Server{
		cfg:    &config.Config{EventRetention: 48 * time.Hour},
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.runRetention(ctx, purger)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for purger.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if purger.calls() == 0 {
		t.Fatal("retention must purge once at start-up")
	}
	cutoff := purger.cutoffs[0]
	if age := time.Since(cutoff); age < 47*time.Hour || age > 49*time.Hour {
		t.Fatalf("cutoff should be ~48h ago, was %v ago", age)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention loop did not stop on cancel")
	}
	if !strings.Contains(logs.String(), "purged expired events") {
		t.Fatalf("expected a log line about deleted events: %s", logs.String())
	}
}

func TestRetentionLogsPurgeErrors(t *testing.T) {
	var logs bytes.Buffer
	s := &Server{cfg: &config.Config{EventRetention: time.Hour}, logger: slog.New(slog.NewTextHandler(&logs, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	s.runRetention(ctx, &fakePurger{err: errors.New("db down")})
	if !strings.Contains(logs.String(), "event retention purge failed") {
		t.Fatalf("expected error log, got %s", logs.String())
	}
}

// --- construction ----------------------------------------------------------

func TestNewServerRejectsNilConfig(t *testing.T) {
	if _, err := NewServer(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNewServerInMemoryDefaults(t *testing.T) {
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer s.closeAll() //nolint:errcheck
	if s.purger != nil {
		t.Fatal("in-memory repository has nothing to purge")
	}
	// Fail closed out of the box: no API key configured means no access.
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(`{}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("default config must refuse API calls, got %d", rr.Code)
	}
}

func TestNewServerRejectsBadRedisURL(t *testing.T) {
	cfg, _ := config.LoadConfig()
	cfg.RedisURL = "not-a-redis-url"
	if _, err := NewServer(context.Background(), cfg, quietLogger()); err == nil {
		t.Fatal("expected error for malformed REDIS_URL")
	}
}

func TestNewServerFailsFastWhenRedisUnreachable(t *testing.T) {
	cfg, _ := config.LoadConfig()
	cfg.RedisURL = "redis://127.0.0.1:1/0" // nothing listens on port 1
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := NewServer(ctx, cfg, quietLogger()); err == nil {
		t.Fatal("expected start-up to fail when Redis is configured but unreachable")
	}
}

func TestNewServerRejectsMissingRulesFile(t *testing.T) {
	cfg, _ := config.LoadConfig()
	cfg.RulesPath = "/definitely/not/here.yaml"
	if _, err := NewServer(context.Background(), cfg, quietLogger()); err == nil {
		t.Fatal("expected error for missing rules file")
	}
}

func TestNewProviderWarnsWithoutChannels(t *testing.T) {
	var logs bytes.Buffer
	p := newProvider(&config.Config{}, slog.New(slog.NewTextHandler(&logs, nil)))
	if !strings.Contains(logs.String(), "no delivery channel configured") {
		t.Fatalf("expected warning, got %q", logs.String())
	}
	if p.Name() != "webhook" {
		t.Fatalf("provider = %s", p.Name())
	}

	logs.Reset()
	p = newProvider(&config.Config{SMTPHost: "smtp.example.com", SMTPSender: "a@example.com"}, slog.New(slog.NewTextHandler(&logs, nil)))
	if logs.Len() != 0 || p.Name() != "webhook+smtp" {
		t.Fatalf("smtp configured: logs=%q provider=%s", logs.String(), p.Name())
	}
}

func TestCloseAllJoinsErrors(t *testing.T) {
	a, b := errors.New("a"), errors.New("b")
	s := &Server{closers: []func() error{func() error { return a }, func() error { return nil }, func() error { return b }}}
	err := s.closeAll()
	if !errors.Is(err, a) || !errors.Is(err, b) {
		t.Fatalf("expected both errors, got %v", err)
	}
	if s.closeAll() != nil {
		t.Fatal("closers must run once")
	}
}
