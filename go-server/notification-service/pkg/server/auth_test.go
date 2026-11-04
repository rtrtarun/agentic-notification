package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/notification-service/pkg/config"
)

func authStatus(cfg *config.Config, apiKeyHeader string) (int, bool) {
	s := &Server{cfg: cfg}
	reached := false
	h := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/notify", nil)
	if apiKeyHeader != "" {
		req.Header.Set("X-API-Key", apiKeyHeader)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, reached
}

func TestAuthMiddleware(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		header      string
		wantStatus  int
		wantReached bool
	}{
		{"correct key", config.Config{APIKey: "s3cret"}, "s3cret", http.StatusNoContent, true},
		{"wrong key", config.Config{APIKey: "s3cret"}, "nope", http.StatusUnauthorized, false},
		{"missing key", config.Config{APIKey: "s3cret"}, "", http.StatusUnauthorized, false},
		{"prefix of key", config.Config{APIKey: "s3cret"}, "s3cre", http.StatusUnauthorized, false},
		{"no key configured fails closed", config.Config{}, "", http.StatusServiceUnavailable, false},
		{"no key configured ignores any header", config.Config{}, "guess", http.StatusServiceUnavailable, false},
		{"explicit opt-out allows unauthenticated", config.Config{AllowUnauthenticated: true}, "", http.StatusNoContent, true},
		{"opt-out does not weaken a configured key", config.Config{APIKey: "s3cret", AllowUnauthenticated: true}, "bad", http.StatusUnauthorized, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			status, reached := authStatus(&cfg, tt.header)
			if status != tt.wantStatus || reached != tt.wantReached {
				t.Fatalf("status=%d reached=%v, want status=%d reached=%v", status, reached, tt.wantStatus, tt.wantReached)
			}
		})
	}
}

func TestHealthIsPublicButOtherRoutesAreProtected(t *testing.T) {
	s := &Server{cfg: &config.Config{APIKey: "s3cret", MaxBodyBytes: 1024}}
	h := s.Handler()

	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/health", http.StatusOK},
		{http.MethodGet, "/metrics", http.StatusUnauthorized},
		{http.MethodPost, "/notify", http.StatusUnauthorized},
		{http.MethodPost, "/feedback", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(tt.method, tt.path, nil))
			if rr.Code != tt.want {
				t.Fatalf("status = %d, want %d", rr.Code, tt.want)
			}
		})
	}
}

func TestMetricsEndpointServesPrometheusText(t *testing.T) {
	s := &Server{cfg: &config.Config{APIKey: "k"}}
	h := s.Handler()

	// Generate one observation, then scrape with the key.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("X-API-Key", "k")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct == "" || ct[:10] != "text/plain" {
		t.Fatalf("content type = %q", ct)
	}
}

func TestValidAPIKeyIgnoresLength(t *testing.T) {
	if validAPIKey("short", "a-much-longer-guess-than-the-key") {
		t.Fatal("different keys must not match")
	}
	if !validAPIKey("same", "same") {
		t.Fatal("identical keys must match")
	}
}

func TestRouteLabelBoundsCardinality(t *testing.T) {
	if routeLabel("/notify") != "/notify" {
		t.Fatal("known route must keep its label")
	}
	if routeLabel("/random/scanner/path") != "other" {
		t.Fatal("unknown path must collapse to other")
	}
}
