package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/preferences"
	"github.com/example/notification-service/pkg/stream"
)

func newPrefsServer(t *testing.T) *Server {
	t.Helper()
	s := newInboxServer(t)
	svc, err := preferences.NewService(context.Background(), preferences.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	s.preferences = svc
	s.broker = stream.NewBroker(2)
	s.shutdown = make(chan struct{})
	return s
}

func TestPreferencesGetPutAndValidation(t *testing.T) {
	s := newPrefsServer(t)
	rr := inboxCall(s, http.MethodGet, "/api/preferences", "")
	got := decode[preferences.Preferences](t, rr)
	if rr.Code != http.StatusOK || got.QuietHours.Enabled || got.MutedSources == nil {
		t.Fatalf("defaults = %d %+v", rr.Code, got)
	}

	body := `{"quietHours":{"enabled":true,"start":"22:00","end":"07:00","timezone":"Asia/Kolkata"},"mutedSources":[" GitLab "],"mutedRepositories":["acme/*"]}`
	rr = inboxCall(s, http.MethodPut, "/api/preferences", body)
	saved := decode[preferences.Preferences](t, rr)
	if rr.Code != http.StatusOK || saved.MutedSources[0] != "gitlab" || saved.UpdatedAt.IsZero() {
		t.Fatalf("put = %d %+v", rr.Code, saved)
	}
	if again := decode[preferences.Preferences](t, inboxCall(s, http.MethodGet, "/api/preferences", "")); !again.QuietHours.Enabled {
		t.Fatalf("not stored: %+v", again)
	}

	for name, bad := range map[string]string{
		"zone":    `{"quietHours":{"enabled":true,"start":"22:00","end":"07:00","timezone":"Mars/Base"}}`,
		"time":    `{"quietHours":{"enabled":true,"start":"late","end":"07:00","timezone":"UTC"}}`,
		"pattern": `{"quietHours":{"start":"22:00","end":"07:00","timezone":"UTC"},"mutedRepositories":["a/[b"]}`,
		"json":    `nope`,
	} {
		if rr := inboxCall(s, http.MethodPut, "/api/preferences", bad); rr.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, rr.Code)
		}
	}
	if rr := inboxCall(s, http.MethodPost, "/api/preferences", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rr.Code)
	}
}

func TestPreferencesRequireKeyAndService(t *testing.T) {
	s := newPrefsServer(t)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/preferences", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no key = %d", rr.Code)
	}
	s.preferences = nil
	if rr := inboxCall(s, http.MethodGet, "/api/preferences", ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no service = %d", rr.Code)
	}
}

func TestCORSAllowsPUTForRuleAndPreferenceEdits(t *testing.T) {
	s := newPrefsServer(t)
	s.cfg.CORSAllowedOrigins = []string{"http://localhost:3000"}
	req := httptest.NewRequest(http.MethodOptions, "/api/rules/x", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if !strings.Contains(rr.Header().Get("Access-Control-Allow-Methods"), "PUT") {
		t.Fatalf("preflight does not allow PUT: %q", rr.Header().Get("Access-Control-Allow-Methods"))
	}
}

// openStream connects like the console does: a GET with the API key header.
func openStream(t *testing.T, ts *httptest.Server, key string) (*http.Response, *bufio.Scanner) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/stream", nil)
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res, bufio.NewScanner(res.Body)
}

// nextData reads lines until the next "data:" line.
func nextData(t *testing.T, sc *bufio.Scanner) string {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				done <- rest
				return
			}
		}
		done <- ""
	}()
	select {
	case line := <-done:
		return line
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream data")
		return ""
	}
}

func TestStreamSendsReadyThenPublishedMessages(t *testing.T) {
	s := newPrefsServer(t)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close) // runs after the stream bodies are closed (cleanups run last-in first-out)

	res, sc := openStream(t, ts, testAPIKey) //nolint:bodyclose // openStream closes the body in a test cleanup
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d type=%q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if got := nextData(t, sc); !strings.Contains(got, `"ready"`) {
		t.Fatalf("first message = %s", got)
	}
	s.publish(stream.Message{Type: stream.TypeEvent, ID: "e9", AttentionClass: "critical"})
	if got := nextData(t, sc); !strings.Contains(got, `"e9"`) || !strings.Contains(got, `"critical"`) {
		t.Fatalf("published message = %s", got)
	}
	// An inbox change made over HTTP is announced too.
	if rr := inboxCall(s, http.MethodPost, "/api/notifications/mark-all-read", ""); rr.Code != http.StatusOK {
		t.Fatalf("mark all read = %d", rr.Code)
	}
	if got := nextData(t, sc); !strings.Contains(got, `"inbox"`) {
		t.Fatalf("inbox message = %s", got)
	}
}

func TestStreamRequiresKeyAndRespectsTheCap(t *testing.T) {
	s := newPrefsServer(t)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close) // runs after the stream bodies are closed (cleanups run last-in first-out)

	if res, _ := openStream(t, ts, ""); res.StatusCode != http.StatusUnauthorized { //nolint:bodyclose // openStream closes the body in a test cleanup
		t.Fatalf("no key = %d", res.StatusCode)
	}
	if res, _ := openStream(t, ts, "wrong"); res.StatusCode != http.StatusUnauthorized { //nolint:bodyclose // openStream closes the body in a test cleanup
		t.Fatalf("wrong key = %d", res.StatusCode)
	}
	a, _ := openStream(t, ts, testAPIKey) //nolint:bodyclose // openStream closes the body in a test cleanup
	b, _ := openStream(t, ts, testAPIKey) //nolint:bodyclose // openStream closes the body in a test cleanup
	c, _ := openStream(t, ts, testAPIKey) //nolint:bodyclose // openStream closes the body in a test cleanup
	if a.StatusCode != 200 || b.StatusCode != 200 || c.StatusCode != http.StatusServiceUnavailable || c.Header.Get("Retry-After") == "" {
		t.Fatalf("statuses = %d %d %d", a.StatusCode, b.StatusCode, c.StatusCode)
	}
}

func TestStreamEndsWhenTheServerStops(t *testing.T) {
	s := newPrefsServer(t)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)                    // runs after the stream bodies are closed (cleanups run last-in first-out)
	_, sc := openStream(t, ts, testAPIKey) //nolint:bodyclose // openStream closes the body in a test cleanup
	nextData(t, sc)                        // ready

	s.stopStreams()
	s.stopStreams() // idempotent
	finished := make(chan struct{})
	go func() {
		for sc.Scan() {
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end on shutdown")
	}
}
