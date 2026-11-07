package delivery

import (
	"context"
	"fmt"

	"github.com/agentpulse/notification-service/pkg/notification"
)

type Service struct {
	provider Provider
}

func NewService(provider Provider) *Service {
	return &Service{provider: provider}
}

func (s *Service) Send(ctx context.Context, req notification.NotificationRequest) error {
	if s.provider == nil {
		return fmt.Errorf("no delivery provider configured")
	}
	return s.provider.Send(ctx, req)
}

func (s *Service) ProviderName() string {
	if s.provider == nil {
		return "none"
	}
	return s.provider.Name()
}
