// Package dbtest opens a migrated PostgreSQL database for integration tests.
//
// Integration tests are skipped unless TEST_DATABASE_URL is set, so the normal
// test run needs no database. In CI, `docker compose up -d postgres` and
// `TEST_DATABASE_URL=postgres://... go test -tags integration ./...` run them.
package dbtest

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/db"
)

// EnvURL is the environment variable that holds the test database address.
const EnvURL = "TEST_DATABASE_URL"

const setupTimeout = 30 * time.Second

// Open connects to the test database, applies the migrations and registers a
// cleanup that closes the connection. It skips the test when no database is configured.
func Open(t testing.TB) *sql.DB {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Skip(EnvURL + " not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	conn, err := db.Connect(ctx, url, db.PoolConfig{MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetime: time.Minute})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.MigrateLocked(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

// Clear empties the named tables so a test starts from a known state.
// Table names come from the test code, never from input.
func Clear(t testing.TB, conn *sql.DB, tables ...string) {
	t.Helper()
	for _, table := range tables {
		if _, err := conn.ExecContext(context.Background(), `DELETE FROM `+table); err != nil { //nolint:gosec // table names come from test code, never from input
			t.Fatalf("clear %s: %v", table, err)
		}
	}
}
