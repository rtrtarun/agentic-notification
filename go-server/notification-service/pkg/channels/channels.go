// Package channels describes the delivery channels the service is configured
// with and can send a test message through one. Secrets never leave the
// package: addresses and webhook URLs are masked, and a Slack or Teams webhook
// URL is reduced to its host because the path is the credential.
package channels

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/example/notification-service/pkg/notification"
	"github.com/example/notification-service/pkg/sms"
)

// Channel IDs.
const (
	IDEmail = "email"
	IDSlack = "slack"
	IDTeams = "teams"
	IDSMS   = "sms"
)

// Errors the HTTP layer maps to status codes.
var (
	ErrUnknown       = errors.New("unknown channel")
	ErrNotConfigured = errors.New("channel is not configured")
	ErrTooSoon       = errors.New("a test was just sent; wait a few seconds")
)

// Settings is what the channels are configured with.
type Settings struct {
	SMTPHost        string
	SMTPSender      string
	EmailRecipients []string
	SlackWebhookURL string
	TeamsWebhookURL string
	// SMSGateway is "mock", "http" or "sns"; empty means SMS is off.
	SMSGateway    string
	SMSRecipients []string
	// StandardChannels and BackgroundChannels are the channel IDs those classes are routed to.
	StandardChannels   []string
	BackgroundChannels []string
}

// Channel is one delivery channel as shown to people. Target is always masked.
type Channel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Target     string `json:"target,omitempty"`
	// Receives says which attention classes are sent here. Today only critical events are pushed.
	Receives []string `json:"receives"`
	// Setup names the environment variables that configure the channel.
	Setup string `json:"setup"`
	// Mode is "mock" when messages are only logged and never leave the service.
	Mode string `json:"mode,omitempty"`
}

// Describe lists every channel with its masked target.
func Describe(s Settings) []Channel {
	receivesFor := func(id string) []string {
		out := []string{"critical"}
		if contains(s.StandardChannels, id) {
			out = append(out, "standard")
		}
		if contains(s.BackgroundChannels, id) {
			out = append(out, "background")
		}
		return out
	}
	email := Channel{ID: IDEmail, Name: "Email", Receives: receivesFor(IDEmail), Setup: "SMTP_HOST, SMTP_SENDER and DEFAULT_NOTIFY_EMAIL"}
	if s.SMTPHost != "" && s.SMTPSender != "" && len(s.EmailRecipients) > 0 {
		email.Configured = true
		masked := make([]string, 0, len(s.EmailRecipients))
		for _, r := range s.EmailRecipients {
			masked = append(masked, maskEmail(r))
		}
		email.Target = strings.Join(masked, ", ")
	}
	slack := Channel{ID: IDSlack, Name: "Slack", Receives: receivesFor(IDSlack), Setup: "SLACK_WEBHOOK_URL"}
	if s.SlackWebhookURL != "" {
		slack.Configured, slack.Target = true, hostOnly(s.SlackWebhookURL)
	}
	teams := Channel{ID: IDTeams, Name: "Microsoft Teams", Receives: receivesFor(IDTeams), Setup: "TEAMS_WEBHOOK_URL"}
	if s.TeamsWebhookURL != "" {
		teams.Configured, teams.Target = true, hostOnly(s.TeamsWebhookURL)
	}
	smsChannel := Channel{ID: IDSMS, Name: "SMS", Receives: receivesFor(IDSMS), Setup: "SMS_GATEWAY (mock, http or sns) and SMS_RECIPIENTS"}
	if s.SMSGateway != "" && len(s.SMSRecipients) > 0 {
		smsChannel.Configured = true
		masked := make([]string, 0, len(s.SMSRecipients))
		for _, n := range s.SMSRecipients {
			masked = append(masked, sms.MaskNumber(n))
		}
		smsChannel.Target = strings.Join(masked, ", ")
		if s.SMSGateway == "mock" {
			smsChannel.Mode = "mock"
		}
	}
	return []Channel{email, slack, teams, smsChannel}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// maskEmail keeps the first letter and the domain: "alice@example.com" becomes "a***@example.com".
func maskEmail(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		return "***"
	}
	return addr[:1] + "***" + addr[at:]
}

// hostOnly returns "host/…" for a webhook URL, hiding the secret path.
func hostOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "configured"
	}
	return u.Hostname() + "/…"
}

// Sender delivers a notification. *delivery.Service satisfies it.
type Sender interface {
	Send(ctx context.Context, req notification.NotificationRequest) error
}

// Tester sends a test message through one channel.
type Tester struct {
	sender   Sender
	settings Settings
	minGap   time.Duration
	now      func() time.Time
	mu       sync.Mutex
	last     map[string]time.Time
}

// NewTester builds a tester. minGap is the shortest time between tests of one
// channel, so the button cannot be used to flood a Slack channel or an inbox.
func NewTester(sender Sender, settings Settings, minGap time.Duration) *Tester {
	return &Tester{sender: sender, settings: settings, minGap: minGap, now: time.Now, last: map[string]time.Time{}}
}

// WithClock replaces the clock; for tests.
func (t *Tester) WithClock(now func() time.Time) *Tester {
	t.now = now
	return t
}

// Test sends one clearly labelled message through the channel. Only that
// channel's target is used, so a Slack test never emails anyone.
func (t *Tester) Test(ctx context.Context, id string) error {
	req := notification.NotificationRequest{
		Agent:   notification.AgentInfo{Name: "attention-orchestration", Group: "channel-test"},
		Title:   "[TEST] Attention channel check",
		Message: "This is a test message from the Attention console. No action is needed.",
	}
	switch id {
	case IDEmail:
		if !t.channel(id).Configured {
			return ErrNotConfigured
		}
		req.EmailRecipients = t.settings.EmailRecipients
	case IDSlack:
		if t.settings.SlackWebhookURL == "" {
			return ErrNotConfigured
		}
		req.SlackWebhookURLs = []string{t.settings.SlackWebhookURL}
	case IDTeams:
		if t.settings.TeamsWebhookURL == "" {
			return ErrNotConfigured
		}
		req.TeamsWebhookURLs = []string{t.settings.TeamsWebhookURL}
	case IDSMS:
		if !t.channel(id).Configured {
			return ErrNotConfigured
		}
		req.SMSRecipients = t.settings.SMSRecipients
	default:
		return ErrUnknown
	}

	t.mu.Lock()
	if prev, ok := t.last[id]; ok && t.now().Sub(prev) < t.minGap {
		t.mu.Unlock()
		return ErrTooSoon
	}
	t.last[id] = t.now()
	t.mu.Unlock()

	if err := t.sender.Send(ctx, req); err != nil {
		return fmt.Errorf("send test through %s: %w", id, err)
	}
	return nil
}

func (t *Tester) channel(id string) Channel {
	for _, c := range Describe(t.settings) {
		if c.ID == id {
			return c
		}
	}
	return Channel{}
}
