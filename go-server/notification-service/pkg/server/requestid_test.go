package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/config"
	"github.com/agentpulse/notification-service/pkg/delivery"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/notification"
	"github.com/agentpulse/notification-service/pkg/pipeline"
	"github.com/agentpulse/notification-service/pkg/policy"
)

func TestRequestIDMiddleware(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		reused   bool
	}{
		{"generated when absent", "", false},
		{"reused when valid", "abc-123_x.y", true},
		{"replaced when too long", strings.Repeat("a", maxRequestIDLen+1), false},
		{"replaced when unsafe characters", "bad id<script>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = requestID(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.incoming != "" {
				req.Header.Set(requestIDHeader, tt.incoming)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			got := rr.Header().Get(requestIDHeader)
			if got == "" || got != seen {
				t.Fatalf("header %q and context %q must match and be non-empty", got, seen)
			}
			if tt.reused != (got == tt.incoming) {
				t.Fatalf("reused=%v but got %q for incoming %q", tt.reused, got, tt.incoming)
			}
		})
	}
}

func TestLogsIncludeRequestID(t *testing.T) {
	var buf bytes.Buffer
	s := &Server{
		cfg:    &config.Config{},
		logger: slog.New(slog.NewJSONHandler(&buf, nil)),
	}
	h := requestIDMiddleware(s.loggingMiddleware(http.HandlerFunc(s.healthHandler)))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(requestIDHeader, "req-42")
	h.ServeHTTP(httptest.NewRecorder(), req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	want := map[string]any{"request_id": "req-42", "path": "/health", "method": "GET", "http_status": float64(200)}
	for key, value := range want {
		if entry[key] != value {
			t.Fatalf("log key %q = %v, want %v", key, entry[key], value)
		}
	}
}

type failingRepo struct{ err error }

func (f failingRepo) Save(context.Context, event.Event) error      { return f.err }
func (f failingRepo) List(context.Context) ([]event.Event, error)  { return nil, f.err }
func (f failingRepo) Exists(context.Context, string) (bool, error) { return false, nil }

func TestWebhookPipelineFailureReturnsGenericError(t *testing.T) {
	s := &Server{
		cfg: &config.Config{},
		eventPipeline: pipeline.New(
			policy.NewEngine(nil),
			delivery.NewService(&delivery.StubProvider{}),
			failingRepo{err: errors.New("db password=hunter2 refused connection")},
			pipeline.Config{},
		),
	}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(`{}`))
	req.Header.Set(githubEventHeader, "star")
	rr := httptest.NewRecorder()
	s.githubWebhookHandler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "hunter2") {
		t.Fatalf("internal error text leaked to client: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), errInternal) {
		t.Fatalf("expected generic message, got %s", rr.Body.String())
	}
}

type stubNotifier struct{ err error }

func (s stubNotifier) Send(context.Context, notification.NotificationRequest) error { return s.err }

func TestNotifyHandlerErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
		notBody  string
	}{
		{"validation is a client error", errors.Join(notification.ErrInvalidRequest, errors.New("title required")), http.StatusBadRequest, "title required", ""},
		{"delivery failure is generic 500", errors.New("smtp password rejected"), http.StatusInternalServerError, errInternal, "smtp"},
		{"success", nil, http.StatusAccepted, "accepted", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{cfg: &config.Config{}, service: stubNotifier{err: tt.err}}
			req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(`{}`))
			rr := httptest.NewRecorder()
			s.notifyHandler(rr, req)

			if rr.Code != tt.wantCode {
				t.Fatalf("code = %d, want %d", rr.Code, tt.wantCode)
			}
			if !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Fatalf("body %q missing %q", rr.Body.String(), tt.wantBody)
			}
			if tt.notBody != "" && strings.Contains(rr.Body.String(), tt.notBody) {
				t.Fatalf("body %q must not contain %q", rr.Body.String(), tt.notBody)
			}
		})
	}
}

func TestRunShutsDownWhenContextCancelled(t *testing.T) {
	s := &Server{cfg: &config.Config{Address: "127.0.0.1:0", AllowUnsignedWebhooks: true}}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
