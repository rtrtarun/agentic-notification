package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/agentpulse/notification-service/pkg/policy"
)

// MemoryStore keeps rules in process. Used without a database and in tests.
type MemoryStore struct {
	mu    sync.Mutex
	rules []policy.Rule
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

func (m *MemoryStore) List(ctx context.Context) ([]policy.Rule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]policy.Rule(nil), m.rules...), nil
}

func (m *MemoryStore) Replace(ctx context.Context, rules []policy.Rule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = append([]policy.Rule(nil), rules...)
	return nil
}

// DB is the part of *sql.DB the PostgreSQL store needs.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// PostgresStore keeps rules in the policy_rules table (see pkg/db).
type PostgresStore struct{ db DB }

func NewPostgresStore(db DB) *PostgresStore { return &PostgresStore{db: db} }

func (p *PostgresStore) List(ctx context.Context) ([]policy.Rule, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT body FROM policy_rules ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []policy.Rule
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var r policy.Rule
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("decode stored rule: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rules: %w", err)
	}
	return out, nil
}

// Replace swaps the whole list in one transaction, so a failure never leaves a
// half-written rule set that could change how events are classified.
func (p *PostgresStore) Replace(ctx context.Context, rules []policy.Rule) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if _, err := tx.ExecContext(ctx, `DELETE FROM policy_rules`); err != nil {
		return err
	}
	for i, r := range rules {
		body, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO policy_rules (position, id, body) VALUES ($1, $2, $3)`, i, r.ID, body); err != nil {
			return err
		}
	}
	return tx.Commit()
}
