//go:build integration

package delivery

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/db"
)

// TestPostgresStoreContract runs the same checks as the memory store against a real database.
func TestPostgresStoreContract(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Connect(ctx, url, db.PoolConfig{MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetime: time.Minute})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	if err := db.MigrateLocked(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM delivery_jobs`); err != nil {
		t.Fatal(err)
	}
	storeContract(t, NewPostgresStore(conn))
}
