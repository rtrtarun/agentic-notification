package delivery

import (
	"context"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/config"
	"github.com/agentpulse/notification-service/pkg/notification"
)

func TestSMTPProviderRejectsUnconfiguredState(t *testing.T) {
	provider := NewSMTPProvider(&config.Config{})
	if err := provider.Send(context.Background(), notification.NotificationRequest{EmailRecipients: []string{"test@example.com"}}); err == nil {
		t.Fatal("expected unconfigured smtp provider to fail")
	}
}

func TestSMTPProviderNoRecipientsIsNoop(t *testing.T) {
	if err := NewSMTPProvider(&config.Config{}).Send(context.Background(), notification.NotificationRequest{}); err != nil {
		t.Fatalf("no recipients should be a no-op even when unconfigured, got %v", err)
	}
}

func TestSMTPProviderRejectsInjectedRecipients(t *testing.T) {
	cfg := &config.Config{SMTPHost: "127.0.0.1", SMTPPort: 1, SMTPSender: "from@example.com"}
	tests := []string{
		"a@example.com\r\nBcc: evil@x.com",
		"Name <a@example.com>",
		"not-an-address",
	}
	for _, rcpt := range tests {
		err := NewSMTPProvider(cfg).Send(context.Background(), notification.NotificationRequest{EmailRecipients: []string{rcpt}})
		if err == nil || !strings.Contains(err.Error(), "invalid email recipient") {
			t.Fatalf("recipient %q: expected validation error, got %v", rcpt, err)
		}
	}
}

func TestBuildMessageFlattensHeaderInjection(t *testing.T) {
	msg := string(buildMessage("from@example.com", notification.NotificationRequest{
		Agent:           notification.AgentInfo{Name: "agent", Owner: "me", Group: "g"},
		Title:           "hello\r\nBcc: evil@x.com",
		Message:         "body",
		EmailRecipients: []string{"a@example.com"},
	}))

	headers := msg[:strings.Index(msg, "\r\n\r\n")]
	if strings.Contains(headers, "\r\nBcc:") {
		t.Fatalf("title injected a header:\n%s", headers)
	}
	for _, want := range []string{"Subject: hello  Bcc: evil@x.com", "To: a@example.com", "From: from@example.com", "Agent: agent"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestSMTPProviderName(t *testing.T) {
	if NewSMTPProvider(nil).Name() != "smtp" {
		t.Fatal("unexpected provider name")
	}
}
