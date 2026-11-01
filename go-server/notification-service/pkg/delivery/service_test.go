package delivery

import (
	"context"
	"testing"

	"github.com/example/notification-service/pkg/notification"
)

type fakeProvider struct{}

func (f *fakeProvider) Send(_ context.Context, req notification.NotificationRequest) error {
	return nil
}

func (f *fakeProvider) Name() string {
	return "fake"
}

func TestServiceUsesConfiguredProvider(t *testing.T) {
	service := NewService(&fakeProvider{})
	if err := service.Send(context.Background(), notification.NotificationRequest{Agent: notification.AgentInfo{Name: "agent"}, Title: "hello", Message: "world"}); err != nil {
		t.Fatalf("expected delivery success, got %v", err)
	}
	if service.ProviderName() != "fake" {
		t.Fatalf("expected provider name fake, got %s", service.ProviderName())
	}
}
