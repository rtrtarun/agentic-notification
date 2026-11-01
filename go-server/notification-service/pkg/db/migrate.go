package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

func Connect(ctx context.Context, databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// Execer runs a statement; *sql.DB satisfies it.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Migrate creates the schema. Every statement is idempotent.
func Migrate(ctx context.Context, db Execer) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS events (
			id                     TEXT PRIMARY KEY,
			source                 TEXT NOT NULL,
			event_type             TEXT NOT NULL,
			repository             TEXT,
			attention_class        TEXT NOT NULL DEFAULT 'standard',
			decision_status        TEXT,
			decision_reason        TEXT,
			decision_model_version TEXT,
			decision_confidence    DOUBLE PRECISION,
			decision_matched_rule  TEXT,
			payload                JSONB NOT NULL DEFAULT '{}',
			created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS dedup_key TEXT`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS thread_id TEXT`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS merged_into TEXT`,
		`CREATE INDEX IF NOT EXISTS events_thread_id_idx ON events (thread_id)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate events table: %w", err)
		}
	}
	return nil
}
