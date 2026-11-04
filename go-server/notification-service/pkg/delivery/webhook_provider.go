package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/example/notification-service/pkg/notification"
)

// WebhookTimeout bounds one webhook POST.
const WebhookTimeout = 10 * time.Second

// ErrBlockedAddress is returned when a webhook resolves to a private or loopback address.
var ErrBlockedAddress = errors.New("webhook target resolves to a blocked address")

// WebhookProvider POSTs a JSON {"text": ...} body to every Slack and Teams webhook
// URL on the request. A failing URL does not stop the others.
type WebhookProvider struct {
	client *http.Client
}

// NewWebhookProvider returns a provider using the default client.
// With blockPrivate set, connections to loopback, private, link-local and
// unspecified addresses are refused at dial time, which also defeats DNS
// rebinding because the check sees the address actually being dialled.
func NewWebhookProvider(blockPrivate bool) *WebhookProvider {
	dialer := &net.Dialer{Timeout: WebhookTimeout}
	if blockPrivate {
		dialer.Control = refusePrivateAddresses
	}
	return &WebhookProvider{client: &http.Client{
		Timeout:   WebhookTimeout,
		Transport: &http.Transport{DialContext: dialer.DialContext, Proxy: nil},
		// A redirect could point at an internal host the allow-list never saw.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Name identifies the provider.
func (p *WebhookProvider) Name() string { return "webhook" }

// Send delivers to every webhook URL and returns all failures joined.
func (p *WebhookProvider) Send(ctx context.Context, req notification.NotificationRequest) error {
	urls := append(append([]string{}, req.SlackWebhookURLs...), req.TeamsWebhookURLs...)
	if len(urls) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]string{
		"text": fmt.Sprintf("%s\n\nAgent: %s\nGroup: %s", req.Message, req.Agent.Name, req.Agent.Group),
	})
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	var errs []error
	for _, target := range urls {
		if err := p.post(ctx, target, payload); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *WebhookProvider) post(ctx context.Context, target string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// refusePrivateAddresses is a net.Dialer Control hook run after DNS resolution.
func refusePrivateAddresses(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || isBlockedIP(ip) {
		return ErrBlockedAddress
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}
