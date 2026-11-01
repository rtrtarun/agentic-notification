package delivery

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/mail"
	"github.com/example/notification-service/pkg/notification"
)

// SMTPProvider delivers email through an SMTP server.
type SMTPProvider struct {
	cfg *config.Config
}

func NewSMTPProvider(cfg *config.Config) *SMTPProvider {
	return &SMTPProvider{cfg: cfg}
}

func (p *SMTPProvider) Send(ctx context.Context, req notification.NotificationRequest) error {
	if p.cfg == nil || p.cfg.SMTPHost == "" || p.cfg.SMTPSender == "" {
		return fmt.Errorf("smtp provider is not configured")
	}
	if len(req.EmailRecipients) == 0 {
		return nil
	}

	msg := []byte(fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\n%s\r\n", strings.Join(req.EmailRecipients, ", "), req.Title, req.Message))
	auth := smtp.PlainAuth("", p.cfg.SMTPUsername, p.cfg.SMTPPassword, p.cfg.SMTPHost)
	addr := fmt.Sprintf("%s:%d", p.cfg.SMTPHost, p.cfg.SMTPPort)
	return mail.Send(ctx, addr, auth, p.cfg.SMTPSender, req.EmailRecipients, msg)
}

func (p *SMTPProvider) Name() string {
	return "smtp"
}
