package learning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// DB is the part of *sql.DB the PostgreSQL repository needs.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PostgresRepository keeps suggestions as JSON in the learning_suggestions table (see pkg/db).
type PostgresRepository struct{ db DB }

// NewPostgresRepository wraps a database handle.
func NewPostgresRepository(db DB) *PostgresRepository { return &PostgresRepository{db: db} }

// Save implements Repository.
func (p *PostgresRepository) Save(ctx context.Context, s Suggestion) error {
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO learning_suggestions (id, status, body, created_at, updated_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, body = EXCLUDED.body, updated_at = EXCLUDED.updated_at`,
		s.ID, s.Status, body, s.CreatedAt, s.UpdatedAt)
	return err
}

// Get implements Repository.
func (p *PostgresRepository) Get(ctx context.Context, id string) (Suggestion, error) {
	var body []byte
	err := p.db.QueryRowContext(ctx, `SELECT body FROM learning_suggestions WHERE id = $1`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return Suggestion{}, ErrNotFound
	}
	if err != nil {
		return Suggestion{}, err
	}
	var s Suggestion
	return s, json.Unmarshal(body, &s)
}

// List implements Repository.
func (p *PostgresRepository) List(ctx context.Context, status string) ([]Suggestion, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT body FROM learning_suggestions WHERE ($1 = '' OR status = $1) ORDER BY updated_at DESC, id ASC LIMIT 500`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suggestion
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var s Suggestion
		if err := json.Unmarshal(body, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
