package event

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type fakeQueryer struct {
	queries []string
	args    [][]any
}

func (f *fakeQueryer) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	return nil, nil
}

func (f *fakeQueryer) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	return nil, nil
}

func (f *fakeQueryer) QueryRow(query string, args ...any) *sql.Row {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	return nil
}

func TestPostgresRepositorySaveUsesExpectedColumns(t *testing.T) {
	queryer := &fakeQueryer{}
	repo := NewPostgresRepository(queryer)

	err := repo.Save(context.Background(), Event{
		ID:             "evt-1",
		Source:         "github",
		EventType:      "pull_request",
		Repository:     "octo/repo",
		AttentionClass: AttentionCritical,
		DecisionState: &DecisionState{
			Status:      DecisionSent,
			Reason:      "security review",
			MatchedRule: "security-alerts",
		},
		Payload: map[string]interface{}{"foo": "bar"},
	})
	if err != nil {
		t.Fatalf("save event: %v", err)
	}

	if len(queryer.queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queryer.queries))
	}
	if len(queryer.args[0]) != 15 {
		t.Fatalf("expected 15 query args, got %d", len(queryer.args[0]))
	}
	if queryer.args[0][14] != AttentionStateDone {
		t.Fatalf("an event decided at ingest must not be pending, got %v", queryer.args[0][14])
	}
	if queryer.args[0][4] != AttentionCritical {
		t.Fatalf("expected attention_class critical, got %v", queryer.args[0][4])
	}
}

func TestInMemoryRepositoryHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := NewInMemoryRepository()
	if err := repo.Save(ctx, Event{ID: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("save: expected context.Canceled, got %v", err)
	}
	if _, err := repo.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("list: expected context.Canceled, got %v", err)
	}
}
