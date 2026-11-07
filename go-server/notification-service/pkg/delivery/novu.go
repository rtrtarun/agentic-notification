package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/example/notification-service/pkg/notification"
)

const (
	// DefaultNovuURL is Novu's hosted API.
	DefaultNovuURL = "https://api.novu.co"
	// DefaultNovuWorkflow is the workflow trigger identifier used when none is configured.
	DefaultNovuWorkflow = "attention-orchestration"
	novuTimeout         = 10 * time.Second
	novuTriggerPath     = "/v1/events/trigger"
	maxNovuReply        = 64 * 1024
)

// NovuProvider delivers email through Novu's trigger API. It sits behind the same
// Provider interface as every other channel, so nothing else in the service knows Novu
// exists, and replacing it with another vendor means writing another Provider.
//
// Each email recipient becomes a Novu subscriber and receives one workflow trigger.
// Slack, Teams and SMS are untouched: their own providers still send them.
type NovuProvider struct {
	baseURL  string
	apiKey   string
	workflow string
	client   *http.Client
}

// NewNovuProvider returns a provider for the Novu API at baseURL (empty means Novu's cloud).
// workflow is the trigger identifier of the Novu workflow that renders the message.
func NewNovuProvider(baseURL, apiKey, workflow string) (*NovuProvider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("NOVU_API_KEY is required")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultNovuURL
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("NOVU_API_URL must be an http or https address")
	}
	if workflow == "" {
		workflow = DefaultNovuWorkflow
	}
	return &NovuProvider{
		baseURL: u.String(), apiKey: apiKey, workflow: workflow,
		// A redirect could carry the API key to another host.
		client: &http.Client{Timeout: novuTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

// Name identifies the provider.
func (p *NovuProvider) Name() string { return "novu" }

// Send triggers the workflow once per email recipient and returns every failure joined.
// A request with no email recipients is a no-op.
func (p *NovuProvider) Send(ctx context.Context, req notification.NotificationRequest) error {
	var errs []error
	for _, to := range req.EmailRecipients {
		if err := p.trigger(ctx, to, req); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *NovuProvider) trigger(ctx context.Context, recipient string, req notification.NotificationRequest) error {
	body, err := json.Marshal(map[string]any{
		"name": p.workflow,
		"to":   map[string]string{"subscriberId": recipient, "email": recipient},
		"payload": map[string]string{
			"title": req.Title, "message": req.Message, "agent": req.Agent.Name, "group": req.Agent.Group,
		},
	})
	if err != nil {
		return fmt.Errorf("encode novu trigger: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+novuTriggerPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build novu request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "ApiKey "+p.apiKey)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		// The error text of a failed request can include the URL, never the key, but keep it generic anyway.
		return errors.New("novu could not be reached")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxNovuReply))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("novu is not accepting triggers right now (status %d)", resp.StatusCode)
	default:
		// The request itself is wrong (bad key, unknown workflow, bad subscriber): retrying cannot help.
		return fmt.Errorf("%w: novu refused the trigger (status %d)", ErrPermanent, resp.StatusCode)
	}
}

// FallbackProvider tries a primary provider and, only if it fails, a fallback. It is how
// email goes through Novu with SMTP behind it: the same recipients are never sent twice.
type FallbackProvider struct {
	primary, fallback Provider
}

// NewFallbackProvider returns a provider that uses fallback when primary fails.
func NewFallbackProvider(primary, fallback Provider) *FallbackProvider {
	return &FallbackProvider{primary: primary, fallback: fallback}
}

// Name lists both providers.
func (f *FallbackProvider) Name() string { return f.primary.Name() + ">" + f.fallback.Name() }

// Send uses the primary and, if it fails, the fallback. It returns an error only when both fail.
func (f *FallbackProvider) Send(ctx context.Context, req notification.NotificationRequest) error {
	primaryErr := f.primary.Send(ctx, req)
	if primaryErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		return primaryErr
	}
	if fallbackErr := f.fallback.Send(ctx, req); fallbackErr != nil {
		return errors.Join(primaryErr, fallbackErr)
	}
	return nil
}
