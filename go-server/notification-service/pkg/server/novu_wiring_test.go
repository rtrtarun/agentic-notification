package server

import (
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/config"
)

func TestEmailGoesThroughExactlyOneProvider(t *testing.T) {
	smtpOnly := &config.Config{SMTPHost: "smtp.example.com", SMTPSender: "a@example.com"}
	novuOnly := &config.Config{NovuAPIKey: "k"}
	both := &config.Config{SMTPHost: "smtp.example.com", SMTPSender: "a@example.com", NovuAPIKey: "k", NovuAPIURL: "https://novu.example.com"}
	neither := &config.Config{}
	for name, c := range map[string]struct {
		cfg  *config.Config
		want string
	}{
		"smtp only":      {smtpOnly, "smtp"},
		"novu only":      {novuOnly, "novu"},
		"novu then smtp": {both, "novu>smtp"},
	} {
		if got := emailProvider(c.cfg); got == nil || got.Name() != c.want {
			t.Errorf("%s: got %v, want %s", name, got, c.want)
		}
	}
	if emailProvider(neither) != nil {
		t.Error("with nothing configured there is no email provider")
	}
	if got := emailProvider(&config.Config{NovuAPIKey: "k", NovuAPIURL: "ftp://bad"}); got != nil {
		t.Errorf("a bad Novu address with no SMTP must not invent a provider: %v", got)
	}
}

func TestTheChannelsScreenCountsNovuAsConfiguredEmail(t *testing.T) {
	s := newDeliveryServer(t, func(c *config.Config) {
		c.NovuAPIKey, c.DefaultNotifyEmail = "k", "ops@example.com"
	})
	rr := call(s, "GET", "/api/channels", "", withKey)
	body := rr.Body.String()
	if !strings.Contains(body, `"id":"email"`) || !strings.Contains(body, `"configured":true`) {
		t.Fatalf("email through Novu should be configured: %s", body)
	}
	if strings.Contains(body, "k\"") && strings.Contains(body, "novu-secret") {
		t.Fatal("the key must never be shown")
	}
}
