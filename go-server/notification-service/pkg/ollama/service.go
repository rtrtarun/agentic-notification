package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const requestTimeout = 10 * time.Second

// Service summarises feedback with a local Ollama model.
type Service struct {
	baseURL string
	client  *http.Client
}

func NewService(baseURL string) *Service {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &Service{baseURL: baseURL, client: &http.Client{Timeout: requestTimeout}}
}

type OllamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type OllamaResponse struct {
	Response string `json:"response"`
}

func (s *Service) Summarize(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("text required")
	}

	payload := OllamaRequest{
		Model:  "llama3.2",
		Prompt: fmt.Sprintf("Summarize this feedback into one short recommendation:\n\n%s", text),
		Stream: false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama request failed with status %d", resp.StatusCode)
	}

	var out OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Response, nil
}

// Completer sends one prompt to a chosen Ollama model and returns its text.
// It is the adapter Attention Intelligence uses; Service above is the older
// feedback summariser and is left as it was.
type Completer struct {
	baseURL string
	model   string
	client  *http.Client
}

// DefaultModel is used when OLLAMA_MODEL is not set.
const DefaultModel = "llama3.2"

// NewCompleter returns a Completer for a server and model. Empty values fall back to the defaults.
func NewCompleter(baseURL, model string) *Completer {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = DefaultModel
	}
	// The caller's context sets the deadline, so the client has none of its own.
	return &Completer{baseURL: strings.TrimRight(baseURL, "/"), model: model, client: &http.Client{}}
}

// Name identifies the model in decisions and digests.
func (c *Completer) Name() string { return "ollama:" + c.model }

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Format   string        `json:"format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Message chatMessage `json:"message"`
}

// Complete asks the model; when wantJSON is set Ollama is told to answer in JSON.
func (c *Completer) Complete(ctx context.Context, system, user string, wantJSON bool) (string, error) {
	req := chatRequest{
		Model:    c.model,
		Messages: []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
	}
	if wantJSON {
		req.Format = "json"
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call ollama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama request failed with status %d", resp.StatusCode)
	}
	var out chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("read ollama response: %w", err)
	}
	return out.Message.Content, nil
}
