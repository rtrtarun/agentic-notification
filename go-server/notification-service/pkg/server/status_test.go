package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/config"
	"github.com/agentpulse/notification-service/pkg/metrics"
)

// Logs and metrics must report the real status, not a hardcoded 200.
func TestLoggingMiddlewareRecordsActualStatus(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    int
		class   string
	}{
		{"explicit 413", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusRequestEntityTooLarge) }, 413, `code="4xx"`},
		{"explicit 500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, 500, `code="5xx"`},
		{"implicit 200 via Write", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }, 200, `code="2xx"`},
		{"nothing written", func(http.ResponseWriter, *http.Request) {}, 200, `code="2xx"`},
		{"first WriteHeader wins", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			w.WriteHeader(http.StatusTeapot)
		}, 202, `code="2xx"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			s := &Server{
				cfg:     &config.Config{},
				logger:  slog.New(slog.NewJSONHandler(&logs, nil)),
				metrics: metrics.NewRegistry(),
			}
			s.loggingMiddleware(tt.handler).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notify", nil))

			if want := `"http_status":` + strconv.Itoa(tt.want); !strings.Contains(logs.String(), want) {
				t.Fatalf("log missing %s: %s", want, logs.String())
			}
			var out strings.Builder
			s.metrics.WritePrometheus(&out)
			if !strings.Contains(out.String(), tt.class) {
				t.Fatalf("metrics missing %s:\n%s", tt.class, out.String())
			}
		})
	}
}
