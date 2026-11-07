package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBodyTemplate is used when none is configured.
const DefaultBodyTemplate = `{"to":"{{to}}","text":"{{text}}"}`

const httpTimeout = 10 * time.Second

// HTTPGateway posts a JSON body to any SMS API. The URL, headers and body
// template come from configuration, so most gateways need no code.
type HTTPGateway struct {
	url      string
	headers  map[string]string
	template string
	client   *http.Client
}

// NewHTTP builds a generic HTTP gateway. headers carry credentials, for
// example Authorization. The template may contain {{to}} and {{text}}, which
// are JSON-escaped when substituted.
func NewHTTP(rawURL string, headers map[string]string, template string) (*HTTPGateway, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("sms http url must be an http(s) address")
	}
	if template == "" {
		template = DefaultBodyTemplate
	}
	return &HTTPGateway{
		url: rawURL, headers: headers, template: template,
		client: &http.Client{
			Timeout:       httpTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Name identifies the gateway.
func (g *HTTPGateway) Name() string { return "http" }

// Send posts one message.
func (g *HTTPGateway) Send(ctx context.Context, to, text string) error {
	if !ValidNumber(to) {
		return ErrInvalidNumber
	}
	body := strings.NewReplacer("{{to}}", jsonEscape(to), "{{text}}", jsonEscape(text)).Replace(g.template)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url, bytes.NewReader([]byte(body)))
	if err != nil {
		return fmt.Errorf("build sms request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range g.headers {
		req.Header.Set(k, v)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		// url.Error repeats the URL, which can hold a token in its query.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("send sms: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError("http", resp.StatusCode)
	}
	return nil
}

// jsonEscape returns s escaped for use inside a JSON string literal.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
