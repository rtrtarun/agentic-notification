package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/channels"
	"github.com/agentpulse/notification-service/pkg/integrations"
	"github.com/agentpulse/notification-service/pkg/notification"
)

type captureSender struct {
	reqs []notification.NotificationRequest
	err  error
}

func (c *captureSender) Send(_ context.Context, r notification.NotificationRequest) error {
	c.reqs = append(c.reqs, r)
	return c.err
}

const testSlack = "https://hooks.slack.com/services/T0/B0/TOPSECRET"

func newChannelsServer(t *testing.T) (*Server, *captureSender) {
	t.Helper()
	s := newInboxServer(t)
	s.cfg.SlackWebhookURL = testSlack
	s.cfg.SMTPHost, s.cfg.SMTPSender, s.cfg.DefaultNotifyEmail = "smtp.example.com", "a@example.com", "oncall@example.com"
	sender := &captureSender{}
	s.channelTester = channels.NewTester(sender, s.channelSettings(), time.Hour)
	return s, sender
}

func TestChannelsListMasksSecrets(t *testing.T) {
	s, _ := newChannelsServer(t)
	rr := inboxCall(s, http.MethodGet, "/api/channels", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, secret := range []string{"TOPSECRET", "oncall@", "T0/B0"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaks %q: %s", secret, body)
		}
	}
	list := decode[struct {
		Channels []channels.Channel `json:"channels"`
	}](t, rr).Channels
	if len(list) != 4 || !list[0].Configured || !list[1].Configured || list[2].Configured || list[3].Configured {
		t.Fatalf("channels = %+v", list)
	}
}

func TestChannelTestSendsAndMapsErrors(t *testing.T) {
	s, sender := newChannelsServer(t)

	if rr := inboxCall(s, http.MethodPost, "/api/channels/slack/test", ""); rr.Code != http.StatusOK || len(sender.reqs) != 1 {
		t.Fatalf("slack test = %d sent=%d %s", rr.Code, len(sender.reqs), rr.Body.String())
	}
	rr := inboxCall(s, http.MethodPost, "/api/channels/slack/test", "")
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("second test = %d", rr.Code)
	}
	if rr := inboxCall(s, http.MethodPost, "/api/channels/teams/test", ""); rr.Code != http.StatusConflict {
		t.Fatalf("unconfigured = %d", rr.Code)
	}
	if rr := inboxCall(s, http.MethodPost, "/api/channels/pager/test", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d", rr.Code)
	}
}

func TestChannelTestFailureDoesNotLeakDetail(t *testing.T) {
	s, sender := newChannelsServer(t)
	sender.err = errors.New("dial tcp 10.0.0.5:587: connection refused for oncall@example.com")
	rr := inboxCall(s, http.MethodPost, "/api/channels/email/test", "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "10.0.0.5") || strings.Contains(rr.Body.String(), "oncall@") {
		t.Fatalf("leaked: %s", rr.Body.String())
	}
}

func TestChannelRoutesValidateMethodsPathsAndAuth(t *testing.T) {
	s, _ := newChannelsServer(t)
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/channels", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/channels/slack/test", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/channels/slack/other", http.StatusNotFound},
		{http.MethodPost, "/api/channels/slack", http.StatusNotFound},
	}
	for _, c := range cases {
		if rr := inboxCall(s, c.method, c.path, ""); rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/channels/slack/test", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no key = %d", rr.Code)
	}
	s.channelTester = nil
	if rr := inboxCall(s, http.MethodPost, "/api/channels/slack/test", ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no tester = %d", rr.Code)
	}
}

func TestIntegrationsReportGitHubStateWithoutSecrets(t *testing.T) {
	s := newInboxServer(t)
	s.cfg.GitHubWebhookSecret = "supersecret"
	rr := inboxCall(s, http.MethodGet, "/api/integrations", "")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "supersecret") {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	got := decode[struct {
		Integrations []integrations.Integration `json:"integrations"`
	}](t, rr).Integrations
	if len(got) != 1 || got[0].Status != integrations.StatusReady || !got[0].SecretConfigured || got[0].EventsReceived != 3 {
		t.Fatalf("integrations = %+v", got)
	}

	s.cfg.GitHubWebhookSecret = ""
	rr = inboxCall(s, http.MethodGet, "/api/integrations", "")
	if g := decode[struct {
		Integrations []integrations.Integration `json:"integrations"`
	}](t, rr).Integrations; g[0].Status != integrations.StatusBlocked {
		t.Fatalf("no secret should be blocked: %+v", g[0])
	}
	if rr := inboxCall(s, http.MethodPost, "/api/integrations", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rr.Code)
	}
}
