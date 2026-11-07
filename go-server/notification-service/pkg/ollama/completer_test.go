package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompleterSendsChatRequest(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"ok\":true}"}}`))
	}))
	defer srv.Close()
	c := NewCompleter(srv.URL+"/", "tiny")
	out, err := c.Complete(context.Background(), "sys", "usr", true)
	if err != nil || out != `{"ok":true}` {
		t.Fatalf("got %q, %v", out, err)
	}
	if got.Model != "tiny" || got.Stream || got.Format != "json" || len(got.Messages) != 2 || got.Messages[0].Role != "system" {
		t.Fatalf("request wrong: %+v", got)
	}
	if c.Name() != "ollama:tiny" {
		t.Fatalf("name %q", c.Name())
	}
}

func TestCompleterPlainTextOmitsFormat(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"message":{"content":"hi"}}`))
	}))
	defer srv.Close()
	if _, err := NewCompleter(srv.URL, "").Complete(context.Background(), "s", "u", false); err != nil {
		t.Fatal(err)
	}
	if got.Format != "" || got.Model != DefaultModel {
		t.Fatalf("request wrong: %+v", got)
	}
}

func TestCompleterErrors(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer bad.Close()
	if _, err := NewCompleter(bad.URL, "m").Complete(context.Background(), "s", "u", false); err == nil {
		t.Fatal("want a status error")
	}
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("nope")) }))
	defer junk.Close()
	if _, err := NewCompleter(junk.URL, "m").Complete(context.Background(), "s", "u", false); err == nil {
		t.Fatal("want a decode error")
	}
	if _, err := NewCompleter("http://127.0.0.1:1", "m").Complete(context.Background(), "s", "u", false); err == nil {
		t.Fatal("want a connection error")
	}
}
