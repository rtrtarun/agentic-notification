package delivery

import (
	"context"
	"testing"

	"github.com/agentpulse/notification-service/pkg/notification"
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

func TestServiceWithoutProviderFails(t *testing.T) {
	s := NewService(nil)
	if err := s.Send(context.Background(), notification.NotificationRequest{}); err == nil {
		t.Fatal("expected error without provider")
	}
	if s.ProviderName() != "none" {
		t.Fatalf("ProviderName() = %q", s.ProviderName())
	}
}

func TestStubProviderHonoursContext(t *testing.T) {
	p := &StubProvider{}
	if p.Name() != "stub" || p.Send(context.Background(), notification.NotificationRequest{}) != nil {
		t.Fatal("stub must accept requests")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p.Send(ctx, notification.NotificationRequest{}) == nil {
		t.Fatal("stub must report cancellation")
	}
}
