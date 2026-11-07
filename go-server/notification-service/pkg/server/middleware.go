package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentpulse/notification-service/pkg/auth"
	"github.com/agentpulse/notification-service/pkg/config"
	"github.com/agentpulse/notification-service/pkg/util"
)

// statusRecorder remembers the first status code written. A handler that writes
// a body without calling WriteHeader implicitly answers 200.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

// Unwrap lets http.ResponseController reach the real writer, which streaming
// responses need to flush and to extend their write deadline.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) WriteHeader(code int) {
	if r.statusCode == 0 {
		r.statusCode = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// status returns the recorded code, or 200 if the handler wrote nothing.
func (r *statusRecorder) status() int {
	if r.statusCode == 0 {
		return http.StatusOK
	}
	return r.statusCode
}

// knownRoutes is the closed set of metric labels; anything else is "other" so
// scanners probing random paths cannot create unbounded label cardinality.
var knownRoutes = map[string]bool{
	"/notify": true, "/health": true, "/metrics": true, "/webhooks/github": true, "/feedback": true, "/events": true,
	inboxPrefix:      true,
	"/api/analytics": true, "/api/activity": true, "/api/rules": true, "/api/policies": true,
	"/api/channels": true, "/api/deliveries": true, "/api/auth": true, "/api/users": true, "/api/audit": true, "/api/overrides": true, "/api/digests": true, "/api/explain": true, "/api/attention": true, "/api/learning": true, "/api/events": true, "/api/mcp": true, "/api/traces": true, "/api/me": true, "/api/integrations": true, "/api/preferences": true, "/api/stream": true,
}

func routeLabel(path string) string {
	// Ids in the path would otherwise make one label per notification.
	if path == inboxPrefix || strings.HasPrefix(path, inboxPrefix+"/") {
		return inboxPrefix
	}
	if knownRoutes[path] {
		return path
	}
	// /api/rules/{id} and similar collapse to their collection for the same reason.
	if rest, ok := strings.CutPrefix(path, "/api/"); ok {
		collection := "/api/" + strings.SplitN(rest, "/", 2)[0]
		if knownRoutes[collection] {
			return collection
		}
	}
	return "other"
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		elapsed := time.Since(start)
		s.metrics.ObserveRequest(routeLabel(r.URL.Path), recorder.status(), elapsed)
		s.logFor(r.Context()).Info("request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"http_status", recorder.status(),
			"duration_ms", elapsed.Milliseconds())
	})
}

// authMiddleware lets a request through when it carries the API key (services)
// or a valid sign-in session (people). A key that is sent but wrong is refused
// outright. A session cookie on a state-changing request must also carry the
// session's CSRF token. With no API key configured and no session, every
// request is refused unless ALLOW_UNAUTHENTICATED_API is set (fail closed).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	service := principal{service: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("X-API-Key"); key != "" && s.cfg.APIKey != "" {
			if !validAPIKey(s.cfg.APIKey, key) {
				util.WriteJSON(w, http.StatusUnauthorized, util.ErrorResponse{Error: "missing or invalid api key"})
				return
			}
			next.ServeHTTP(w, withPrincipal(r, service))
			return
		}
		if s.auth != nil {
			if c, err := r.Cookie(sessionCookie); err == nil {
				if user, sess, err := s.auth.Authenticate(r.Context(), c.Value); err == nil {
					if unsafeMethod(r.Method) && !auth.ValidCSRF(sess, r.Header.Get(csrfHeader)) {
						util.WriteJSON(w, http.StatusForbidden, util.ErrorResponse{Error: "missing or invalid csrf token"})
						return
					}
					next.ServeHTTP(w, withPrincipal(r, principal{user: user, session: sess}))
					return
				}
			}
		}
		if s.cfg.APIKey == "" {
			if !s.cfg.AllowUnauthenticated {
				util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "api key not configured"})
				return
			}
			next.ServeHTTP(w, withPrincipal(r, service))
			return
		}
		util.WriteJSON(w, http.StatusUnauthorized, util.ErrorResponse{Error: "missing or invalid api key"})
	})
}

// validAPIKey compares in constant time. Hashing first makes both inputs the
// same length, so the comparison does not leak the length of the real key.
func validAPIKey(want, got string) bool {
	w, g := sha256.Sum256([]byte(want)), sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(w[:], g[:]) == 1
}

// corsMiddleware allows only origins listed in CORS_ALLOWED_ORIGINS. With an empty
// list no CORS headers are sent and browsers block cross-origin calls.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && originAllowed(s.cfg, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, X-Request-ID, X-CSRF-Token")
			// Cookies cross origins only for an origin named exactly: a "*" entry never gets credentials.
			if originExact(s.cfg, origin) {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originExact(cfg *config.Config, origin string) bool {
	for _, allowed := range cfg.CORSAllowedOrigins {
		if allowed != "*" && strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

func originAllowed(cfg *config.Config, origin string) bool {
	for _, allowed := range cfg.CORSAllowedOrigins {
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

// timeoutMiddleware bounds a request, and so every query and delivery it starts.
func (s *Server) timeoutMiddleware(next http.Handler) http.Handler {
	timeout := s.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = config.DefaultRequestTimeout
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// decodeJSON reads a size-limited JSON body into dst. On failure it writes the
// error response (413 when too large, 400 otherwise) and returns false.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := readJSON(w, r, s.cfg.MaxBodyBytes, dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			util.WriteJSON(w, http.StatusRequestEntityTooLarge, util.ErrorResponse{Error: errTooLarge})
			return false
		}
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: errInvalidPayload})
		return false
	}
	return true
}
