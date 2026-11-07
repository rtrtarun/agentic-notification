package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/config"
)

func TestHealthHandlerReturnsStructuredStatus(t *testing.T) {
	s := &Server{cfg: &config.Config{Address: ":8080"}}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	s.healthHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("expected valid json response: %v", err)
	}

	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}

	if body["service"] != "attention-orchestration" {
		t.Fatalf("expected service attention-orchestration, got %v", body["service"])
	}
}

func TestLoadConfigRejectsInvalidPort(t *testing.T) {
	t.Setenv("PORT", "abc")

	_, err := config.LoadConfig()
	if err == nil {
		t.Fatal("expected invalid port to return an error")
	}
}

func TestAuthMiddlewareRejectsMissingApiKeyWhenConfigured(t *testing.T) {
	s := &Server{cfg: &config.Config{Address: ":8080", APIKey: "secret"}}
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()

	handler := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, recorder.Code)
	}
}

func TestCorsMiddlewareOnlyAllowsListedOrigins(t *testing.T) {
	s := &Server{cfg: &config.Config{CORSAllowedOrigins: []string{"http://localhost:5173"}}}
	handler := s.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name, origin, want string
	}{
		{"allowed origin is echoed", "http://localhost:5173", "http://localhost:5173"},
		{"unlisted origin gets no header", "http://evil.example", ""},
		{"no origin gets no header", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != tt.want {
				t.Fatalf("Allow-Origin = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCorsDefaultSendsNoHeaders(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "http://anything.example")
	rr := httptest.NewRecorder()
	s.corsMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS must be closed by default")
	}
}

func TestCorsPreflightShortCircuits(t *testing.T) {
	s := &Server{cfg: &config.Config{CORSAllowedOrigins: []string{"*"}}}
	req := httptest.NewRequest(http.MethodOptions, "/notify", nil)
	req.Header.Set("Origin", "http://x.example")
	rr := httptest.NewRecorder()
	s.corsMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight must not reach the handler")
	})).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", rr.Code)
	}
}

func TestHealthReportsTheRelease(t *testing.T) {
	s := &Server{cfg: &config.Config{Address: ":8080", AppVersion: "1.2.3"}}
	rr := httptest.NewRecorder()
	s.healthHandler(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	if body := rr.Body.String(); !strings.Contains(body, `"version":"1.2.3"`) {
		t.Fatalf("health must say which release is running, got %s", body)
	}
}

func TestTheReleaseDefaultsToDevAndComesFromTheEnvironment(t *testing.T) {
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppVersion != "dev" {
		t.Fatalf("AppVersion = %q, want dev when APP_VERSION is unset", cfg.AppVersion)
	}
	t.Setenv("APP_VERSION", "0.36.0")
	cfg, err = config.LoadConfig()
	if err != nil || cfg.AppVersion != "0.36.0" {
		t.Fatalf("AppVersion = %q, %v", cfg.AppVersion, err)
	}
}
