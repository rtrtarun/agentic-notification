package impl

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/notification"
)

func validRequest(urls ...string) notification.NotificationRequest {
	return notification.NotificationRequest{
		Agent:            notification.AgentInfo{Name: "agent", Group: "g"},
		Title:            "title",
		Message:          "hello",
		SlackWebhookURLs: urls,
	}
}

func TestSendValidatesRequest(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*notification.NotificationRequest)
	}{
		{"missing agent", func(r *notification.NotificationRequest) { r.Agent.Name = "" }},
		{"missing title", func(r *notification.NotificationRequest) { r.Title = "" }},
	}
	svc := NewNotificationService(&config.Config{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.mut(&req)
			err := svc.Send(context.Background(), req)
			if !errors.Is(err, notification.ErrInvalidRequest) {
				t.Fatalf("expected ErrInvalidRequest, got %v", err)
			}
		})
	}
}

func TestSendPostsToEveryWebhook(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing json content type")
		}
		hits.Add(1)
	}))
	defer srv.Close()

	req := validRequest(srv.URL)
	req.TeamsWebhookURLs = []string{srv.URL}
	if err := NewNotificationService(&config.Config{}).Send(context.Background(), req); err != nil {
		t.Fatalf("send: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected 2 webhook posts, got %d", hits.Load())
	}
}

func TestSendDoesNotMutateCallerSlices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	// Spare capacity would expose an append-aliasing bug.
	slack := make([]string, 1, 4)
	slack[0] = srv.URL
	req := validRequest()
	req.SlackWebhookURLs = slack
	req.TeamsWebhookURLs = []string{srv.URL}
	if err := NewNotificationService(&config.Config{}).Send(context.Background(), req); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := slack[:2][1]; got != "" {
		t.Fatalf("caller backing array was modified: %q", got)
	}
}

func TestSendReportsWebhookFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	err := NewNotificationService(&config.Config{}).Send(context.Background(), validRequest(srv.URL))
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected status 502 error, got %v", err)
	}
	if errors.Is(err, notification.ErrInvalidRequest) {
		t.Fatal("delivery failure must not look like a validation error")
	}
}

func TestSendHonoursCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewNotificationService(&config.Config{}).Send(ctx, validRequest(srv.URL))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
