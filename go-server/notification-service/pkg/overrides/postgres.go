package overrides

import (
	"context"
	"database/sql"
)

// DB is the part of *sql.DB the PostgreSQL store needs.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PostgresStore keeps overrides in the overrides table (see pkg/db).
type PostgresStore struct{ db DB }

// NewPostgresStore wraps a database handle.
func NewPostgresStore(db DB) *PostgresStore { return &PostgresStore{db: db} }

// Load implements Store.
func (p *PostgresStore) Load(ctx context.Context) ([]Override, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id, source, event_type, repository, mode, created_by, note, created_at FROM overrides ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Override
	for rows.Next() {
		var o Override
		if err := rows.Scan(&o.ID, &o.Source, &o.EventType, &o.Repository, &o.Mode, &o.CreatedBy, &o.Note, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Save implements Store. The same scope keeps its id and creation time and takes the new mode.
func (p *PostgresStore) Save(ctx context.Context, o Override) (Override, error) {
	err := p.db.QueryRowContext(ctx, `
		INSERT INTO overrides (id, scope_key, source, event_type, repository, mode, created_by, note, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (scope_key) DO UPDATE SET mode = EXCLUDED.mode, created_by = EXCLUDED.created_by, note = EXCLUDED.note
		RETURNING id, created_at`,
		o.ID, scopeKey(o), o.Source, o.EventType, o.Repository, o.Mode, o.CreatedBy, o.Note, o.CreatedAt).Scan(&o.ID, &o.CreatedAt)
	return o, err
}

// Delete implements Store.
func (p *PostgresStore) Delete(ctx context.Context, id string) error {
	res, err := p.db.ExecContext(ctx, `DELETE FROM overrides WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
