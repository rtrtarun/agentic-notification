package delivery

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/notification"
)

type stubProvider struct {
	name  string
	err   error
	delay time.Duration
	calls atomic.Int32
}

func (s *stubProvider) Send(ctx context.Context, _ notification.NotificationRequest) error {
	s.calls.Add(1)
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.err
}

func (s *stubProvider) Name() string { return s.name }

func TestMultiProviderRunsEveryProviderWhenOneFails(t *testing.T) {
	failing := &stubProvider{name: "email", err: errors.New("smtp down")}
	working := &stubProvider{name: "slack"}
	m := NewMultiProvider(failing, working)

	err := m.Send(context.Background(), notification.NotificationRequest{})
	if err == nil || !errors.Is(err, failing.err) {
		t.Fatalf("expected joined smtp error, got %v", err)
	}
	if working.calls.Load() != 1 {
		t.Fatal("a failing provider must not stop the others")
	}
}

func TestMultiProviderIsConcurrent(t *testing.T) {
	a := &stubProvider{name: "a", delay: 150 * time.Millisecond}
	b := &stubProvider{name: "b", delay: 150 * time.Millisecond}

	start := time.Now()
	if err := NewMultiProvider(a, b).Send(context.Background(), notification.NotificationRequest{}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 280*time.Millisecond {
		t.Fatalf("providers ran serially (%v)", elapsed)
	}
}

func TestMultiProviderJoinsAllErrors(t *testing.T) {
	e1, e2 := errors.New("one"), errors.New("two")
	err := NewMultiProvider(&stubProvider{err: e1}, &stubProvider{err: e2}).Send(context.Background(), notification.NotificationRequest{})
	if !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Fatalf("expected both errors joined, got %v", err)
	}
}

func TestMultiProviderName(t *testing.T) {
	m := NewMultiProvider(&stubProvider{name: "webhook"}, &stubProvider{name: "smtp"})
	if m.Name() != "webhook+smtp" {
		t.Fatalf("Name() = %q", m.Name())
	}
}

func TestMultiProviderPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewMultiProvider(&stubProvider{delay: time.Second}).Send(ctx, notification.NotificationRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
