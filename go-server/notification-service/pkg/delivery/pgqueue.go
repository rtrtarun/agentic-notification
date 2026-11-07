package delivery

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DB is the part of *sql.DB the PostgreSQL store needs.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PostgresStore keeps the delivery queue in the delivery_jobs table (see pkg/db).
type PostgresStore struct{ db DB }

// NewPostgresStore wraps a database handle.
func NewPostgresStore(db DB) *PostgresStore { return &PostgresStore{db: db} }

const jobColumns = `id, event_id, channel, class, title, message, grp, status, attempts,
	next_attempt_at, COALESCE(lease_until, 'epoch'), last_error, direct, created_at, updated_at`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.EventID, &j.Channel, &j.Class, &j.Title, &j.Message, &j.Group, &j.Status,
		&j.Attempts, &j.NextAttemptAt, &j.LeaseUntil, &j.LastError, &j.Direct, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// Enqueue implements Store.
func (p *PostgresStore) Enqueue(ctx context.Context, j Job) (bool, error) {
	res, err := p.db.ExecContext(ctx, `
		INSERT INTO delivery_jobs (id, event_id, channel, class, title, message, grp, status, attempts,
			next_attempt_at, last_error, direct, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (event_id, channel) DO NOTHING`,
		j.ID, j.EventID, j.Channel, j.Class, j.Title, j.Message, j.Group, j.Status, j.Attempts,
		j.NextAttemptAt, j.LastError, j.Direct, j.CreatedAt, j.UpdatedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Record implements Store.
func (p *PostgresStore) Record(ctx context.Context, j Job) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO delivery_jobs (id, event_id, channel, class, title, message, grp, status, attempts,
			next_attempt_at, last_error, direct, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (event_id, channel) DO UPDATE SET status = EXCLUDED.status, attempts = EXCLUDED.attempts,
			last_error = EXCLUDED.last_error, updated_at = EXCLUDED.updated_at`,
		j.ID, j.EventID, j.Channel, j.Class, j.Title, j.Message, j.Group, j.Status, j.Attempts,
		j.NextAttemptAt, j.LastError, j.Direct, j.CreatedAt, j.UpdatedAt)
	return err
}

// Claim implements Store. SKIP LOCKED lets several instances share the queue.
func (p *PostgresStore) Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Job, error) {
	rows, err := p.db.QueryContext(ctx, `
		UPDATE delivery_jobs SET status = 'sending', attempts = attempts + 1, lease_until = $2, updated_at = $1
		WHERE id IN (
			SELECT id FROM delivery_jobs
			WHERE (status = 'pending' AND next_attempt_at <= $1) OR (status = 'sending' AND lease_until <= $1)
			ORDER BY next_attempt_at LIMIT $3 FOR UPDATE SKIP LOCKED)
		RETURNING `+jobColumns, now, now.Add(lease), limit)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// Complete implements Store.
func (p *PostgresStore) Complete(ctx context.Context, id string, now time.Time) error {
	return p.exec(ctx, `UPDATE delivery_jobs SET status = 'sent', last_error = '', updated_at = $2 WHERE id = $1`, id, now)
}

// Fail implements Store.
func (p *PostgresStore) Fail(ctx context.Context, id, errText string, retryAt *time.Time, now time.Time) error {
	if retryAt == nil {
		return p.exec(ctx, `UPDATE delivery_jobs SET status = 'dead', last_error = $2, updated_at = $3 WHERE id = $1`, id, errText, now)
	}
	return p.exec(ctx, `UPDATE delivery_jobs SET status = 'pending', last_error = $2, next_attempt_at = $4, updated_at = $3 WHERE id = $1`,
		id, errText, now, *retryAt)
}

// Requeue implements Store.
func (p *PostgresStore) Requeue(ctx context.Context, id string, now time.Time) error {
	res, err := p.db.ExecContext(ctx, `
		UPDATE delivery_jobs SET status = 'pending', attempts = 0, next_attempt_at = $2, updated_at = $2
		WHERE id = $1 AND status = 'dead'`, id, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var status string
	err = p.db.QueryRowContext(ctx, `SELECT status FROM delivery_jobs WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrNotRetryable
}

func (p *PostgresStore) exec(ctx context.Context, query string, args ...any) error {
	res, err := p.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// List implements Store.
func (p *PostgresStore) List(ctx context.Context, limit int) ([]Job, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM delivery_jobs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// Counts implements Store.
func (p *PostgresStore) Counts(ctx context.Context) (map[Status]int, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM delivery_jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[Status]int{}
	for rows.Next() {
		var s Status
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

// CountSent implements Store.
func (p *PostgresStore) CountSent(ctx context.Context, channel string, since time.Time) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM delivery_jobs WHERE channel = $1 AND status = 'sent' AND updated_at >= $2`, channel, since).Scan(&n)
	return n, err
}

func collect(rows *sql.Rows) ([]Job, error) {
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
