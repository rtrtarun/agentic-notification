package auth

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

// PostgresStore keeps users, sessions and the audit trail in PostgreSQL (see pkg/db).
type PostgresStore struct{ db DB }

// NewPostgresStore wraps a database handle.
func NewPostgresStore(db DB) *PostgresStore { return &PostgresStore{db: db} }

const userColumns = `id, email, name, role, password_hash, disabled, created_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.Disabled, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// isUniqueViolation reports a PostgreSQL unique_violation (SQLSTATE 23505) without importing the driver.
func isUniqueViolation(err error) bool {
	var coded interface{ SQLState() string }
	return errors.As(err, &coded) && coded.SQLState() == "23505"
}

// CreateUser implements Store.
func (p *PostgresStore) CreateUser(ctx context.Context, u User) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO users (`+userColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		u.ID, u.Email, u.Name, u.Role, u.PasswordHash, u.Disabled, u.CreatedAt)
	if isUniqueViolation(err) {
		return ErrEmailTaken
	}
	return err
}

// UserByID implements Store.
func (p *PostgresStore) UserByID(ctx context.Context, id string) (User, error) {
	return scanUser(p.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// UserByEmail implements Store.
func (p *PostgresStore) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(p.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email))
}

// UserByIdentity implements Store.
func (p *PostgresStore) UserByIdentity(ctx context.Context, issuer, subject string) (User, error) {
	return scanUser(p.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.name, u.role, u.password_hash, u.disabled, u.created_at
		FROM users u JOIN user_identities i ON i.user_id = u.id WHERE i.issuer = $1 AND i.subject = $2`, issuer, subject))
}

// LinkIdentity implements Store.
func (p *PostgresStore) LinkIdentity(ctx context.Context, userID, issuer, subject string) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO user_identities (issuer, subject, user_id) VALUES ($1,$2,$3)
		ON CONFLICT (issuer, subject) DO UPDATE SET user_id = EXCLUDED.user_id`, issuer, subject, userID)
	return err
}

// ListUsers implements Store.
func (p *PostgresStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUser implements Store.
func (p *PostgresStore) UpdateUser(ctx context.Context, u User) error {
	res, err := p.db.ExecContext(ctx, `UPDATE users SET name=$2, role=$3, password_hash=$4, disabled=$5 WHERE id=$1`,
		u.ID, u.Name, u.Role, u.PasswordHash, u.Disabled)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountUsers implements Store.
func (p *PostgresStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountActiveAdmins implements Store.
func (p *PostgresStore) CountActiveAdmins(ctx context.Context) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND NOT disabled`).Scan(&n)
	return n, err
}

// CreateSession implements Store.
func (p *PostgresStore) CreateSession(ctx context.Context, s Session) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO sessions (hash, user_id, csrf, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`,
		s.Hash, s.UserID, s.CSRF, s.ExpiresAt, s.CreatedAt)
	return err
}

// SessionByHash implements Store.
func (p *PostgresStore) SessionByHash(ctx context.Context, hash string) (Session, error) {
	var s Session
	err := p.db.QueryRowContext(ctx, `SELECT hash, user_id, csrf, expires_at, created_at FROM sessions WHERE hash = $1`, hash).
		Scan(&s.Hash, &s.UserID, &s.CSRF, &s.ExpiresAt, &s.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return s, err
}

// DeleteSession implements Store.
func (p *PostgresStore) DeleteSession(ctx context.Context, hash string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM sessions WHERE hash = $1`, hash)
	return err
}

// DeleteUserSessions implements Store.
func (p *PostgresStore) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

// DeleteExpiredSessions implements Store.
func (p *PostgresStore) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= $1`, now)
	return err
}

// AddAudit implements Store.
func (p *PostgresStore) AddAudit(ctx context.Context, e AuditEntry) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO audit_log (at, actor, action, target, ip, detail) VALUES ($1,$2,$3,$4,$5,$6)`,
		e.At, e.Actor, e.Action, e.Target, e.IP, e.Detail)
	return err
}

// ListAudit implements Store; newest first.
func (p *PostgresStore) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT at, actor, action, target, ip, detail FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.At, &e.Actor, &e.Action, &e.Target, &e.IP, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
