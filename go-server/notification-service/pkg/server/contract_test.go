package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/config"
)

// The shared HTTP contract (docs/contract) is replayed against this service. The Java service replays
// the same files, so the two are held to one behaviour. See docs/contract/README.md for the format.

const (
	contractKey    = "contract-key"
	contractSecret = "contract-secret"
	contractDir    = "../../../../docs/contract"
	contractRules  = "../../config/rules.yaml"
)

type contractFile struct {
	Name  string         `json:"name"`
	Steps []contractStep `json:"steps"`
}

type contractStep struct {
	Name    string `json:"name"`
	Request struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
		RawBody *string           `json:"rawBody"`
		Sign    bool              `json:"sign"`
	} `json:"request"`
	Expect struct {
		Status       int                 `json:"status"`
		JSON         json.RawMessage     `json:"json"`
		Absent       []string            `json:"absent"`
		Present      []string            `json:"present"`
		AbsentInEach map[string][]string `json:"absentInEach"`
	} `json:"expect"`
	Save map[string]string `json:"save"`
}

func TestContract(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(contractDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no contract files in %s: %v", contractDir, err)
	}
	for _, path := range files {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var file contractFile
			if err := json.Unmarshal(raw, &file); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			runContractFile(t, file)
		})
	}
}

func newContractServer(t *testing.T) http.Handler {
	t.Helper()
	t.Setenv("API_KEY", contractKey)
	t.Setenv("GITHUB_WEBHOOK_SECRET", contractSecret)
	t.Setenv("RULES_PATH", contractRules)
	t.Setenv("ATTENTION_CLASSIFY", "false")
	t.Setenv("ATTENTION_BUDGET_PER_HOUR", "0")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_URL", "")
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = s.closeAll() })
	return s.Handler()
}

func runContractFile(t *testing.T, file contractFile) {
	handler := newContractServer(t)
	vars := map[string]string{}
	for _, step := range file.Steps {
		path := step.Request.Path
		for k, v := range vars {
			path = strings.ReplaceAll(path, "{{"+k+"}}", v)
		}
		var body []byte
		switch {
		case step.Request.RawBody != nil:
			body = []byte(*step.Request.RawBody)
		case len(step.Request.Body) > 0:
			body = step.Request.Body
		}
		req := httptest.NewRequest(step.Request.Method, path, bytes.NewReader(body))
		for k, v := range step.Request.Headers {
			req.Header.Set(k, v)
		}
		if step.Request.Sign {
			mac := hmac.New(sha256.New, []byte(contractSecret))
			mac.Write(body)
			req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		answer, _ := io.ReadAll(rr.Body)
		if rr.Code != step.Expect.Status {
			t.Fatalf("%s: status %d, want %d (body %s)", step.Name, rr.Code, step.Expect.Status, answer)
		}
		var got map[string]any
		if len(step.Expect.JSON) > 0 || len(step.Expect.Absent) > 0 || len(step.Expect.Present) > 0 || len(step.Expect.AbsentInEach) > 0 || len(step.Save) > 0 {
			if err := json.Unmarshal(answer, &got); err != nil {
				t.Fatalf("%s: answer is not a JSON object: %s", step.Name, answer)
			}
		}
		if len(step.Expect.JSON) > 0 {
			var want any
			if err := json.Unmarshal(step.Expect.JSON, &want); err != nil {
				t.Fatal(err)
			}
			if msg := subsetOf(want, got, "$"); msg != "" {
				t.Fatalf("%s: %s (body %s)", step.Name, msg, answer)
			}
		}
		for _, k := range step.Expect.Absent {
			if _, ok := got[k]; ok {
				t.Fatalf("%s: %q must be absent (body %s)", step.Name, k, answer)
			}
		}
		for _, k := range step.Expect.Present {
			if _, ok := got[k]; !ok {
				t.Fatalf("%s: %q must be present (body %s)", step.Name, k, answer)
			}
		}
		for arr, keys := range step.Expect.AbsentInEach {
			items, _ := got[arr].([]any)
			for _, item := range items {
				obj, _ := item.(map[string]any)
				for _, k := range keys {
					if _, ok := obj[k]; ok {
						t.Fatalf("%s: %s element must not have %q", step.Name, arr, k)
					}
				}
			}
		}
		for name, key := range step.Save {
			vars[name] = fmt.Sprint(got[key])
		}
	}
}

// subsetOf reports how got differs from want, or "" when every written key matches. Arrays must have
// the same length and match element by element.
func subsetOf(want, got any, at string) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: want an object, got %v", at, got)
		}
		for k, wv := range w {
			gv, present := g[k]
			if !present {
				return fmt.Sprintf("%s.%s: missing", at, k)
			}
			if msg := subsetOf(wv, gv, at+"."+k); msg != "" {
				return msg
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: want %d elements, got %v", at, len(w), got)
		}
		for i := range w {
			if msg := subsetOf(w[i], g[i], fmt.Sprintf("%s[%d]", at, i)); msg != "" {
				return msg
			}
		}
	default:
		if want != got {
			return fmt.Sprintf("%s: want %v, got %v", at, want, got)
		}
	}
	return ""
}
