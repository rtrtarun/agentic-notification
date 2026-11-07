//go:build integration

package event_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/db"
	"github.com/agentpulse/notification-service/pkg/event"
)

// These tests run against a real PostgreSQL. Start one and point TEST_DATABASE_URL at it:
//
//	docker run --rm -d -p 5433:5432 -e POSTGRES_PASSWORD=test postgres:16-alpine
//	TEST_DATABASE_URL=postgres://postgres:test@localhost:5433/postgres?sslmode=disable \
//	  go test -tags integration ./pkg/event/...

func connect(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Connect(ctx, url, db.PoolConfig{MaxOpenConns: 10})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.MigrateLocked(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `TRUNCATE events CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return conn
}

func TestPostgresSaveExistsListRoundTrip(t *testing.T) {
	conn := connect(t)
	repo := event.NewPostgresRepository(conn)
	ctx := context.Background()

	evt := event.Event{
		ID: "evt-1", Source: "github", EventType: "pull_request", Repository: "octo/repo",
		AttentionClass: event.AttentionCritical,
		DecisionState:  &event.DecisionState{Status: event.DecisionSent, Reason: "security", MatchedRule: "r1"},
		DedupKey:       "k", ThreadID: "t", MergedInto: "",
		Payload: map[string]interface{}{"number": float64(7)},
	}
	if err := repo.Save(ctx, evt); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Saving the same ID again must be a no-op, not an error.
	if err := repo.Save(ctx, evt); err != nil {
		t.Fatalf("second save must be idempotent: %v", err)
	}

	if ok, err := repo.Exists(ctx, "evt-1"); err != nil || !ok {
		t.Fatalf("Exists(evt-1) = %v, %v", ok, err)
	}
	if ok, err := repo.Exists(ctx, "nope"); err != nil || ok {
		t.Fatalf("Exists(nope) = %v, %v", ok, err)
	}

	stored, err := repo.List(ctx)
	if err != nil || len(stored) != 1 {
		t.Fatalf("List = %d events, %v", len(stored), err)
	}
	got := stored[0]
	if got.ID != "evt-1" || got.AttentionClass != event.AttentionCritical || got.ThreadID != "t" ||
		got.CreatedAt.IsZero() || got.DecisionState == nil || got.DecisionState.MatchedRule != "r1" || got.Payload["number"] != float64(7) {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestPostgresListReturnsNewestOldestFirst(t *testing.T) {
	conn := connect(t)
	repo := event.NewPostgresRepository(conn)
	ctx := context.Background()

	total := event.DefaultListLimit + 3
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO events (id, source, event_type, created_at)
		SELECT 'e' || lpad(g::text, 5, '0'), 'github', 'star', NOW() - ($1 - g) * interval '1 second'
		FROM generate_series(1, $1) g`, total); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stored, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stored) != event.DefaultListLimit {
		t.Fatalf("expected %d events, got %d", event.DefaultListLimit, len(stored))
	}
	if stored[0].ID != "e00004" || stored[len(stored)-1].ID != fmt.Sprintf("e%05d", total) {
		t.Fatalf("expected newest %d oldest-first, got %s..%s", event.DefaultListLimit, stored[0].ID, stored[len(stored)-1].ID)
	}
}

func TestPostgresDeleteOlderThanEnforcesRetention(t *testing.T) {
	conn := connect(t)
	repo := event.NewPostgresRepository(conn)
	ctx := context.Background()

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO events (id, source, event_type, created_at) VALUES
		('old', 'github', 'star', NOW() - interval '100 days'),
		('new', 'github', 'star', NOW())`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	deleted, err := repo.DeleteOlderThan(ctx, time.Now().Add(-90*24*time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteOlderThan = %d, %v", deleted, err)
	}
	if ok, _ := repo.Exists(ctx, "old"); ok {
		t.Fatal("expired event must be gone")
	}
	if ok, _ := repo.Exists(ctx, "new"); !ok {
		t.Fatal("recent event must survive")
	}
}

func TestMigrateLockedIsSafeUnderConcurrentStartup(t *testing.T) {
	conn := connect(t)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- db.MigrateLocked(context.Background(), conn)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration failed: %v", err)
		}
	}
}

func TestMigrateLockedRollsBackOnFailure(t *testing.T) {
	conn := connect(t)
	ctx := context.Background()

	// A poisoned statement inside the same transaction must not leave partial state.
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE migration_rollback_probe (id int)`); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()

	var exists bool
	if err := conn.QueryRowContext(ctx, `SELECT to_regclass('migration_rollback_probe') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("DDL must be transactional in PostgreSQL; migration relies on it")
	}
}
