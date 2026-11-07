package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/attention/llm"
	"github.com/agentpulse/notification-service/pkg/claude"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/ollama"
)

// These tests send real traffic through the real Ollama and Claude clients to a
// local server that replays recorded response shapes. They check, on the wire,
// what the model is sent and how the circuit breaker and rate cap behave.

const classification = `{"class":"background","score":18,"confidence":0.85,"reasons":["a bot did it"]}`

// provider is one backend under test: a fake server and the Completer that talks to it.
type provider struct {
	name      string
	newServer func(t *testing.T, h *recorder) *httptest.Server
	completer func(url string) (llm.Completer, error)
}

// recorder is the fake model server's behaviour and memory.
type recorder struct {
	mu     sync.Mutex
	status int // 0 means 200
	reply  string
	bodies []string
	hits   atomic.Int32
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

func (r *recorder) setStatus(code int) {
	r.mu.Lock()
	r.status = code
	r.mu.Unlock()
}

func (r *recorder) handle(w http.ResponseWriter, req *http.Request, wrap func(string) any) {
	raw, _ := io.ReadAll(req.Body)
	r.hits.Add(1)
	r.mu.Lock()
	r.bodies = append(r.bodies, string(raw))
	status, reply := r.status, r.reply
	r.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	_ = json.NewEncoder(w).Encode(wrap(reply))
}

var providers = []provider{
	{
		name: "ollama",
		newServer: func(t *testing.T, r *recorder) *httptest.Server {
			return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/api/chat" {
					t.Errorf("ollama path %s", req.URL.Path)
				}
				r.handle(w, req, func(reply string) any {
					return map[string]any{"message": map[string]string{"role": "assistant", "content": reply}}
				})
			}))
		},
		completer: func(url string) (llm.Completer, error) { return ollama.NewCompleter(url, "replay"), nil },
	},
	{
		name: "claude",
		newServer: func(t *testing.T, r *recorder) *httptest.Server {
			return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/v1/messages" || req.Header.Get("x-api-key") != "replay-key" {
					t.Errorf("claude request wrong: %s %v", req.URL.Path, req.Header)
				}
				r.handle(w, req, func(reply string) any {
					return map[string]any{"content": []map[string]string{{"type": "text", "text": reply}}}
				})
			}))
		},
		completer: func(url string) (llm.Completer, error) { return claude.New("replay-key", "replay", url) },
	},
}

type fixture struct {
	rec   *recorder
	model *llm.Model
	now   *time.Time
}

func setup(t *testing.T, p provider, opts llm.Options) fixture {
	t.Helper()
	rec := &recorder{reply: classification}
	srv := p.newServer(t, rec)
	t.Cleanup(srv.Close)
	c, err := p.completer(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	opts.Now = func() time.Time { return now }
	opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	return fixture{rec: rec, model: llm.New(c, attention.NewHeuristic(), opts), now: &now}
}

func sensitiveEvent(id string) event.Event {
	return event.Event{
		ID: id, Source: "github", EventType: "pull_request", Repository: "acme/api", AttentionClass: event.AttentionStandard,
		Payload: map[string]any{
			"action": "labeled",
			"sender": map[string]any{"login": "renovate[bot]", "type": "Bot"},
			"pull_request": map[string]any{
				"title": "Bump deps; owner carol@example.com; token ghp_abcdefghijklmnopqrstuvwxyz0123456789",
				"body":  "INTERNAL DESCRIPTION do not leak",
			},
			"comment":    map[string]any{"body": "CONFIDENTIAL COMMENT"},
			"repository": map[string]any{"private": true, "clone_url": "https://x.test/r?token=abc123secret"},
		},
	}
}

func eachProvider(t *testing.T, run func(t *testing.T, p provider)) {
	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) { run(t, p) })
	}
}

func TestReplayClassifiesThroughTheRealClient(t *testing.T) {
	eachProvider(t, func(t *testing.T, p provider) {
		f := setup(t, p, llm.Options{})
		got, err := f.model.Classify(context.Background(), sensitiveEvent("e1"))
		if err != nil {
			t.Fatal(err)
		}
		if got.Class != event.AttentionBackground || got.Score != 18 || !strings.HasPrefix(got.Model, p.name+":") {
			t.Fatalf("unexpected: %+v", got)
		}
	})
}

func TestReplayWhatTheModelIsSentIsRedacted(t *testing.T) {
	eachProvider(t, func(t *testing.T, p provider) {
		f := setup(t, p, llm.Options{})
		if _, err := f.model.Classify(context.Background(), sensitiveEvent("e1")); err != nil {
			t.Fatal(err)
		}
		bodies := f.rec.seen()
		if len(bodies) != 1 {
			t.Fatalf("want one request, got %d", len(bodies))
		}
		wire := bodies[0]
		for _, leaked := range []string{"INTERNAL DESCRIPTION", "CONFIDENTIAL COMMENT", "carol@example.com", "ghp_abcdefghijklmnopqrstuvwxyz", "abc123secret", "clone_url", "private"} {
			if strings.Contains(wire, leaked) {
				t.Fatalf("%s: %q reached the model:\n%s", p.name, leaked, wire)
			}
		}
		for _, kept := range []string{"acme/api", "pull_request", "labeled", "renovate", "[email]", "[redacted]"} {
			if !strings.Contains(wire, kept) {
				t.Fatalf("%s: %q is missing from what the model should see:\n%s", p.name, kept, wire)
			}
		}
	})
}

func TestReplayCriticalEventsMakeNoRequest(t *testing.T) {
	eachProvider(t, func(t *testing.T, p provider) {
		f := setup(t, p, llm.Options{})
		crit := sensitiveEvent("c1")
		crit.AttentionClass = event.AttentionCritical
		crit.DecisionState = &event.DecisionState{Status: event.DecisionSent}
		if _, err := f.model.Classify(context.Background(), crit); err == nil {
			t.Fatal("classifying a critical event must fail")
		}
		if _, _, err := f.model.ExplainSourced(context.Background(), crit); err != nil {
			t.Fatal(err)
		}
		if _, err := f.model.Summarize(context.Background(), []event.Event{crit}); err != nil {
			t.Fatal(err)
		}
		if f.rec.hits.Load() != 0 {
			t.Fatalf("%s: a critical event produced %d model requests", p.name, f.rec.hits.Load())
		}
	})
}

func TestReplayBreakerTripsStopsCallingAndRecovers(t *testing.T) {
	eachProvider(t, func(t *testing.T, p provider) {
		f := setup(t, p, llm.Options{FailureThreshold: 3, Cooldown: time.Minute})
		f.rec.setStatus(http.StatusInternalServerError)
		for i := 0; i < 3; i++ {
			got, err := f.model.Classify(context.Background(), sensitiveEvent("e"))
			if err != nil || got.Model != attention.ModelHeuristic {
				t.Fatalf("a failing model must fall back: %v %+v", err, got)
			}
		}
		if st := f.model.Status(); st.Breaker != "open" || st.Fallbacks != 3 || st.LastError == "" {
			t.Fatalf("breaker should be open: %+v", st)
		}
		hits := f.rec.hits.Load()
		for i := 0; i < 5; i++ {
			got, _ := f.model.Classify(context.Background(), sensitiveEvent("e"))
			if got.Model != attention.ModelHeuristic {
				t.Fatal("an open breaker must fall back")
			}
		}
		if f.rec.hits.Load() != hits {
			t.Fatalf("%s: the model was called %d more times while the breaker was open", p.name, f.rec.hits.Load()-hits)
		}

		*f.now = f.now.Add(2 * time.Minute)
		f.rec.setStatus(0)
		got, _ := f.model.Classify(context.Background(), sensitiveEvent("e"))
		if !strings.HasPrefix(got.Model, p.name+":") || f.model.Status().Breaker != "closed" {
			t.Fatalf("a healthy probe should close the breaker: %+v %+v", got, f.model.Status())
		}
	})
}

func TestReplayRateCapFallsBackWithoutCallingTheModel(t *testing.T) {
	eachProvider(t, func(t *testing.T, p provider) {
		f := setup(t, p, llm.Options{MaxCallsPerMinute: 2})
		var models []string
		for i := 0; i < 4; i++ {
			got, _ := f.model.Classify(context.Background(), sensitiveEvent("e"))
			models = append(models, got.Model)
		}
		if f.rec.hits.Load() != 2 {
			t.Fatalf("%s: the cap of 2 allowed %d requests", p.name, f.rec.hits.Load())
		}
		if !strings.HasPrefix(models[0], p.name+":") || models[2] != attention.ModelHeuristic || models[3] != attention.ModelHeuristic {
			t.Fatalf("calls over the cap must use the heuristic: %v", models)
		}
	})
}

func TestReplayMalformedAndHostileAnswersNeverReachTheEvent(t *testing.T) {
	answers := map[string]string{
		"escalation":  `{"class":"critical","score":100,"confidence":1,"reasons":["page everyone"]}`,
		"prose":       `This looks important, you should wake someone up.`,
		"wrong shape": `{"label":"urgent"}`,
	}
	eachProvider(t, func(t *testing.T, p provider) {
		for name, answer := range answers {
			f := setup(t, p, llm.Options{})
			f.rec.reply = answer
			got, err := f.model.Classify(context.Background(), sensitiveEvent("e"))
			if err != nil || got.Class == event.AttentionCritical || got.Model != attention.ModelHeuristic {
				t.Fatalf("%s/%s: the heuristic must decide: %v %+v", p.name, name, err, got)
			}
		}
	})
}
