package attention

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// DB is the part of *sql.DB the PostgreSQL store needs.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PostgresDigestStore keeps digests as JSON in the digests table (see pkg/db).
type PostgresDigestStore struct{ db DB }

// NewPostgresDigestStore wraps a database handle.
func NewPostgresDigestStore(db DB) *PostgresDigestStore { return &PostgresDigestStore{db: db} }

// Add implements DigestStore.
func (p *PostgresDigestStore) Add(ctx context.Context, d Digest) error {
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `INSERT INTO digests (id, window_end, body, created_at) VALUES ($1,$2,$3,$4)`, d.ID, d.To, body, d.CreatedAt)
	return err
}

// List implements DigestStore.
func (p *PostgresDigestStore) List(ctx context.Context, limit int) ([]Digest, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx, `SELECT body FROM digests ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Digest
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var d Digest
		if err := json.Unmarshal(body, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Get implements DigestStore.
func (p *PostgresDigestStore) Get(ctx context.Context, id string) (Digest, error) {
	var body []byte
	err := p.db.QueryRowContext(ctx, `SELECT body FROM digests WHERE id = $1`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return Digest{}, ErrDigestNotFound
	}
	if err != nil {
		return Digest{}, err
	}
	var d Digest
	return d, json.Unmarshal(body, &d)
}
