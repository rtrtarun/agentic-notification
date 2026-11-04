package inbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SQLExecutor is the part of *sql.DB the repository needs.
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// PostgresRepository stores state in notification_state and event_trail (see pkg/db).
type PostgresRepository struct{ db SQLExecutor }

func NewPostgresRepository(db SQLExecutor) *PostgresRepository { return &PostgresRepository{db: db} }

const stateColumns = `event_id, read_at, archived_at, deleted_at, acknowledged_at, resolved_at,
	snoozed_until, priority_override, updated_at`

func scanState(row interface{ Scan(...any) error }) (State, error) {
	var s State
	var read, archived, deleted, ack, resolved, snoozed sql.NullTime
	var override sql.NullString
	if err := row.Scan(&s.EventID, &read, &archived, &deleted, &ack, &resolved, &snoozed, &override, &s.UpdatedAt); err != nil {
		return State{}, err
	}
	s.ReadAt, s.ArchivedAt, s.DeletedAt = nullTime(read), nullTime(archived), nullTime(deleted)
	s.AcknowledgedAt, s.ResolvedAt, s.SnoozedUntil = nullTime(ack), nullTime(resolved), nullTime(snoozed)
	s.PriorityOverride = Level(override.String)
	return s, nil
}

func (r *PostgresRepository) All(ctx context.Context) (map[string]State, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+stateColumns+` FROM notification_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]State{}
	for rows.Next() {
		s, err := scanState(rows)
		if err != nil {
			return nil, err
		}
		out[s.EventID] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification state: %w", err)
	}
	return out, nil
}

func (r *PostgresRepository) Get(ctx context.Context, id string) (State, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+stateColumns+` FROM notification_state WHERE event_id = $1`, id)
	if err != nil {
		return State{}, err
	}
	defer rows.Close()
	if rows.Next() {
		return scanState(rows)
	}
	if err := rows.Err(); err != nil {
		return State{}, err
	}
	return State{EventID: id}, nil
}

func (r *PostgresRepository) Save(ctx context.Context, s State) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notification_state (`+stateColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (event_id) DO UPDATE SET
			read_at = EXCLUDED.read_at, archived_at = EXCLUDED.archived_at,
			deleted_at = EXCLUDED.deleted_at, acknowledged_at = EXCLUDED.acknowledged_at,
			resolved_at = EXCLUDED.resolved_at, snoozed_until = EXCLUDED.snoozed_until,
			priority_override = EXCLUDED.priority_override, updated_at = EXCLUDED.updated_at`,
		s.EventID, timeArg(s.ReadAt), timeArg(s.ArchivedAt), timeArg(s.DeletedAt),
		timeArg(s.AcknowledgedAt), timeArg(s.ResolvedAt), timeArg(s.SnoozedUntil),
		sql.NullString{String: string(s.PriorityOverride), Valid: s.PriorityOverride != ""}, s.UpdatedAt)
	return err
}

func (r *PostgresRepository) AppendTrail(ctx context.Context, id string, e TrailEntry) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO event_trail (event_id, step, detail, at) VALUES ($1, $2, $3, $4)`,
		id, e.Step, e.Detail, e.At)
	return err
}

func (r *PostgresRepository) Trail(ctx context.Context, id string) ([]TrailEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT step, detail, at FROM event_trail WHERE event_id = $1 ORDER BY at, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrailEntry
	for rows.Next() {
		var e TrailEntry
		if err := rows.Scan(&e.Step, &e.Detail, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func timeArg(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
