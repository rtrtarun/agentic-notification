package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// DefaultSessionTTL is how long a browser stays signed in.
const DefaultSessionTTL = 12 * time.Hour

// Errors the HTTP layer maps to status codes.
var (
	// ErrInvalidCredentials is deliberately the same for an unknown email, a wrong
	// password, a disabled account and an account with no password, so the
	// response never says which accounts exist.
	ErrInvalidCredentials = errors.New("incorrect email or password")
	ErrLocked             = errors.New("too many failed sign-ins; try again later")
	ErrInvalidInput       = errors.New("invalid input")
	ErrLastAdmin          = errors.New("there must be at least one enabled admin")
	ErrNotAllowed         = errors.New("this account is not allowed to sign in")
	ErrUnauthenticated    = errors.New("not signed in")
)

// Service is the use-case layer: sign-in, sessions and user management.
type Service struct {
	store   Store
	ttl     time.Duration
	now     func() time.Time
	limiter *Limiter
}

// NewService builds a service. A zero ttl uses DefaultSessionTTL.
func NewService(store Store, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &Service{store: store, ttl: ttl, now: time.Now, limiter: NewLimiter(DefaultMaxFailures, DefaultLockout)}
}

// WithClock replaces the clock; for tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	s.limiter.now = now
	return s
}

// WithLimiter replaces the login throttle.
func (s *Service) WithLimiter(l *Limiter) *Service {
	l.now = s.now
	s.limiter = l
	return s
}

// SessionTTL is the lifetime of a new session.
func (s *Service) SessionTTL() time.Duration { return s.ttl }

// HasUsers reports whether any account exists, so the console knows whether to offer sign-in.
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	n, err := s.store.CountUsers(ctx)
	return n > 0, err
}

// Bootstrap creates the first admin when there are no users. It does nothing
// otherwise, so a restart with the same settings cannot reset a password.
func (s *Service) Bootstrap(ctx context.Context, email, password string) (bool, error) {
	n, err := s.store.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	if _, err := s.createUser(ctx, "system", email, "", RoleAdmin, password); err != nil {
		return false, fmt.Errorf("create first admin: %w", err)
	}
	return true, nil
}

// Login checks a password and starts a session. It returns the cookie token,
// which is shown once and never stored, only its hash.
func (s *Service) Login(ctx context.Context, email, password, ip string) (string, Session, User, error) {
	email = NormalizeEmail(email)
	if locked := s.limiter.Locked("email:"+email, "ip:"+ip); locked {
		s.audit(ctx, email, "login.locked", "", ip, "")
		return "", Session{}, User{}, ErrLocked
	}
	u, err := s.store.UserByEmail(ctx, email)
	hash := dummyHash
	if err == nil && u.PasswordHash != "" {
		hash = u.PasswordHash
	}
	ok := VerifyPassword(hash, password) && err == nil && u.PasswordHash != "" && !u.Disabled
	if !ok {
		s.limiter.Fail("email:"+email, "ip:"+ip)
		s.audit(ctx, email, "login.failed", "", ip, "")
		return "", Session{}, User{}, ErrInvalidCredentials
	}
	s.limiter.Reset("email:" + email)
	token, sess, err := s.startSession(ctx, u)
	if err != nil {
		return "", Session{}, User{}, err
	}
	s.audit(ctx, u.Email, "login", "", ip, "password")
	return token, sess, u, nil
}

// LoginFederated starts a session for a person an identity provider has
// already verified. The account is found by (issuer, subject), then by email
// (and linked); otherwise it is created as a member, but only when the email's
// domain is on the allow-list. An empty allow-list never creates accounts.
func (s *Service) LoginFederated(ctx context.Context, issuer, subject, email, name string, allowedDomains []string, ip string) (string, Session, User, error) {
	email = NormalizeEmail(email)
	if issuer == "" || subject == "" {
		return "", Session{}, User{}, ErrNotAllowed
	}
	u, err := s.store.UserByIdentity(ctx, issuer, subject)
	if errors.Is(err, ErrNotFound) {
		if _, perr := mail.ParseAddress(email); perr != nil {
			s.audit(ctx, email, "login.denied", "", ip, "no usable email from identity provider")
			return "", Session{}, User{}, ErrNotAllowed
		}
		u, err = s.store.UserByEmail(ctx, email)
		if errors.Is(err, ErrNotFound) {
			if !domainAllowed(email, allowedDomains) {
				s.audit(ctx, email, "login.denied", "", ip, "domain not allowed")
				return "", Session{}, User{}, ErrNotAllowed
			}
			u = User{ID: newID(), Email: email, Name: strings.TrimSpace(name), Role: RoleMember, CreatedAt: s.now()}
			if err = s.store.CreateUser(ctx, u); err != nil {
				return "", Session{}, User{}, err
			}
			s.audit(ctx, email, "user.provisioned", u.ID, ip, "from identity provider")
		} else if err != nil {
			return "", Session{}, User{}, err
		}
		if err = s.store.LinkIdentity(ctx, u.ID, issuer, subject); err != nil {
			return "", Session{}, User{}, err
		}
	} else if err != nil {
		return "", Session{}, User{}, err
	}
	if u.Disabled {
		s.audit(ctx, u.Email, "login.denied", "", ip, "disabled")
		return "", Session{}, User{}, ErrNotAllowed
	}
	token, sess, err := s.startSession(ctx, u)
	if err != nil {
		return "", Session{}, User{}, err
	}
	s.audit(ctx, u.Email, "login", "", ip, "sso")
	return token, sess, u, nil
}

func domainAllowed(email string, allowed []string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domain := email[at+1:]
	for _, d := range allowed {
		if strings.EqualFold(strings.TrimSpace(d), domain) {
			return true
		}
	}
	return false
}

func (s *Service) startSession(ctx context.Context, u User) (string, Session, error) {
	token, err := randomToken()
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", Session{}, err
	}
	now := s.now()
	sess := Session{Hash: hashToken(token), UserID: u.ID, CSRF: csrf, CreatedAt: now, ExpiresAt: now.Add(s.ttl)}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return "", Session{}, err
	}
	// Opportunistic cleanup keeps the table small without a background job.
	_ = s.store.DeleteExpiredSessions(ctx, now)
	return token, sess, nil
}

// Authenticate resolves a cookie token to a signed-in, enabled user.
func (s *Service) Authenticate(ctx context.Context, token string) (User, Session, error) {
	if token == "" {
		return User{}, Session{}, ErrUnauthenticated
	}
	sess, err := s.store.SessionByHash(ctx, hashToken(token))
	if err != nil || !sess.ExpiresAt.After(s.now()) {
		return User{}, Session{}, ErrUnauthenticated
	}
	u, err := s.store.UserByID(ctx, sess.UserID)
	if err != nil || u.Disabled {
		return User{}, Session{}, ErrUnauthenticated
	}
	return u, sess, nil
}

// Logout ends one session.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, hashToken(token))
}

// ValidCSRF compares a request's CSRF token with the session's, in constant time.
func ValidCSRF(sess Session, got string) bool {
	return sess.CSRF != "" && subtle.ConstantTimeCompare([]byte(sess.CSRF), []byte(got)) == 1
}

// CreateUser adds an account. actor is the email of the admin doing it.
func (s *Service) CreateUser(ctx context.Context, actor, email, name, role, password string) (User, error) {
	return s.createUser(ctx, actor, email, name, role, password)
}

func (s *Service) createUser(ctx context.Context, actor, email, name, role, password string) (User, error) {
	email = NormalizeEmail(email)
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		return User{}, fmt.Errorf("%w: a valid email is required", ErrInvalidInput)
	}
	if role != RoleAdmin && role != RoleMember {
		return User{}, fmt.Errorf("%w: role must be admin or member", ErrInvalidInput)
	}
	u := User{ID: newID(), Email: email, Name: strings.TrimSpace(name), Role: role, CreatedAt: s.now()}
	if password != "" {
		if err := CheckPassword(password); err != nil {
			return User{}, err
		}
		hash, err := HashPassword(password)
		if err != nil {
			return User{}, err
		}
		u.PasswordHash = hash
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		return User{}, err
	}
	s.audit(ctx, actor, "user.created", u.ID, "", "role "+role)
	return u, nil
}

// UserPatch is a partial update; nil fields are left alone.
type UserPatch struct {
	Name     *string
	Role     *string
	Disabled *bool
	Password *string
}

// UpdateUser changes an account. Demoting or disabling the last enabled admin
// is refused, and disabling an account or changing its password ends its sessions.
func (s *Service) UpdateUser(ctx context.Context, actor, id string, p UserPatch) (User, error) {
	u, err := s.store.UserByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	wasActiveAdmin := u.Role == RoleAdmin && !u.Disabled
	endSessions := false
	if p.Name != nil {
		u.Name = strings.TrimSpace(*p.Name)
	}
	if p.Role != nil {
		if *p.Role != RoleAdmin && *p.Role != RoleMember {
			return User{}, fmt.Errorf("%w: role must be admin or member", ErrInvalidInput)
		}
		u.Role = *p.Role
	}
	if p.Disabled != nil {
		endSessions = endSessions || (*p.Disabled && !u.Disabled)
		u.Disabled = *p.Disabled
	}
	if p.Password != nil {
		if err := CheckPassword(*p.Password); err != nil {
			return User{}, err
		}
		if u.PasswordHash, err = HashPassword(*p.Password); err != nil {
			return User{}, err
		}
		endSessions = true
	}
	if wasActiveAdmin && !(u.Role == RoleAdmin && !u.Disabled) {
		if n, err := s.store.CountActiveAdmins(ctx); err != nil {
			return User{}, err
		} else if n <= 1 {
			return User{}, ErrLastAdmin
		}
	}
	if err := s.store.UpdateUser(ctx, u); err != nil {
		return User{}, err
	}
	if endSessions {
		if err := s.store.DeleteUserSessions(ctx, u.ID); err != nil {
			return User{}, err
		}
	}
	s.audit(ctx, actor, "user.updated", u.ID, "", describePatch(p))
	return u, nil
}

func describePatch(p UserPatch) string {
	var parts []string
	if p.Name != nil {
		parts = append(parts, "name")
	}
	if p.Role != nil {
		parts = append(parts, "role "+*p.Role)
	}
	if p.Disabled != nil {
		parts = append(parts, fmt.Sprintf("disabled=%t", *p.Disabled))
	}
	if p.Password != nil {
		parts = append(parts, "password") // never the value
	}
	return strings.Join(parts, ", ")
}

// ChangePassword lets a person change their own password. Their other sessions end.
func (s *Service) ChangePassword(ctx context.Context, userID, current, next string) error {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.PasswordHash == "" || !VerifyPassword(u.PasswordHash, current) {
		s.audit(ctx, u.Email, "password.change.failed", "", "", "")
		return ErrInvalidCredentials
	}
	if err := CheckPassword(next); err != nil {
		return err
	}
	if u.PasswordHash, err = HashPassword(next); err != nil {
		return err
	}
	if err := s.store.UpdateUser(ctx, u); err != nil {
		return err
	}
	if err := s.store.DeleteUserSessions(ctx, u.ID); err != nil {
		return err
	}
	s.audit(ctx, u.Email, "password.changed", u.ID, "", "")
	return nil
}

// Users lists accounts.
func (s *Service) Users(ctx context.Context) ([]User, error) { return s.store.ListUsers(ctx) }

// Audit returns recent audit entries, newest first.
func (s *Service) Audit(ctx context.Context, limit int) ([]AuditEntry, error) {
	return s.store.ListAudit(ctx, limit)
}

// audit records an entry. A failure to write it must never block the action it describes.
func (s *Service) audit(ctx context.Context, actor, action, target, ip, detail string) {
	_ = s.store.AddAudit(ctx, AuditEntry{At: s.now(), Actor: actor, Action: action, Target: target, IP: ip, Detail: detail})
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
