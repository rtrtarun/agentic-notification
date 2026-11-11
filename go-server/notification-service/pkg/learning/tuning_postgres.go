package learning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// PostgresAdjustments keeps adjustments in the learning_adjustments table (see pkg/db).
type PostgresAdjustments struct{ db DB }

// NewPostgresAdjustments wraps a database handle.
func NewPostgresAdjustments(db DB) *PostgresAdjustments { return &PostgresAdjustments{db: db} }

// ScopeKey is the unique key of an adjustment's scope: lower-cased source, event type and repository.
func ScopeKey(source, eventType, repository string) string {
	return strings.ToLower(source) + "\x1f" + strings.ToLower(eventType) + "\x1f" + strings.ToLower(repository)
}

const adjustmentColumns = `id, source, event_type, repository, delta, status, evidence, impact, version, created_at, updated_at, resolved_by, resolved_at`

// Save implements AdjustmentRepository.
func (p *PostgresAdjustments) Save(ctx context.Context, a Adjustment) error {
	evidence, err := json.Marshal(a.Evidence)
	if err != nil {
		return err
	}
	impact, err := json.Marshal(a.Impact)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO learning_adjustments (id, scope_key, source, event_type, repository, delta, status, evidence, impact, version, created_at, updated_at, resolved_by, resolved_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (id) DO UPDATE SET delta = EXCLUDED.delta, status = EXCLUDED.status, evidence = EXCLUDED.evidence, impact = EXCLUDED.impact,
			version = EXCLUDED.version, updated_at = EXCLUDED.updated_at, resolved_by = EXCLUDED.resolved_by, resolved_at = EXCLUDED.resolved_at`,
		a.ID, ScopeKey(a.Source, a.EventType, a.Repository), a.Source, a.EventType, a.Repository, a.Delta, a.Status, evidence, impact,
		a.Version, a.CreatedAt, a.UpdatedAt, a.ResolvedBy, a.ResolvedAt)
	return err
}

// Get implements AdjustmentRepository.
func (p *PostgresAdjustments) Get(ctx context.Context, id string) (Adjustment, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+adjustmentColumns+` FROM learning_adjustments WHERE id = $1`, id)
	a, err := scanAdjustment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Adjustment{}, ErrAdjustmentNotFound
	}
	return a, err
}

// List implements AdjustmentRepository.
func (p *PostgresAdjustments) List(ctx context.Context, status string) ([]Adjustment, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+adjustmentColumns+` FROM learning_adjustments
		WHERE ($1 = '' OR status = $1) ORDER BY updated_at DESC, id ASC LIMIT 500`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Adjustment
	for rows.Next() {
		a, err := scanAdjustment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MaxVersion implements AdjustmentRepository.
func (p *PostgresAdjustments) MaxVersion(ctx context.Context) (int, error) {
	var v int
	err := p.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM learning_adjustments`).Scan(&v)
	return v, err
}

type rowScanner interface{ Scan(dest ...any) error }

func scanAdjustment(r rowScanner) (Adjustment, error) {
	var (
		a                Adjustment
		evidence, impact []byte
		resolved         sql.NullTime
	)
	if err := r.Scan(&a.ID, &a.Source, &a.EventType, &a.Repository, &a.Delta, &a.Status, &evidence, &impact, &a.Version, &a.CreatedAt, &a.UpdatedAt, &a.ResolvedBy, &resolved); err != nil {
		return Adjustment{}, err
	}
	if err := json.Unmarshal(evidence, &a.Evidence); err != nil {
		return Adjustment{}, err
	}
	if err := json.Unmarshal(impact, &a.Impact); err != nil {
		return Adjustment{}, err
	}
	if resolved.Valid {
		t := resolved.Time
		a.ResolvedAt = &t
	}
	return a, nil
}

// PostgresRuns keeps the run log in the learning_runs table. A partial unique index allows one running run at a time.
type PostgresRuns struct{ db DB }

// NewPostgresRuns wraps a database handle.
func NewPostgresRuns(db DB) *PostgresRuns { return &PostgresRuns{db: db} }

// Start implements RunRepository.
func (p *PostgresRuns) Start(ctx context.Context, r Run, staleBefore time.Time) error {
	if _, err := p.db.ExecContext(ctx, `UPDATE learning_runs SET status = 'failed', finished_at = $2, error = $3
		WHERE status = 'running' AND started_at < $1`, staleBefore, r.StartedAt, abandonedRun); err != nil {
		return err
	}
	res, err := p.db.ExecContext(ctx, `INSERT INTO learning_runs (id, status, started_at) VALUES ($1, 'running', $2)
		ON CONFLICT (status) WHERE status = 'running' DO NOTHING`, r.ID, r.StartedAt)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrRunInProgress
	}
	return nil
}

// Finish implements RunRepository.
func (p *PostgresRuns) Finish(ctx context.Context, r Run) error {
	_, err := p.db.ExecContext(ctx, `UPDATE learning_runs SET status = $2, finished_at = $3, feedback_read = $4, events_read = $5, proposals_made = $6, error = $7
		WHERE id = $1`, r.ID, r.Status, r.FinishedAt, r.FeedbackRead, r.EventsRead, r.ProposalsMade, r.Error)
	return err
}

// List implements RunRepository.
func (p *PostgresRuns) List(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := p.db.QueryContext(ctx, `SELECT id, status, started_at, finished_at, feedback_read, events_read, proposals_made, error
		FROM learning_runs ORDER BY started_at DESC, id ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var (
			r        Run
			finished sql.NullTime
		)
		if err := rows.Scan(&r.ID, &r.Status, &r.StartedAt, &finished, &r.FeedbackRead, &r.EventsRead, &r.ProposalsMade, &r.Error); err != nil {
			return nil, err
		}
		if finished.Valid {
			t := finished.Time
			r.FinishedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
