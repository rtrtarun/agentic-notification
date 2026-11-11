package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PoolConfig sizes the connection pool. Zero values keep the database/sql defaults.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Connect opens a pooled connection and verifies it with a ping bounded by ctx.
func Connect(ctx context.Context, databaseURL string, pool PoolConfig) (*sql.DB, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if pool.MaxOpenConns > 0 {
		db.SetMaxOpenConns(pool.MaxOpenConns)
	}
	if pool.MaxIdleConns > 0 {
		db.SetMaxIdleConns(pool.MaxIdleConns)
	}
	if pool.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(pool.ConnMaxLifetime)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// migrationLockID is an arbitrary application-wide key for the migration advisory lock.
const migrationLockID = 727274

// Beginner starts a transaction; *sql.DB satisfies it.
type Beginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// MigrateLocked runs Migrate in one transaction holding a Postgres advisory lock,
// so replicas starting together migrate one at a time and a failure rolls back
// instead of leaving a half-applied schema (Postgres DDL is transactional).
func MigrateLocked(ctx context.Context, db Beginner) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if err := Migrate(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
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
		// Asynchronous attention work (milestone 29): done for events decided at ingest, pending for those the worker must decide.
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS attention_state TEXT NOT NULL DEFAULT 'done'`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS attention_lease_until TIMESTAMPTZ`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS attention_attempts INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS events_attention_work_idx ON events (created_at) WHERE attention_state IN ('pending','processing')`,
		`CREATE INDEX IF NOT EXISTS events_delayed_idx ON events (created_at) WHERE decision_status = 'delayed'`,
		// Agent traces (milestone 31): one run of an agent is a trace; each step may name its parent.
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS trace_id TEXT`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS parent_event_id TEXT`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS agent_name TEXT`,
		`CREATE INDEX IF NOT EXISTS events_trace_id_idx ON events (trace_id) WHERE trace_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS events_thread_id_idx ON events (thread_id)`,
		`CREATE INDEX IF NOT EXISTS events_created_at_idx ON events (created_at)`,
		`CREATE INDEX IF NOT EXISTS events_dedup_key_idx ON events (dedup_key)`,
		// Inbox state: what a person did with an event. Rows go with the event when retention purges it.
		`CREATE TABLE IF NOT EXISTS notification_state (
			event_id          TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
			read_at           TIMESTAMPTZ,
			archived_at       TIMESTAMPTZ,
			deleted_at        TIMESTAMPTZ,
			acknowledged_at   TIMESTAMPTZ,
			resolved_at       TIMESTAMPTZ,
			snoozed_until     TIMESTAMPTZ,
			priority_override TEXT,
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS event_trail (
			id       BIGSERIAL PRIMARY KEY,
			event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
			step     TEXT NOT NULL,
			detail   TEXT NOT NULL DEFAULT '',
			at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS event_trail_event_id_idx ON event_trail (event_id, at)`,
		// One row of workspace preferences (quiet hours, muted sources). See pkg/preferences.
		`CREATE TABLE IF NOT EXISTS preferences (
			id         INT PRIMARY KEY CHECK (id = 1),
			body       JSONB NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// Editable policy rules, kept in evaluation order. See pkg/rules.
		// Per-person preferences, keyed by user ID; a person with no row uses the workspace row above.
		`CREATE TABLE IF NOT EXISTS user_preferences (
			user_id    TEXT PRIMARY KEY,
			body       JSONB NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS policy_rules (
			position INT  NOT NULL,
			id       TEXT PRIMARY KEY,
			body     JSONB NOT NULL
		)`,
		// Queued and logged deliveries. Targets are never stored: they come from configuration
		// when a job is sent. UNIQUE (event_id, channel) makes enqueueing idempotent. See pkg/delivery.
		`CREATE TABLE IF NOT EXISTS delivery_jobs (
			id              TEXT PRIMARY KEY,
			event_id        TEXT NOT NULL,
			channel         TEXT NOT NULL,
			class           TEXT NOT NULL DEFAULT '',
			title           TEXT NOT NULL,
			message         TEXT NOT NULL DEFAULT '',
			grp             TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL,
			attempts        INT  NOT NULL DEFAULT 0,
			next_attempt_at TIMESTAMPTZ NOT NULL,
			lease_until     TIMESTAMPTZ,
			last_error      TEXT NOT NULL DEFAULT '',
			direct          BOOLEAN NOT NULL DEFAULT FALSE,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (event_id, channel)
		)`,
		`CREATE INDEX IF NOT EXISTS delivery_jobs_due_idx ON delivery_jobs (status, next_attempt_at)`,
		`CREATE INDEX IF NOT EXISTS delivery_jobs_created_idx ON delivery_jobs (created_at DESC)`,
		// Accounts, sign-in sessions and the security audit trail. See pkg/auth.
		// Sessions hold the SHA-256 of the cookie token, never the token.
		`CREATE TABLE IF NOT EXISTS users (
			id            TEXT PRIMARY KEY,
			email         TEXT NOT NULL UNIQUE,
			name          TEXT NOT NULL DEFAULT '',
			role          TEXT NOT NULL CHECK (role IN ('admin','member')),
			password_hash TEXT NOT NULL DEFAULT '',
			disabled      BOOLEAN NOT NULL DEFAULT FALSE,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// Sign-up approval: older rows become active.
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'`,
		`CREATE TABLE IF NOT EXISTS user_identities (
			issuer  TEXT NOT NULL,
			subject TEXT NOT NULL,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			PRIMARY KEY (issuer, subject)
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			hash       TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			csrf       TEXT NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions (user_id)`,
		// What people said about notifications, and the always/never choices that came from it.
		// See pkg/feedback and pkg/overrides.
		`CREATE TABLE IF NOT EXISTS feedback (
			id          TEXT PRIMARY KEY,
			event_id    TEXT NOT NULL,
			user_id     TEXT NOT NULL DEFAULT '',
			action      TEXT NOT NULL,
			scope       TEXT NOT NULL DEFAULT '',
			reason      TEXT NOT NULL DEFAULT '',
			override_id TEXT NOT NULL DEFAULT '',
			source      TEXT NOT NULL DEFAULT '',
			event_type  TEXT NOT NULL DEFAULT '',
			repository  TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS feedback_event_idx ON feedback (event_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS feedback_created_idx ON feedback (created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS overrides (
			id         TEXT PRIMARY KEY,
			scope_key  TEXT NOT NULL UNIQUE,
			source     TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			repository TEXT NOT NULL DEFAULT '',
			mode       TEXT NOT NULL CHECK (mode IN ('always','never')),
			created_by TEXT NOT NULL DEFAULT '',
			note       TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// Digests written by Attention Intelligence. See pkg/attention.
		`CREATE TABLE IF NOT EXISTS digests (
			id         TEXT PRIMARY KEY,
			window_end TIMESTAMPTZ NOT NULL,
			body       JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS digests_created_idx ON digests (created_at DESC)`,
		// Suggestions made by the learning engine from stored feedback. See pkg/learning.
		`CREATE TABLE IF NOT EXISTS learning_suggestions (
			id         TEXT PRIMARY KEY,
			status     TEXT NOT NULL CHECK (status IN ('pending','applied','dismissed')),
			body       JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS learning_suggestions_status_idx ON learning_suggestions (status, updated_at DESC)`,
		// Score adjustments proposed by offline tuning and approved by an admin, and the log of tuning runs. See pkg/learning.
		`CREATE TABLE IF NOT EXISTS learning_adjustments (
			id          TEXT PRIMARY KEY,
			scope_key   TEXT NOT NULL UNIQUE,
			source      TEXT NOT NULL,
			event_type  TEXT NOT NULL,
			repository  TEXT NOT NULL DEFAULT '',
			delta       INTEGER NOT NULL,
			status      TEXT NOT NULL CHECK (status IN ('proposed','active','rejected','retired')),
			evidence    JSONB NOT NULL DEFAULT '{}',
			impact      JSONB NOT NULL DEFAULT '{}',
			version     INTEGER NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			resolved_by TEXT NOT NULL DEFAULT '',
			resolved_at TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS learning_adjustments_status_idx ON learning_adjustments (status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS learning_runs (
			id             TEXT PRIMARY KEY,
			status         TEXT NOT NULL CHECK (status IN ('running','done','failed')),
			started_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			finished_at    TIMESTAMPTZ,
			feedback_read  INTEGER NOT NULL DEFAULT 0,
			events_read    INTEGER NOT NULL DEFAULT 0,
			proposals_made INTEGER NOT NULL DEFAULT 0,
			error          TEXT NOT NULL DEFAULT ''
		)`,
		// At most one run may be running at a time, across every service sharing the database.
		`CREATE UNIQUE INDEX IF NOT EXISTS learning_runs_one_running ON learning_runs (status) WHERE status = 'running'`,
		`CREATE INDEX IF NOT EXISTS learning_runs_started_idx ON learning_runs (started_at DESC)`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id     BIGSERIAL PRIMARY KEY,
			at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			actor  TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			ip     TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate schema: %w", err)
		}
	}
	return nil
}
