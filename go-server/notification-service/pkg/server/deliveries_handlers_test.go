package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/config"
	"github.com/agentpulse/notification-service/pkg/delivery"
	"github.com/agentpulse/notification-service/pkg/sms"
)

func newDeliveryServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.APIKey, cfg.AllowUnsignedWebhooks = testAPIKey, true
	cfg.SMSGateway, cfg.SMSRecipients = "mock", []string{"+14155550100"}
	cfg.StandardChannels = []string{"sms"}
	if mutate != nil {
		mutate(cfg)
	}
	s, err := NewServer(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = s.closeAll() })
	return s
}

func webhook(s *Server, eventType, id, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", eventType)
	req.Header.Set("X-GitHub-Delivery", id)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

type deliveriesBody struct {
	Deliveries []delivery.Job          `json:"deliveries"`
	Counts     map[delivery.Status]int `json:"counts"`
}

const prPayload = `{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":7}}`

func TestStandardEventIsDeliveredBySMSThroughTheQueue(t *testing.T) {
	s := newDeliveryServer(t, nil)
	mock := s.smsGateway.(*sms.Mock)

	rr := webhook(s, "pull_request", "d1", prPayload)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("webhook = %d %s", rr.Code, rr.Body.String())
	}
	if len(mock.Sent()) != 0 {
		t.Fatal("a standard event must be queued, not sent inline")
	}
	list := decode[deliveriesBody](t, inboxCall(s, http.MethodGet, "/api/deliveries", ""))
	if len(list.Deliveries) != 1 || list.Deliveries[0].Status != delivery.StatusPending || list.Counts[delivery.StatusPending] != 1 {
		t.Fatalf("before dispatch = %+v", list)
	}

	if n, err := s.dispatcher.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("dispatch = %d, %v", n, err)
	}
	if got := mock.Sent(); len(got) != 1 || got[0].To != "+14155550100" {
		t.Fatalf("sent = %+v", got)
	}
	list = decode[deliveriesBody](t, inboxCall(s, http.MethodGet, "/api/deliveries", ""))
	if list.Deliveries[0].Status != delivery.StatusSent || list.Counts[delivery.StatusSent] != 1 {
		t.Fatalf("after dispatch = %+v", list)
	}
}

func TestCriticalEventIsTextedInlineAndLogged(t *testing.T) {
	s := newDeliveryServer(t, nil)
	mock := s.smsGateway.(*sms.Mock)

	rr := webhook(s, "security_advisory", "d2", `{"action":"published","repository":{"full_name":"acme/web"}}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("webhook = %d %s", rr.Code, rr.Body.String())
	}
	if len(mock.Sent()) != 1 || !strings.Contains(mock.Sent()[0].Text, "CRITICAL") {
		t.Fatalf("critical must be texted immediately: %+v", mock.Sent())
	}
	list := decode[deliveriesBody](t, inboxCall(s, http.MethodGet, "/api/deliveries", ""))
	if len(list.Deliveries) != 1 || !list.Deliveries[0].Direct || list.Deliveries[0].Status != delivery.StatusSent {
		t.Fatalf("log = %+v", list)
	}
	if n, _ := s.dispatcher.RunOnce(context.Background()); n != 0 {
		t.Fatal("a critical event must never be in the queue")
	}
}

func TestDeliveriesResponsesNeverContainTargetsOrText(t *testing.T) {
	s := newDeliveryServer(t, nil)
	webhook(s, "pull_request", "d1", prPayload)
	_, _ = s.dispatcher.RunOnce(context.Background())
	body := inboxCall(s, http.MethodGet, "/api/deliveries", "").Body.String()
	for _, secret := range []string{"4155550100", "+1415", "Source:", "Repository:"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaks %q: %s", secret, body)
		}
	}
}

func TestRetryEndpoint(t *testing.T) {
	s := newDeliveryServer(t, nil)
	now := time.Now()
	dead := delivery.Job{ID: "dead1", EventID: "e", Channel: "sms", Class: "standard", Title: "t",
		Status: delivery.StatusDead, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	if _, err := s.deliveryStore.Enqueue(context.Background(), dead); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/deliveries/dead1/retry", http.StatusOK},
		{http.MethodPost, "/api/deliveries/dead1/retry", http.StatusConflict},
		{http.MethodPost, "/api/deliveries/nope/retry", http.StatusNotFound},
		{http.MethodGet, "/api/deliveries/dead1/retry", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/deliveries", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/deliveries/dead1/other", http.StatusNotFound},
		{http.MethodGet, "/api/deliveries?limit=0", http.StatusBadRequest},
		{http.MethodGet, "/api/deliveries?limit=2", http.StatusOK},
	}
	for _, c := range checks {
		if rr := inboxCall(s, c.method, c.path, ""); rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
}

func TestDeliveriesRequireTheAPIKey(t *testing.T) {
	s := newDeliveryServer(t, nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/deliveries", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestEmptyQueueListsAnEmptyArray(t *testing.T) {
	s := newDeliveryServer(t, nil)
	if body := inboxCall(s, http.MethodGet, "/api/deliveries", "").Body.String(); !strings.Contains(body, `"deliveries":[]`) {
		t.Fatalf("body = %s", body)
	}
}

func TestSMSChannelIsListedAndTestable(t *testing.T) {
	s := newDeliveryServer(t, nil)
	rr := inboxCall(s, http.MethodPost, "/api/channels/sms/test", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("sms test = %d %s", rr.Code, rr.Body.String())
	}
	if len(s.smsGateway.(*sms.Mock).Sent()) != 1 {
		t.Fatal("the test text did not reach the gateway")
	}
	body := inboxCall(s, http.MethodGet, "/api/channels", "").Body.String()
	if !strings.Contains(body, `"mode":"mock"`) || strings.Contains(body, "4155550100") {
		t.Fatalf("channels = %s", body)
	}
}

func TestNewServerRejectsBadDeliverySettings(t *testing.T) {
	cases := map[string]func(*config.Config){
		"http gateway without a url": func(c *config.Config) { c.SMSGateway = "http" },
		"sns without credentials":    func(c *config.Config) { c.SMSGateway = "sns" },
		"invalid number":             func(c *config.Config) { c.SMSRecipients = []string{"12345"} },
		"unknown standard channel":   func(c *config.Config) { c.StandardChannels = []string{"pager"} },
		"unknown background channel": func(c *config.Config) { c.BackgroundChannels = []string{"fax"} },
	}
	for name, mutate := range cases {
		cfg, _ := config.LoadConfig()
		cfg.SMSGateway, cfg.SMSRecipients = "mock", []string{"+14155550100"}
		mutate(cfg)
		if _, err := NewServer(context.Background(), cfg, quietLogger()); err == nil {
			t.Errorf("%s: expected a start-up error", name)
		}
	}
}

func TestSMSIsOffByDefault(t *testing.T) {
	s := newDeliveryServer(t, func(c *config.Config) { c.SMSGateway, c.SMSRecipients, c.StandardChannels = "", nil, nil })
	if s.smsGateway != nil {
		t.Fatal("no gateway configured, yet one was built")
	}
	webhook(s, "pull_request", "d1", prPayload)
	if n, _ := s.dispatcher.RunOnce(context.Background()); n != 0 {
		t.Fatal("with no routes nothing is queued")
	}
}
