package event

import (
	"database/sql"
	"testing"
)

type fakeQueryer struct {
	queries []string
	args    [][]any
}

func (f *fakeQueryer) Exec(query string, args ...any) (sql.Result, error) {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	return nil, nil
}

func (f *fakeQueryer) Query(query string, args ...any) (*sql.Rows, error) {
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

	err := repo.Save(Event{
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
	if len(queryer.args[0]) != 14 {
		t.Fatalf("expected 14 query args, got %d", len(queryer.args[0]))
	}
	if queryer.args[0][4] != AttentionCritical {
		t.Fatalf("expected attention_class critical, got %v", queryer.args[0][4])
	}
}
