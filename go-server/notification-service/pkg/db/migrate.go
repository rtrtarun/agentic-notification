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
