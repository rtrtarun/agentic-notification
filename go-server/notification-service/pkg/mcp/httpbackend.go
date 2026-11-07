package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/example/notification-service/pkg/agentevents"
)

const (
	httpTimeout     = 15 * time.Second
	maxResponseSize = 1 << 20
	apiKeyHeader    = "X-API-Key" //nolint:gosec // the name of a header, not a credential
)

// HTTPBackend runs the tools against a running AgentPulse service over its REST API,
// authenticating with an API key. It is what cmd/mcp-server uses, so a desktop client
// can only do what that key may already do.
type HTTPBackend struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewHTTPBackend returns a backend for the service at baseURL. Both values are required.
func NewHTTPBackend(baseURL, apiKey string) (*HTTPBackend, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("AGENTPULSE_URL must be an http or https address")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("AGENTPULSE_API_KEY is required")
	}
	return &HTTPBackend{baseURL: u.String(), apiKey: apiKey, client: &http.Client{Timeout: httpTimeout}}, nil
}

// PublishEvent implements Backend.
func (h *HTTPBackend) PublishEvent(ctx context.Context, in agentevents.Input) (any, error) {
	return h.do(ctx, http.MethodPost, "/api/events", in)
}

// RecentEvents implements Backend.
func (h *HTTPBackend) RecentEvents(ctx context.Context, traceID string, limit int) (any, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if traceID != "" {
		q.Set("traceId", traceID)
	}
	return h.do(ctx, http.MethodGet, "/api/events?"+q.Encode(), nil)
}

// Notify implements Backend. It maps onto POST /notify, which validates recipients and hosts.
func (h *HTTPBackend) Notify(ctx context.Context, n Notification) (any, error) {
	body := map[string]any{
		"agent": map[string]string{"name": firstNonEmpty(n.Agent, "mcp")}, "title": n.Title, "message": n.Message,
		"emailRecipients": n.EmailRecipients, "slackWebhookUrls": n.SlackWebhookURLs, "teamsWebhookUrls": n.TeamsWebhookURLs,
	}
	return h.do(ctx, http.MethodPost, "/notify", body)
}

// Explain implements Backend.
func (h *HTTPBackend) Explain(ctx context.Context, eventID string) (any, error) {
	return h.do(ctx, http.MethodGet, "/api/explain/"+url.PathEscape(eventID), nil)
}

func (h *HTTPBackend) do(ctx context.Context, method, path string, body any) (any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set(apiKeyHeader, h.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, errors.New("AgentPulse could not be reached")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, errors.New("the response from AgentPulse could not be read")
	}
	if resp.StatusCode >= 400 {
		return nil, statusError(resp.StatusCode, raw)
	}
	var out any
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]string{"status": "ok"}, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("AgentPulse answered with something that is not JSON")
	}
	return out, nil
}

// statusError turns a failed response into a message for the client. A refusal (4xx) says
// why; a server fault or a rejected key says only that, with no detail that could leak.
func statusError(status int, raw []byte) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	switch {
	case status == http.StatusUnauthorized:
		return errors.New("the API key was rejected")
	case status == http.StatusForbidden:
		return errors.New("this API key is not allowed to do that")
	case status >= 500:
		return fmt.Errorf("AgentPulse had a problem (status %d)", status)
	case body.Error != "":
		return fmt.Errorf("%w: %s", ErrInvalid, body.Error)
	}
	return fmt.Errorf("%w: status %d", ErrInvalid, status)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
