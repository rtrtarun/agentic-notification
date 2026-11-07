package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/example/notification-service/pkg/config"
)

// fakeOllama answers like a model would and records whether it ever saw a critical event.
type fakeOllama struct {
	*httptest.Server
	calls         atomic.Int32
	sawSecurity   atomic.Bool
	classifyReply string
}

func newFakeOllama(t *testing.T) *fakeOllama {
	t.Helper()
	f := &fakeOllama{classifyReply: `{"class":"background","score":10,"confidence":0.9,"reasons":["a bot did it"]}`}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "security_advisory") {
			f.sawSecurity.Store(true)
		}
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.Unmarshal(raw, &req)
		system := ""
		if len(req.Messages) > 0 {
			system = req.Messages[0].Content
		}
		reply := "I held this back for your digest because your rule said it could wait."
		switch {
		case strings.Contains(system, "how much human attention"):
			reply = f.classifyReply
		case strings.Contains(system, "digest of software notifications"):
			reply = `{"title":"A calm stretch","text":"A few quiet updates were held back for you.","groups":[]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": reply}})
	}))
	t.Cleanup(f.Close)
	return f
}

func withOllama(f *fakeOllama) func(*config.Config) {
	return func(c *config.Config) {
		c.AttentionProvider = config.ProviderOllama
		c.OllamaURL = f.URL
		c.OllamaModel = "fake"
	}
}

const botLabel = `{"action":"labeled","repository":{"full_name":"acme/web"},"pull_request":{"number":%d,"title":"Bump deps"},"sender":{"login":"renovate[bot]","type":"Bot"}}`

func botEvent(s *Server, id string) *httptest.ResponseRecorder {
	return webhook(s, "pull_request", id, strings.Replace(botLabel, "%d", "4", 1))
}

func TestStatusReportsHeuristicByDefaultAndIsAdminOnly(t *testing.T) {
	s := newAuthServer(t, nil)
	rr := call(s, http.MethodGet, "/api/attention/status", "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"provider":"heuristic"`) || !strings.Contains(rr.Body.String(), `"breaker":"closed"`) {
		t.Fatalf("status = %d %s", rr.Code, rr.Body.String())
	}
	m := addMember(t, s, "member@corp.com")
	if rr := call(s, http.MethodGet, "/api/attention/status", "", m.apply(false)); rr.Code != http.StatusForbidden {
		t.Fatalf("a member = %d, want 403", rr.Code)
	}
	for _, c := range []struct {
		method, path string
		edit         func(*http.Request)
		want         int
	}{
		{http.MethodGet, "/api/attention/status", nil, http.StatusUnauthorized},
		{http.MethodPost, "/api/attention/status", withKey, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/attention/other", withKey, http.StatusNotFound},
	} {
		if rr := call(s, c.method, c.path, "", c.edit); rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
}

func TestModelClassifiesAndReportsItself(t *testing.T) {
	f := newFakeOllama(t)
	s := newAuthServer(t, withOllama(f))
	rr := botEvent(s, "bot-1")
	body := rr.Body.String()
	if !strings.Contains(body, `"attentionClass":"background"`) || !strings.Contains(body, "ollama:fake") {
		t.Fatalf("the model's decision should be recorded: %s", body)
	}
	st := call(s, http.MethodGet, "/api/attention/status", "", withKey).Body.String()
	if !strings.Contains(st, `"provider":"ollama"`) || !strings.Contains(st, `"model":"ollama:fake"`) || strings.Contains(st, f.URL) {
		t.Fatalf("status = %s", st)
	}
}

func TestCriticalEventsNeverReachTheModel(t *testing.T) {
	f := newFakeOllama(t)
	s := newAuthServer(t, withOllama(f))
	webhook(s, "security_advisory", "sec-1", securityPayload)
	id := eventID(t, webhook(s, "security_advisory", "sec-2", securityPayload))
	rr := call(s, http.MethodGet, "/api/explain/"+id, "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"source":"heuristic"`) {
		t.Fatalf("explain critical = %d %s", rr.Code, rr.Body.String())
	}
	if f.sawSecurity.Load() || f.calls.Load() != 0 {
		t.Fatalf("a critical event reached the model (%d calls)", f.calls.Load())
	}
}

func TestModelCannotEscalateAnEvent(t *testing.T) {
	f := newFakeOllama(t)
	f.classifyReply = `{"class":"critical","score":100,"confidence":1,"reasons":["panic"]}`
	s := newAuthServer(t, withOllama(f))
	rr := botEvent(s, "bot-1")
	body := rr.Body.String()
	if strings.Contains(body, `"attentionClass":"critical"`) {
		t.Fatalf("a model must never escalate to critical: %s", body)
	}
	if !strings.Contains(body, `"attentionClass":"background"`) || strings.Contains(body, "ollama:fake") {
		t.Fatalf("the heuristic should have decided instead: %s", body)
	}
}

func TestDigestAndExplanationSaySheModelWroteThem(t *testing.T) {
	f := newFakeOllama(t)
	s := newAuthServer(t, withOllama(f))
	botEvent(s, "bot-1")

	rr := call(s, http.MethodPost, "/api/digests/generate", "", withKey)
	if rr.Code != http.StatusCreated {
		t.Fatalf("generate = %d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"source":"model"`) || !strings.Contains(body, "A calm stretch") || !strings.Contains(body, "ollama:fake") {
		t.Fatalf("digest should be model-written and say so: %s", body)
	}

	held := call(s, http.MethodGet, "/api/explain/github-pull_request-bot-1", "", withKey)
	if held.Code != http.StatusOK || !strings.Contains(held.Body.String(), `"source":"model"`) || !strings.Contains(held.Body.String(), "your digest") {
		t.Fatalf("explain = %d %s", held.Code, held.Body.String())
	}
}

func TestEverythingKeepsWorkingWhenTheModelIsDown(t *testing.T) {
	f := newFakeOllama(t)
	s := newAuthServer(t, withOllama(f))
	f.Close() // the model server disappears

	rr := botEvent(s, "bot-1")
	if rr.Code >= 300 || !strings.Contains(rr.Body.String(), `"attentionClass":"background"`) || strings.Contains(rr.Body.String(), "ollama:fake") {
		t.Fatalf("the heuristic should have handled it: %d %s", rr.Code, rr.Body.String())
	}
	gen := call(s, http.MethodPost, "/api/digests/generate", "", withKey)
	if gen.Code != http.StatusCreated || !strings.Contains(gen.Body.String(), `"source":"heuristic"`) {
		t.Fatalf("digest = %d %s", gen.Code, gen.Body.String())
	}
	st := call(s, http.MethodGet, "/api/attention/status", "", withKey).Body.String()
	if !strings.Contains(st, `"fallbacks":`) || strings.Contains(st, `"fallbacks":0`) || !strings.Contains(st, "lastError") {
		t.Fatalf("status should show the failures: %s", st)
	}
}

func TestClaudeWithoutAKeyStartsWithTheHeuristic(t *testing.T) {
	s := newAuthServer(t, func(c *config.Config) { c.AttentionProvider = config.ProviderClaude })
	if rr := call(s, http.MethodGet, "/api/attention/status", "", withKey); !strings.Contains(rr.Body.String(), `"provider":"heuristic"`) {
		t.Fatalf("status = %s", rr.Body.String())
	}
	if rr := botEvent(s, "bot-1"); rr.Code >= 300 {
		t.Fatalf("event = %d", rr.Code)
	}
}
