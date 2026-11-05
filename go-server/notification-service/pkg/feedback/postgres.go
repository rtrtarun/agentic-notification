package feedback

import (
	"context"
	"database/sql"
)

// DB is the part of *sql.DB the PostgreSQL store needs.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PostgresStore keeps feedback in the feedback table (see pkg/db).
type PostgresStore struct{ db DB }

// NewPostgresStore wraps a database handle.
func NewPostgresStore(db DB) *PostgresStore { return &PostgresStore{db: db} }

// Add implements Store.
func (p *PostgresStore) Add(ctx context.Context, f EventFeedback) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO feedback (id, event_id, user_id, action, scope, reason, override_id, source, event_type, repository, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		f.ID, f.EventID, f.UserID, f.Action, f.Scope, f.Reason, f.OverrideID, f.Source, f.EventType, f.Repository, f.CreatedAt)
	return err
}

// List implements Store.
func (p *PostgresStore) List(ctx context.Context, f Filter) ([]EventFeedback, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 10_000
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, event_id, user_id, action, scope, reason, override_id, source, event_type, repository, created_at
		FROM feedback
		WHERE ($1 = '' OR event_id = $1) AND ($2 = '' OR user_id = $2) AND created_at >= $3
		ORDER BY created_at DESC LIMIT $4`, f.EventID, f.UserID, f.Since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventFeedback
	for rows.Next() {
		var x EventFeedback
		if err := rows.Scan(&x.ID, &x.EventID, &x.UserID, &x.Action, &x.Scope, &x.Reason, &x.OverrideID, &x.Source, &x.EventType, &x.Repository, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
