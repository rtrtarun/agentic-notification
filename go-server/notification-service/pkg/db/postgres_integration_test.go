//go:build integration

package db

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestConnectAppliesPoolSettingsAndMigrates(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := Connect(ctx, url, PoolConfig{MaxOpenConns: 7, MaxIdleConns: 3, ConnMaxLifetime: time.Minute})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	if got := conn.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7", got)
	}

	for i := 0; i < 2; i++ { // twice: migrations must be idempotent
		if err := MigrateLocked(ctx, conn); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
	}
	var indexes int
	if err := conn.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'events' AND indexname IN ('events_thread_id_idx','events_created_at_idx')`,
	).Scan(&indexes); err != nil || indexes != 2 {
		t.Fatalf("expected both indexes, got %d (err %v)", indexes, err)
	}
}
