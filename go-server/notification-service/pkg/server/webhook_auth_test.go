package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/config"
)

const testSecret = "s3cret"

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// serve runs body through the middleware and returns the status plus what the
// downstream handler saw (nil if it was never called).
func serve(cfg *config.Config, body []byte, header string) (int, []byte) {
	var seen []byte
	s := &Server{cfg: cfg}
	h := s.webhookAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
	if header != "" {
		req.Header.Set(signatureHeader, header)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, seen
}

func signedConfig() *config.Config {
	return &config.Config{GitHubWebhookSecret: testSecret, MaxBodyBytes: 1024}
}

func TestWebhookAuthAcceptsValidSignatureAndRestoresBody(t *testing.T) {
	body := []byte(`{ "b": 1,  "a": 2 }`) // odd spacing and key order must survive
	code, seen := serve(signedConfig(), body, sign(testSecret, body))
	if code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", code)
	}
	if string(seen) != string(body) {
		t.Fatalf("handler saw %q, want original %q", seen, body)
	}
}

func TestWebhookAuthRejectsBadSignatures(t *testing.T) {
	body := []byte(`{"a":1}`)
	cases := map[string]string{
		"missing":      "",
		"wrong secret": sign("other", body),
		"no prefix":    strings.TrimPrefix(sign(testSecret, body), signaturePrefix),
		"bad hex":      signaturePrefix + "zzzz",
		"other body":   sign(testSecret, []byte(`{"a":2}`)),
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			code, seen := serve(signedConfig(), body, header)
			if code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", code)
			}
			if seen != nil {
				t.Fatal("handler must not be called")
			}
		})
	}
}

func TestWebhookAuthRejectsOversizedBody(t *testing.T) {
	body := []byte(strings.Repeat("a", 2048))
	code, seen := serve(signedConfig(), body, sign(testSecret, body))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", code)
	}
	if seen != nil {
		t.Fatal("handler must not be called")
	}
}

func TestWebhookAuthFailsClosedWithoutSecret(t *testing.T) {
	code, seen := serve(&config.Config{MaxBodyBytes: 1024}, []byte(`{}`), "")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", code)
	}
	if seen != nil {
		t.Fatal("handler must not be called")
	}
}

func TestWebhookAuthAllowsUnsignedWhenExplicitlyEnabled(t *testing.T) {
	cfg := &config.Config{AllowUnsignedWebhooks: true, MaxBodyBytes: 1024}
	if code, _ := serve(cfg, []byte(`{}`), ""); code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", code)
	}
}
