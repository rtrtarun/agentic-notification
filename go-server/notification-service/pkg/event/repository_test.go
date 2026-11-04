package event

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestInMemorySaveIsIdempotentOnID(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := repo.Save(ctx, Event{ID: "same", Source: "github"}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	if stored, _ := repo.List(ctx); len(stored) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(stored))
	}
}

func TestInMemoryExists(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	repo.Save(ctx, Event{ID: "a"})

	if ok, err := repo.Exists(ctx, "a"); err != nil || !ok {
		t.Fatalf("Exists(a) = %v, %v", ok, err)
	}
	if ok, err := repo.Exists(ctx, "missing"); err != nil || ok {
		t.Fatalf("Exists(missing) = %v, %v", ok, err)
	}
}

func TestInMemoryExistsHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewInMemoryRepository().Exists(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestInMemoryListIsCappedToNewest(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	total := DefaultListLimit + 5
	for i := 0; i < total; i++ {
		repo.Save(ctx, Event{ID: fmt.Sprintf("e%d", i)})
	}
	stored, _ := repo.List(ctx)
	if len(stored) != DefaultListLimit {
		t.Fatalf("expected %d events, got %d", DefaultListLimit, len(stored))
	}
	if stored[0].ID != "e5" || stored[len(stored)-1].ID != fmt.Sprintf("e%d", total-1) {
		t.Fatalf("expected the newest events oldest-first, got %s..%s", stored[0].ID, stored[len(stored)-1].ID)
	}
}

type fakeResult struct{ affected int64 }

func (fakeResult) LastInsertId() (int64, error)   { return 0, nil }
func (f fakeResult) RowsAffected() (int64, error) { return f.affected, nil }

type execRecorder struct {
	fakeQueryer
	result sql.Result
	err    error
}

func (e *execRecorder) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	e.fakeQueryer.ExecContext(ctx, query, args...)
	return e.result, e.err
}

func TestPostgresSaveIsIdempotent(t *testing.T) {
	q := &fakeQueryer{}
	if err := NewPostgresRepository(q).Save(context.Background(), Event{ID: "e", Source: "github", EventType: "star"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.Contains(q.queries[0], "ON CONFLICT (id) DO NOTHING") {
		t.Fatalf("save must be idempotent on id:\n%s", q.queries[0])
	}
}

func TestPostgresDeleteOlderThan(t *testing.T) {
	cutoff := time.Now().Add(-time.Hour)
	q := &execRecorder{result: fakeResult{affected: 7}}

	deleted, err := NewPostgresRepository(q).DeleteOlderThan(context.Background(), cutoff)
	if err != nil || deleted != 7 {
		t.Fatalf("DeleteOlderThan = %d, %v", deleted, err)
	}
	if !strings.Contains(q.queries[0], "DELETE FROM events WHERE created_at <") {
		t.Fatalf("unexpected query: %s", q.queries[0])
	}
	if got, ok := q.args[0][0].(time.Time); !ok || !got.Equal(cutoff) {
		t.Fatalf("cutoff not passed as a parameter: %v", q.args[0])
	}
}

func TestPostgresDeleteOlderThanPropagatesError(t *testing.T) {
	boom := errors.New("boom")
	q := &execRecorder{err: boom}
	if _, err := NewPostgresRepository(q).DeleteOlderThan(context.Background(), time.Now()); !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
}

func TestPostgresDeleteWithNilResult(t *testing.T) {
	q := &execRecorder{}
	if n, err := NewPostgresRepository(q).DeleteOlderThan(context.Background(), time.Now()); err != nil || n != 0 {
		t.Fatalf("got %d, %v", n, err)
	}
}

var (
	_ Repository = (*InMemoryRepository)(nil)
	_ Repository = (*PostgresRepository)(nil)
	_ Purger     = (*PostgresRepository)(nil)
)
