package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/agentevents"
)

type seen struct {
	method, path, query, key, body string
}

func service(t *testing.T, status int, reply string) (*HTTPBackend, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*got = seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-API-Key"), string(raw)}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	b, err := NewHTTPBackend(srv.URL+"/", "secret-key")
	if err != nil {
		t.Fatal(err)
	}
	return b, got
}

func TestNewHTTPBackendValidatesItsSettings(t *testing.T) {
	for name, c := range map[string][2]string{
		"empty url": {"", "k"},
		"no scheme": {"example.com", "k"},
		"ftp":       {"ftp://example.com", "k"},
		"no key":    {"https://example.com", ""},
		"blank key": {"https://example.com", "  "},
		"no host":   {"https://", "k"},
		"not a url": {"http://[::1", "k"},
	} {
		if _, err := NewHTTPBackend(c[0], c[1]); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestTheHTTPBackendCallsTheRestAPIWithTheKey(t *testing.T) {
	ctx := context.Background()
	b, got := service(t, 202, `{"status":"accepted","eventId":"e1"}`)
	if _, err := b.PublishEvent(ctx, agentevents.Input{EventType: "agent.plan", TraceID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/api/events" || got.key != "secret-key" || !strings.Contains(got.body, `"traceId":"run-1"`) {
		t.Fatalf("publish = %+v", got)
	}

	b, got = service(t, 200, `{"events":[]}`)
	if _, err := b.RecentEvents(ctx, "run 1", 5); err != nil {
		t.Fatal(err)
	}
	if got.method != "GET" || got.path != "/api/events" || got.query != "limit=5&traceId=run+1" {
		t.Fatalf("recent = %+v", got)
	}
	if _, err := b.RecentEvents(ctx, "", 3); err != nil || got.query != "limit=3" {
		t.Fatalf("no trace: %+v %v", got, err)
	}

	b, got = service(t, 202, `{"status":"accepted"}`)
	if _, err := b.Notify(ctx, Notification{Title: "t", Message: "m", EmailRecipients: []string{"a@corp.com"}}); err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(got.body), &sent)
	if got.path != "/notify" || sent["title"] != "t" || sent["agent"].(map[string]any)["name"] != "mcp" {
		t.Fatalf("notify = %+v", got)
	}

	b, got = service(t, 200, `{"explanation":"because"}`)
	if out, err := b.Explain(ctx, "a/b c"); err != nil || got.path != "/api/explain/a/b c" && got.path != "/api/explain/a%2Fb%20c" {
		t.Fatalf("explain = %+v %v %v", got, out, err)
	}
}

func TestTheHTTPBackendTurnsFailuresIntoSafeMessages(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		status  int
		body    string
		want    string
		invalid bool
	}{
		"bad input":    {400, `{"error":"eventType is required"}`, "eventType is required", true},
		"bad key":      {401, `{"error":"the secret-key was wrong"}`, "API key was rejected", false},
		"forbidden":    {403, `{}`, "not allowed", false},
		"server fault": {500, `{"error":"pq: password authentication failed for user admin"}`, "had a problem", false},
		"unknown 4xx":  {404, `not json`, "status 404", true},
	}
	for name, c := range cases {
		b, _ := service(t, c.status, c.body)
		_, err := b.Explain(ctx, "e1")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if errors.Is(err, ErrInvalid) != c.invalid {
			t.Errorf("%s: ErrInvalid = %v, want %v", name, errors.Is(err, ErrInvalid), c.invalid)
		}
		if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret-key") {
			t.Errorf("%s: a server detail leaked: %v", name, err)
		}
	}
}

func TestTheHTTPBackendHandlesOddReplies(t *testing.T) {
	ctx := context.Background()
	b, _ := service(t, 202, ``)
	if out, err := b.Explain(ctx, "e"); err != nil || out.(map[string]string)["status"] != "ok" {
		t.Fatalf("an empty success: %v %v", out, err)
	}
	b, _ = service(t, 200, `<html>`)
	if _, err := b.Explain(ctx, "e"); err == nil {
		t.Fatal("a non-JSON success must be an error")
	}
	down, _ := NewHTTPBackend("http://127.0.0.1:1", "k")
	if _, err := down.Explain(ctx, "e"); err == nil || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("unreachable = %v", err)
	}
}
