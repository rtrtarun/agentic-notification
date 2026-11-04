package delivery

import (
	"bytes"
	"context"
	"fmt"
	"net/mail"
	"net/smtp"
	"strings"

	"github.com/example/notification-service/pkg/config"
	mailer "github.com/example/notification-service/pkg/mail"
	"github.com/example/notification-service/pkg/notification"
)

// SMTPProvider delivers email through an SMTP server.
type SMTPProvider struct {
	cfg *config.Config
}

// NewSMTPProvider returns a provider using cfg's SMTP settings.
func NewSMTPProvider(cfg *config.Config) *SMTPProvider {
	return &SMTPProvider{cfg: cfg}
}

// Send emails every recipient on the request; it is a no-op without recipients.
func (p *SMTPProvider) Send(ctx context.Context, req notification.NotificationRequest) error {
	if len(req.EmailRecipients) == 0 {
		return nil
	}
	if p.cfg == nil || p.cfg.SMTPHost == "" || p.cfg.SMTPSender == "" {
		return fmt.Errorf("smtp provider is not configured")
	}
	for _, rcpt := range req.EmailRecipients {
		// Parsing rejects CR/LF, so a recipient cannot smuggle extra headers.
		if addr, err := mail.ParseAddress(rcpt); err != nil || addr.Address != rcpt {
			return fmt.Errorf("invalid email recipient")
		}
	}

	auth := smtp.PlainAuth("", p.cfg.SMTPUsername, p.cfg.SMTPPassword, p.cfg.SMTPHost)
	addr := fmt.Sprintf("%s:%d", p.cfg.SMTPHost, p.cfg.SMTPPort)
	return mailer.Send(ctx, addr, auth, p.cfg.SMTPSender, req.EmailRecipients, buildMessage(p.cfg.SMTPSender, req))
}

// Name identifies the provider.
func (p *SMTPProvider) Name() string {
	return "smtp"
}

func buildMessage(from string, req notification.NotificationRequest) []byte {
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", headerSafe(from))
	fmt.Fprintf(&msg, "To: %s\r\n", strings.Join(req.EmailRecipients, ", "))
	fmt.Fprintf(&msg, "Subject: %s\r\n", headerSafe(req.Title))
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
	fmt.Fprintf(&msg, "%s\r\n\r\nAgent: %s\r\nOwner: %s\r\nGroup: %s\r\n",
		req.Message, req.Agent.Name, req.Agent.Owner, req.Agent.Group)
	return msg.Bytes()
}

// headerSafe flattens a value to one line so it cannot inject headers.
func headerSafe(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
