// Package claude is a minimal client for the Anthropic Messages API. It does
// one thing, send a prompt and return the text, so Attention Intelligence can
// use Claude without pulling in an SDK.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	// DefaultBaseURL is the public API.
	DefaultBaseURL = "https://api.anthropic.com"
	// DefaultModel is a small, fast model; classification and digests do not need a large one.
	DefaultModel = "claude-haiku-5-5"
	apiVersion   = "2023-06-01"
	maxTokens    = 600
)

// Client calls the Messages API.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// New returns a client. The API key is required; an empty base URL or model uses the default.
func New(apiKey, model, baseURL string) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("claude: ANTHROPIC_API_KEY is required")
	}
	if model == "" {
		model = DefaultModel
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	// The caller's context sets the deadline, so the client has none of its own.
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model, http: &http.Client{}}, nil
}

// Name identifies the model in decisions and digests.
func (c *Client) Name() string { return "claude:" + c.model }

type request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type response struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// Complete sends one user message. wantJSON is accepted for interface parity;
// the prompt itself asks for JSON, and the caller validates what comes back.
func (c *Client) Complete(ctx context.Context, system, user string, _ bool) (string, error) {
	body, err := json.Marshal(request{Model: c.model, MaxTokens: maxTokens, System: system, Messages: []message{{Role: "user", Content: user}}})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build claude request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("call claude: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body can echo request details, so only the status is reported.
		return "", fmt.Errorf("claude request failed with status %d", resp.StatusCode)
	}
	var out response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("read claude response: %w", err)
	}
	var text strings.Builder
	for _, part := range out.Content {
		if part.Type == "text" {
			text.WriteString(part.Text)
		}
	}
	if text.Len() == 0 {
		return "", errors.New("claude returned no text")
	}
	return text.String(), nil
}
