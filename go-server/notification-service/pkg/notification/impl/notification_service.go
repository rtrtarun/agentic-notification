// Package impl is the default NotificationService: it validates a request and
// hands it to the delivery providers.
package impl

import (
	"context"
	"fmt"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/notification"
)

// NotificationServiceImpl sends a request over email and webhooks concurrently.
type NotificationServiceImpl struct {
	provider delivery.Provider
}

// NewNotificationService delivers over SMTP and webhooks. Webhook connections to
// private addresses are not blocked here; use NewNotificationServiceWith to
// choose a provider (the server blocks them in production).
func NewNotificationService(cfg *config.Config) *NotificationServiceImpl {
	return NewNotificationServiceWith(delivery.NewMultiProvider(
		delivery.NewSMTPProvider(cfg),
		delivery.NewWebhookProvider(false),
	))
}

// NewNotificationServiceWith builds the service around any provider.
func NewNotificationServiceWith(provider delivery.Provider) *NotificationServiceImpl {
	return &NotificationServiceImpl{provider: provider}
}

// Send validates the required fields and delivers the request. Every channel is
// attempted even when another fails; the returned error joins all failures.
func (s *NotificationServiceImpl) Send(ctx context.Context, req notification.NotificationRequest) error {
	if req.Agent.Name == "" {
		return fmt.Errorf("%w: agent name required", notification.ErrInvalidRequest)
	}
	if req.Title == "" {
		return fmt.Errorf("%w: title required", notification.ErrInvalidRequest)
	}
	if err := s.provider.Send(ctx, req); err != nil {
		return fmt.Errorf("deliver notification: %w", err)
	}
	return nil
}
