package server

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/agentpulse/notification-service/pkg/auth"
	"github.com/agentpulse/notification-service/pkg/util"
)

const (
	authPrefix    = "/api/auth"
	usersPrefix   = "/api/users"
	sessionCookie = "attn_session"
	oidcCookie    = "attn_oidc"
	csrfHeader    = "X-CSRF-Token"
	apiKeyLabel   = "api-key"
)

type principalKey struct{}

// principal is who made a request: a signed-in person, or a service holding the API key.
type principal struct {
	user    auth.User
	session auth.Session
	service bool
}

func (p principal) admin() bool { return p.service || p.user.Role == auth.RoleAdmin }

// actor names the caller in the audit trail.
func (p principal) actor() string {
	if p.service {
		return apiKeyLabel
	}
	return p.user.Email
}

func principalFrom(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(principalKey{}).(principal)
	return p, ok
}

func withPrincipal(r *http.Request, p principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
}

// requireAdmin answers 403 and returns false unless the caller is an admin or holds the API key.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if p, ok := principalFrom(r.Context()); ok && p.admin() {
		return true
	}
	util.WriteJSON(w, http.StatusForbidden, util.ErrorResponse{Error: "admin access required"})
	return false
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// clientIP is the connecting address. Behind a proxy this is the proxy, so the
// per-address throttle is then looser than intended; the per-account one is not affected.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type userView struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

func viewOf(u auth.User) userView {
	return userView{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Disabled: u.Disabled}
}

// secureCookie decides the Secure flag. SameSite=None always needs it.
func (s *Server) secureCookie(r *http.Request) bool {
	if s.cfg.AuthCookieSameSite == "none" {
		return true
	}
	switch s.cfg.AuthCookieSecure {
	case "true":
		return true
	case "false":
		return false
	}
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) sameSite() http.SameSite {
	switch s.cfg.AuthCookieSameSite {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the request scheme (secureCookie) so local http development works
		Name: sessionCookie, Value: token, Path: "/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()),
		HttpOnly: true, Secure: s.secureCookie(r), SameSite: s.sameSite(),
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the request scheme (secureCookie) so local http development works
		Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: true, Secure: s.secureCookie(r), SameSite: s.sameSite(),
	})
}

// authStatusHandler tells the sign-in screen what is available. It is public and reveals nothing sensitive.
//
//	GET /api/auth/status
func (s *Server) authStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	has, err := s.auth.HasUsers(r.Context())
	if err != nil {
		s.logFor(r.Context()).Error("auth status failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		return
	}
	sso := map[string]any{"enabled": s.oidc != nil}
	if s.oidc != nil {
		sso["name"], sso["startPath"] = s.oidc.Name(), authPrefix+"/oidc/start"
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"sessions": true, "needsSetup": !has, "sso": sso})
}

// loginHandler checks a password and starts a session.
//
//	POST /api/auth/login {"email","password"}
func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	var in struct{ Email, Password string }
	if !s.decodeJSON(w, r, &in) {
		return
	}
	token, sess, user, err := s.auth.Login(r.Context(), in.Email, in.Password, clientIP(r))
	switch {
	case errors.Is(err, auth.ErrLocked):
		w.Header().Set("Retry-After", strconv.Itoa(int(auth.DefaultLockout.Seconds())))
		util.WriteJSON(w, http.StatusTooManyRequests, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, auth.ErrInvalidCredentials):
		util.WriteJSON(w, http.StatusUnauthorized, util.ErrorResponse{Error: err.Error()})
	case err != nil:
		s.logFor(r.Context()).Error("login failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	default:
		s.setSessionCookie(w, r, token, sess.ExpiresAt)
		util.WriteJSON(w, http.StatusOK, map[string]any{"user": viewOf(user), "csrfToken": sess.CSRF, "expiresAt": sess.ExpiresAt})
	}
}

// logoutHandler ends the current session.
//
//	POST /api/auth/logout
func (s *Server) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.auth.Logout(r.Context(), c.Value); err != nil {
			s.logFor(r.Context()).Error("logout failed", "error", err)
		}
	}
	s.clearCookie(w, r, sessionCookie, "/")
	w.WriteHeader(http.StatusNoContent)
}

// meHandler says who the caller is. The console calls it on load to decide whether to show sign-in.
//
//	GET /api/auth/me
func (s *Server) meHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	p, _ := principalFrom(r.Context())
	if p.service {
		util.WriteJSON(w, http.StatusOK, map[string]any{"via": "apikey", "user": userView{Email: apiKeyLabel, Name: "Service", Role: auth.RoleAdmin}})
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"via": "session", "user": viewOf(p.user), "csrfToken": p.session.CSRF})
}

// passwordHandler lets a signed-in person change their own password.
//
//	POST /api/auth/password {"current","next"}
func (s *Server) passwordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	p, _ := principalFrom(r.Context())
	if p.service {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "the API key has no password"})
		return
	}
	var in struct{ Current, Next string }
	if !s.decodeJSON(w, r, &in) {
		return
	}
	err := s.auth.ChangePassword(r.Context(), p.user.ID, in.Current, in.Next)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "the current password is incorrect"})
	case errors.Is(err, auth.ErrWeakPassword):
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
	case err != nil:
		s.logFor(r.Context()).Error("change password failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	default:
		// All sessions ended, including this one; the person signs in again with the new password.
		s.clearCookie(w, r, sessionCookie, "/")
		w.WriteHeader(http.StatusNoContent)
	}
}

// oidcStartHandler sends the browser to the identity provider.
//
//	GET /api/auth/oidc/start
func (s *Server) oidcStartHandler(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "single sign-on is not configured"})
		return
	}
	authURL, tx, err := s.oidc.Begin(r.Context())
	if err != nil {
		s.logFor(r.Context()).Error("sso start failed", "error", err)
		s.redirectToConsole(w, r, "failed")
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the request scheme (secureCookie) so local http development works
		Name: oidcCookie, Value: auth.SealTransaction(s.authSecret, tx), Path: authPrefix + "/oidc", MaxAge: 600,
		HttpOnly: true, Secure: s.secureCookie(r), SameSite: http.SameSiteLaxMode, // Lax: the provider's redirect is a cross-site navigation
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// oidcCallbackHandler finishes single sign-on and starts a session.
//
//	GET /api/auth/oidc/callback?code=&state=
func (s *Server) oidcCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "single sign-on is not configured"})
		return
	}
	s.clearCookie(w, r, oidcCookie, authPrefix+"/oidc")
	q := r.URL.Query()
	if q.Get("error") != "" {
		s.redirectToConsole(w, r, "denied")
		return
	}
	c, err := r.Cookie(oidcCookie)
	if err != nil {
		s.redirectToConsole(w, r, "failed")
		return
	}
	tx, err := auth.OpenTransaction(s.authSecret, c.Value)
	if err != nil {
		s.redirectToConsole(w, r, "failed")
		return
	}
	claims, err := s.oidc.Complete(r.Context(), tx, q.Get("state"), q.Get("code"))
	if err != nil {
		s.logFor(r.Context()).Warn("sso sign-in rejected", "error", err)
		s.redirectToConsole(w, r, "failed")
		return
	}
	token, sess, _, err := s.auth.LoginFederated(r.Context(), claims.Issuer, claims.Subject, claims.Email, claims.Name, s.cfg.OIDCAllowedDomains, clientIP(r))
	if err != nil {
		if !errors.Is(err, auth.ErrNotAllowed) {
			s.logFor(r.Context()).Error("sso login failed", "error", err)
		}
		s.redirectToConsole(w, r, "denied")
		return
	}
	s.setSessionCookie(w, r, token, sess.ExpiresAt)
	s.redirectToConsole(w, r, "")
}

// redirectToConsole sends the browser back to the console, with a plain outcome word and no detail.
func (s *Server) redirectToConsole(w http.ResponseWriter, r *http.Request, outcome string) {
	dest := s.cfg.ConsoleURL
	if dest == "" {
		dest = "/"
	}
	if outcome != "" {
		if u, err := url.Parse(dest); err == nil {
			q := u.Query()
			q.Set("signin", outcome)
			u.RawQuery = q.Encode()
			dest = u.String()
		}
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// usersHandler manages accounts. Admin only.
//
//	GET   /api/users
//	POST  /api/users        {"email","name","role","password"}   (password optional: SSO-only account)
//	PATCH /api/users/{id}   {"name","role","disabled","password"} (any subset)
func (s *Server) usersHandler(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	p, _ := principalFrom(r.Context())
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, usersPrefix), "/")
	switch {
	case id == "" && r.Method == http.MethodGet:
		users, err := s.auth.Users(r.Context())
		if err != nil {
			s.authFailure(w, r, err)
			return
		}
		views := make([]userView, 0, len(users))
		for _, u := range users {
			views = append(views, viewOf(u))
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"users": views})
	case id == "" && r.Method == http.MethodPost:
		var in struct{ Email, Name, Role, Password string }
		if !s.decodeJSON(w, r, &in) {
			return
		}
		if in.Role == "" {
			in.Role = auth.RoleMember
		}
		u, err := s.auth.CreateUser(r.Context(), p.actor(), in.Email, in.Name, in.Role, in.Password)
		if err != nil {
			s.authFailure(w, r, err)
			return
		}
		util.WriteJSON(w, http.StatusCreated, viewOf(u))
	case id != "" && !strings.Contains(id, "/") && r.Method == http.MethodPatch:
		var in struct {
			Name     *string `json:"name"`
			Role     *string `json:"role"`
			Disabled *bool   `json:"disabled"`
			Password *string `json:"password"`
		}
		if !s.decodeJSON(w, r, &in) {
			return
		}
		u, err := s.auth.UpdateUser(r.Context(), p.actor(), id, auth.UserPatch{Name: in.Name, Role: in.Role, Disabled: in.Disabled, Password: in.Password})
		if err != nil {
			s.authFailure(w, r, err)
			return
		}
		util.WriteJSON(w, http.StatusOK, viewOf(u))
	default:
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
	}
}

// auditHandler lists recent security events. Admin only.
//
//	GET /api/audit?limit=100
func (s *Server) auditHandler(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "limit must be between 1 and 500"})
			return
		}
		limit = n
	}
	entries, err := s.auth.Audit(r.Context(), limit)
	if err != nil {
		s.authFailure(w, r, err)
		return
	}
	if entries == nil {
		entries = []auth.AuditEntry{}
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) authFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidInput), errors.Is(err, auth.ErrWeakPassword):
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, auth.ErrEmailTaken), errors.Is(err, auth.ErrLastAdmin):
		util.WriteJSON(w, http.StatusConflict, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, auth.ErrNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "user not found"})
	default:
		s.logFor(r.Context()).Error("account request failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	}
}

// randomBytes returns n random bytes; used for the process-local signing secret.
func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
