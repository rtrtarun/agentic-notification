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
