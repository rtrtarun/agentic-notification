package notification

import (
	"context"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
)

// Limits bound what an API caller may ask the service to contact. Caller-supplied
// webhook URLs and recipients are untrusted input: without limits they let any
// caller make the server POST to internal hosts (SSRF) or send mail anywhere.
type Limits struct {
	// AllowedWebhookHosts lists permitted webhook hosts. "*.example.com" matches
	// any subdomain. An empty list rejects every caller-supplied webhook URL.
	AllowedWebhookHosts []string
	// AllowedEmailDomains restricts recipients to these domains. Empty allows any
	// syntactically valid address.
	AllowedEmailDomains []string
	MaxRecipients       int
	MaxWebhooks         int
}

const (
	defaultMaxRecipients = 20
	defaultMaxWebhooks   = 10
	maxTitleLength       = 200
	maxMessageLength     = 20000
)

// Validate checks a request against the limits. Every error wraps ErrInvalidRequest.
func (l Limits) Validate(req NotificationRequest) error {
	if req.Agent.Name == "" {
		return invalid("agent name required")
	}
	if req.Title == "" {
		return invalid("title required")
	}
	if len(req.Title) > maxTitleLength || containsLineBreak(req.Title) {
		return invalid("title must be a single line of at most %d characters", maxTitleLength)
	}
	if len(req.Message) > maxMessageLength {
		return invalid("message exceeds %d characters", maxMessageLength)
	}

	if err := l.validateRecipients(req.EmailRecipients); err != nil {
		return err
	}
	webhooks := append(append([]string{}, req.SlackWebhookURLs...), req.TeamsWebhookURLs...)
	return l.validateWebhooks(webhooks)
}

func (l Limits) validateRecipients(recipients []string) error {
	if len(recipients) > orDefault(l.MaxRecipients, defaultMaxRecipients) {
		return invalid("too many email recipients")
	}
	for _, rcpt := range recipients {
		addr, err := mail.ParseAddress(rcpt)
		if err != nil || addr.Address != rcpt || containsLineBreak(rcpt) {
			return invalid("invalid email recipient")
		}
		if len(l.AllowedEmailDomains) > 0 && !matchesAny(domainOf(addr.Address), l.AllowedEmailDomains) {
			return invalid("email domain not allowed")
		}
	}
	return nil
}

func (l Limits) validateWebhooks(urls []string) error {
	if len(urls) > orDefault(l.MaxWebhooks, defaultMaxWebhooks) {
		return invalid("too many webhook urls")
	}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return invalid("webhook url must be an https url without credentials")
		}
		if port := u.Port(); port != "" && port != "443" {
			return invalid("webhook url must use the default https port")
		}
		if !matchesAny(strings.ToLower(u.Hostname()), l.AllowedWebhookHosts) {
			return invalid("webhook host not allowed")
		}
	}
	return nil
}

// ValidatingService rejects requests that break the limits before they reach the
// wrapped service, so every entry point shares one policy.
type ValidatingService struct {
	next   NotificationService
	limits Limits
}

// NewValidatingService wraps next with limits.
func NewValidatingService(next NotificationService, limits Limits) *ValidatingService {
	return &ValidatingService{next: next, limits: limits}
}

// Send validates req and forwards it.
func (s *ValidatingService) Send(ctx context.Context, req NotificationRequest) error {
	if err := s.limits.Validate(req); err != nil {
		return err
	}
	return s.next.Send(ctx, req)
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}

func containsLineBreak(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}

func domainOf(addr string) string {
	return strings.ToLower(addr[strings.LastIndex(addr, "@")+1:])
}

// matchesAny reports whether host equals a pattern or matches a "*.suffix" pattern.
func matchesAny(host string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == host {
			return true
		}
		if strings.HasPrefix(p, "*.") && strings.HasSuffix(host, p[1:]) {
			return true
		}
	}
	return false
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
