package claude

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewRequiresKey(t *testing.T) {
	if _, err := New("  ", "", ""); err == nil {
		t.Fatal("an empty key must be refused")
	}
	c, err := New("k", "", "")
	if err != nil || c.Name() != "claude:"+DefaultModel {
		t.Fatalf("defaults wrong: %v %v", c, err)
	}
}

func TestCompleteSendsRequestAndJoinsText(t *testing.T) {
	var got request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "secret" || r.Header.Get("anthropic-version") != apiVersion {
			t.Errorf("headers wrong: %v", r.Header)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello "},{"type":"tool_use"},{"type":"text","text":"world"}]}`))
	}))
	defer srv.Close()
	c, _ := New("secret", "claude-test", srv.URL+"/")
	out, err := c.Complete(context.Background(), "be brief", "hi", true)
	if err != nil || out != "hello world" {
		t.Fatalf("got %q, %v", out, err)
	}
	if got.Model != "claude-test" || got.System != "be brief" || len(got.Messages) != 1 || got.Messages[0].Content != "hi" || got.MaxTokens == 0 {
		t.Fatalf("request wrong: %+v", got)
	}
}

func TestCompleteErrorsDoNotLeakBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key secret-key-echo"}`))
	}))
	defer srv.Close()
	c, _ := New("secret-key-echo", "", srv.URL)
	_, err := c.Complete(context.Background(), "s", "u", false)
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "secret-key-echo") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompleteEmptyAndMalformed(t *testing.T) {
	for name, body := range map[string]string{"empty": `{"content":[]}`, "malformed": `not json`} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			c, _ := New("k", "", srv.URL)
			if _, err := c.Complete(context.Background(), "s", "u", false); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestCompleteHonoursContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer srv.Close()
	c, _ := New("k", "", srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.Complete(ctx, "s", "u", false); err == nil {
		t.Fatal("want a timeout")
	}
}
