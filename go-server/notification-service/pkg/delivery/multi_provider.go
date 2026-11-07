package delivery

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/agentpulse/notification-service/pkg/notification"
)

// MultiProvider fans a request out to every provider concurrently. One provider
// failing never stops the others, and all failures are returned joined, so a
// broken email channel cannot stop a critical Slack alert.
type MultiProvider struct {
	providers []Provider
}

// NewMultiProvider combines providers.
func NewMultiProvider(providers ...Provider) *MultiProvider {
	return &MultiProvider{providers: providers}
}

// Name lists the member providers.
func (m *MultiProvider) Name() string {
	names := make([]string, len(m.providers))
	for i, p := range m.providers {
		names[i] = p.Name()
	}
	return strings.Join(names, "+")
}

// Send runs every provider in its own goroutine and waits for all of them.
func (m *MultiProvider) Send(ctx context.Context, req notification.NotificationRequest) error {
	errs := make([]error, len(m.providers))
	var wg sync.WaitGroup
	for i, p := range m.providers {
		wg.Add(1)
		go func(i int, p Provider) {
			defer wg.Done()
			errs[i] = p.Send(ctx, req)
		}(i, p)
	}
	wg.Wait()
	return errors.Join(errs...)
}
