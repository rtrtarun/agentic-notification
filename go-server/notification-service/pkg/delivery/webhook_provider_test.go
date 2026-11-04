package delivery

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/example/notification-service/pkg/notification"
)

func webhookRequest(slack, teams []string) notification.NotificationRequest {
	return notification.NotificationRequest{
		Agent:            notification.AgentInfo{Name: "agent", Group: "g"},
		Title:            "t",
		Message:          "hello",
		SlackWebhookURLs: slack,
		TeamsWebhookURLs: teams,
	}
}

func TestWebhookProviderPostsToEveryURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing json content type")
		}
		hits.Add(1)
	}))
	defer srv.Close()

	p := NewWebhookProvider(false)
	if err := p.Send(context.Background(), webhookRequest([]string{srv.URL}, []string{srv.URL})); err != nil {
		t.Fatalf("send: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected 2 posts, got %d", hits.Load())
	}
}

func TestWebhookProviderContinuesAfterFailure(t *testing.T) {
	var good atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { good.Add(1) }))
	defer ok.Close()

	err := NewWebhookProvider(false).Send(context.Background(), webhookRequest([]string{bad.URL, ok.URL}, nil))
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected 502 error, got %v", err)
	}
	if good.Load() != 1 {
		t.Fatal("a failing webhook must not stop later webhooks")
	}
}

func TestWebhookProviderNoURLsIsNoop(t *testing.T) {
	if err := NewWebhookProvider(true).Send(context.Background(), webhookRequest(nil, nil)); err != nil {
		t.Fatalf("expected no-op, got %v", err)
	}
}

func TestWebhookProviderBlocksPrivateAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close() // listens on loopback

	err := NewWebhookProvider(true).Send(context.Background(), webhookRequest([]string{srv.URL}, nil))
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected ErrBlockedAddress, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("blocked request must never reach the server")
	}
}

func TestWebhookProviderDoesNotFollowRedirects(t *testing.T) {
	var target atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { target.Add(1) }))
	defer dest.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusFound)
	}))
	defer redirector.Close()

	err := NewWebhookProvider(false).Send(context.Background(), webhookRequest([]string{redirector.URL}, nil))
	if err == nil {
		t.Fatal("a redirect response is not a success")
	}
	if target.Load() != 0 {
		t.Fatal("redirect must not be followed")
	}
}

func TestWebhookProviderHonoursCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewWebhookProvider(false).Send(ctx, webhookRequest([]string{srv.URL}, nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestIsBlockedIP(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"fd00::1", true},
		{"8.8.8.8", false},
		{"2606:4700:4700::1111", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := isBlockedIP(net.ParseIP(tt.ip)); got != tt.blocked {
				t.Fatalf("isBlockedIP(%s) = %v, want %v", tt.ip, got, tt.blocked)
			}
		})
	}
}

func TestRefusePrivateAddressesRejectsHostnames(t *testing.T) {
	if err := refusePrivateAddresses("tcp", "example.com:443", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("a non-IP dial target must be refused, got %v", err)
	}
	if err := refusePrivateAddresses("tcp", "no-port", nil); err == nil {
		t.Fatal("malformed address must error")
	}
	if err := refusePrivateAddresses("tcp", "8.8.8.8:443", nil); err != nil {
		t.Fatalf("public address must pass: %v", err)
	}
}
