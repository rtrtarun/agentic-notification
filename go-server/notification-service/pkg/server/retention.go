package server

import (
	"context"
	"time"

	"github.com/example/notification-service/pkg/event"
)

const retentionInterval = time.Hour

// runRetention deletes events older than the retention period until ctx is
// cancelled. It runs once at start-up and then hourly.
func (s *Server) runRetention(ctx context.Context, purger event.Purger) {
	purge := func() {
		cutoff := time.Now().Add(-s.cfg.EventRetention)
		deleted, err := purger.DeleteOlderThan(ctx, cutoff)
		switch {
		case err != nil:
			s.logFor(ctx).Error("event retention purge failed", "error", err)
		case deleted > 0:
			s.logFor(ctx).Info("purged expired events", "deleted", deleted, "retention", s.cfg.EventRetention.String())
		}
	}

	purge()
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			purge()
		case <-ctx.Done():
			return
		}
	}
}
