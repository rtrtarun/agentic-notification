package impl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/mail"
	"github.com/example/notification-service/pkg/notification"
)

const webhookTimeout = 10 * time.Second

type NotificationServiceImpl struct {
	cfg    *config.Config
	client *http.Client
}

func NewNotificationService(cfg *config.Config) *NotificationServiceImpl {
	return &NotificationServiceImpl{cfg: cfg, client: &http.Client{Timeout: webhookTimeout}}
}

func (s *NotificationServiceImpl) Send(ctx context.Context, req notification.NotificationRequest) error {
	if req.Agent.Name == "" {
		return fmt.Errorf("%w: agent name required", notification.ErrInvalidRequest)
	}
	if req.Title == "" {
		return fmt.Errorf("%w: title required", notification.ErrInvalidRequest)
	}

	if len(req.EmailRecipients) > 0 {
		if err := s.sendEmail(ctx, req.EmailRecipients, req.Title, req.Message, req.Agent); err != nil {
			return fmt.Errorf("send email: %w", err)
		}
	}

	webhookURLs := append(append([]string{}, req.SlackWebhookURLs...), req.TeamsWebhookURLs...)
	for _, url := range webhookURLs {
		if err := s.sendWebhook(ctx, url, req.Message, req.Agent); err != nil {
			return fmt.Errorf("send webhook: %w", err)
		}
	}
	return nil
}

func (s *NotificationServiceImpl) sendEmail(ctx context.Context, to []string, title, message string, agent notification.AgentInfo) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.SMTPHost, s.cfg.SMTPPort)

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", s.cfg.SMTPSender)
	fmt.Fprintf(&msg, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&msg, "Subject: [n8n agent] %s\r\n", title)
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
	fmt.Fprintf(&msg, "%s\n\nAgent: %s\nOwner: %s\nGroup: %s", message, agent.Name, agent.Owner, agent.Group)

	auth := smtp.PlainAuth("", s.cfg.SMTPUsername, s.cfg.SMTPPassword, s.cfg.SMTPHost)
	return mail.Send(ctx, addr, auth, s.cfg.SMTPSender, to, msg.Bytes())
}

func (s *NotificationServiceImpl) sendWebhook(ctx context.Context, url, message string, agent notification.AgentInfo) error {
	payload, err := json.Marshal(map[string]string{"text": fmt.Sprintf("%s\n\nAgent: %s\nGroup: %s", message, agent.Name, agent.Group)})
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
