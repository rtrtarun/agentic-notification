package preferences

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// DB is the part of *sql.DB the PostgreSQL store needs.
type DB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PostgresStore keeps the preferences in one row of the preferences table (see pkg/db).
type PostgresStore struct{ db DB }

func NewPostgresStore(db DB) *PostgresStore { return &PostgresStore{db: db} }

func (p *PostgresStore) Load(ctx context.Context) (Preferences, bool, error) {
	var body []byte
	err := p.db.QueryRowContext(ctx, `SELECT body FROM preferences WHERE id = 1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return Preferences{}, false, nil
	}
	if err != nil {
		return Preferences{}, false, err
	}
	var out Preferences
	if err := json.Unmarshal(body, &out); err != nil {
		return Preferences{}, false, err
	}
	return out, true, nil
}

func (p *PostgresStore) Save(ctx context.Context, prefs Preferences) error {
	body, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO preferences (id, body, updated_at) VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE SET body = EXCLUDED.body, updated_at = NOW()`, body)
	return err
}
