package delivery

import (
	"context"

	"github.com/example/notification-service/pkg/notification"
)

// Provider delivers a notification through one channel or vendor.
type Provider interface {
	Send(ctx context.Context, req notification.NotificationRequest) error
	Name() string
}

// StubProvider accepts every request and delivers nothing.
type StubProvider struct{}

func (s *StubProvider) Send(ctx context.Context, req notification.NotificationRequest) error {
	return ctx.Err()
}

func (s *StubProvider) Name() string {
	return "stub"
}
