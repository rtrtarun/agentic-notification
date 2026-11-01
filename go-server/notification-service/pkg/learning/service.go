package learning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
