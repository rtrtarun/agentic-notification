package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

type recordingExecer struct {
	statements []string
	failOn     int // 1-based statement index to fail; 0 = never
}

func (r *recordingExecer) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	r.statements = append(r.statements, query)
	if r.failOn == len(r.statements) {
		return nil, errors.New("boom")
	}
	return nil, nil
}

func TestMigrateStatementsAreIdempotent(t *testing.T) {
	exec := &recordingExecer{}
	if err := Migrate(context.Background(), exec); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(exec.statements) == 0 {
		t.Fatal("expected statements")
	}
	for _, stmt := range exec.statements {
		if !strings.Contains(stmt, "IF NOT EXISTS") {
			t.Fatalf("statement is not idempotent: %s", stmt)
		}
	}
}

func TestMigrateCreatesDedupColumns(t *testing.T) {
	exec := &recordingExecer{}
	if err := Migrate(context.Background(), exec); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	all := strings.Join(exec.statements, "\n")
	for _, col := range []string{"dedup_key", "thread_id", "merged_into"} {
		if !strings.Contains(all, col) {
			t.Fatalf("migration missing column %s", col)
		}
	}
}

func TestMigrateStopsOnFirstError(t *testing.T) {
	exec := &recordingExecer{failOn: 2}
	if err := Migrate(context.Background(), exec); err == nil {
		t.Fatal("expected error")
	}
	if len(exec.statements) != 2 {
		t.Fatalf("expected to stop after failing statement, ran %d", len(exec.statements))
	}
}

func TestMigrateCreatesInboxTables(t *testing.T) {
	exec := &recordingExecer{}
	if err := Migrate(context.Background(), exec); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	all := strings.Join(exec.statements, "\n")
	for _, want := range []string{"notification_state", "event_trail", "ON DELETE CASCADE"} {
		if !strings.Contains(all, want) {
			t.Fatalf("migration missing %s", want)
		}
	}
}

// TestMigrateCreatesEveryStoreTableAndItsLookupIndexes guards the stores added in
// milestones 22 to 25: each table must exist, and each column used for lookup or
// ordering must be indexed (docs/coding-standards.md section 5).
func TestMigrateCreatesEveryStoreTableAndItsLookupIndexes(t *testing.T) {
	exec := &recordingExecer{}
	if err := Migrate(context.Background(), exec); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	all := strings.Join(exec.statements, "\n")
	for _, table := range []string{"delivery_jobs", "users", "user_identities", "sessions", "audit_log", "feedback", "overrides", "digests", "learning_suggestions"} {
		if !strings.Contains(all, "CREATE TABLE IF NOT EXISTS "+table+" (") {
			t.Errorf("migration does not create table %s", table)
		}
	}
	for _, index := range []string{
		"delivery_jobs_due_idx", "delivery_jobs_created_idx", "sessions_user_idx",
		"feedback_event_idx", "feedback_created_idx", "digests_created_idx", "learning_suggestions_status_idx",
	} {
		if !strings.Contains(all, "CREATE INDEX IF NOT EXISTS "+index+" ") {
			t.Errorf("migration does not create index %s", index)
		}
	}
	for _, unique := range []string{"scope_key  TEXT NOT NULL UNIQUE"} {
		if !strings.Contains(all, unique) {
			t.Errorf("migration lost the constraint %q", unique)
		}
	}
}

// TestMigrateTwiceChangesNothing runs the whole migration twice against the recorder
// and checks the second run issues the same statements: nothing depends on state.
func TestMigrateTwiceChangesNothing(t *testing.T) {
	first, second := &recordingExecer{}, &recordingExecer{}
	if err := Migrate(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if strings.Join(first.statements, "\n") != strings.Join(second.statements, "\n") {
		t.Fatal("the migration must be a fixed list of idempotent statements")
	}
}
