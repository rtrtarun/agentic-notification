package notification

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func baseRequest() NotificationRequest {
	return NotificationRequest{Agent: AgentInfo{Name: "agent"}, Title: "title", Message: "hello"}
}

func TestLimitsValidate(t *testing.T) {
	limits := Limits{
		AllowedWebhookHosts: []string{"hooks.slack.com", "*.webhook.office.com"},
		AllowedEmailDomains: []string{"example.com"},
		MaxRecipients:       2,
		MaxWebhooks:         2,
	}
	tests := []struct {
		name    string
		mut     func(*NotificationRequest)
		wantErr bool
	}{
		{"valid minimal", func(r *NotificationRequest) {}, false},
		{"valid slack", func(r *NotificationRequest) {
			r.SlackWebhookURLs = []string{"https://hooks.slack.com/services/T/B/x"}
		}, false},
		{"valid teams wildcard", func(r *NotificationRequest) {
			r.TeamsWebhookURLs = []string{"https://contoso.webhook.office.com/path"}
		}, false},
		{"valid email", func(r *NotificationRequest) { r.EmailRecipients = []string{"ops@example.com"} }, false},
		{"missing agent", func(r *NotificationRequest) { r.Agent.Name = "" }, true},
		{"missing title", func(r *NotificationRequest) { r.Title = "" }, true},
		{"title with newline", func(r *NotificationRequest) { r.Title = "a\r\nBcc: evil@x.com" }, true},
		{"title too long", func(r *NotificationRequest) { r.Title = strings.Repeat("a", maxTitleLength+1) }, true},
		{"message too long", func(r *NotificationRequest) { r.Message = strings.Repeat("a", maxMessageLength+1) }, true},
		{"http webhook", func(r *NotificationRequest) { r.SlackWebhookURLs = []string{"http://hooks.slack.com/x"} }, true},
		{"internal host", func(r *NotificationRequest) { r.SlackWebhookURLs = []string{"https://169.254.169.254/latest"} }, true},
		{"localhost", func(r *NotificationRequest) { r.TeamsWebhookURLs = []string{"https://localhost/x"} }, true},
		{"credentials in url", func(r *NotificationRequest) {
			r.SlackWebhookURLs = []string{"https://user:pw@hooks.slack.com/x"}
		}, true},
		{"non-default port", func(r *NotificationRequest) { r.SlackWebhookURLs = []string{"https://hooks.slack.com:8443/x"} }, true},
		{"suffix lookalike", func(r *NotificationRequest) {
			r.SlackWebhookURLs = []string{"https://evilwebhook.office.com.attacker.io/x"}
		}, true},
		{"too many webhooks", func(r *NotificationRequest) {
			u := "https://hooks.slack.com/x"
			r.SlackWebhookURLs = []string{u, u, u}
		}, true},
		{"unparseable url", func(r *NotificationRequest) { r.SlackWebhookURLs = []string{"://bad"} }, true},
		{"foreign email domain", func(r *NotificationRequest) { r.EmailRecipients = []string{"x@evil.com"} }, true},
		{"display name email", func(r *NotificationRequest) { r.EmailRecipients = []string{"Ops <ops@example.com>"} }, true},
		{"email header injection", func(r *NotificationRequest) { r.EmailRecipients = []string{"a@example.com\r\nBcc: b@evil.com"} }, true},
		{"too many recipients", func(r *NotificationRequest) {
			r.EmailRecipients = []string{"a@example.com", "b@example.com", "c@example.com"}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := baseRequest()
			tt.mut(&req)
			err := limits.Validate(req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error must wrap ErrInvalidRequest, got %v", err)
			}
		})
	}
}

func TestLimitsRejectWebhooksWhenAllowListEmpty(t *testing.T) {
	req := baseRequest()
	req.SlackWebhookURLs = []string{"https://hooks.slack.com/x"}
	if err := (Limits{}).Validate(req); err == nil {
		t.Fatal("empty allow-list must reject caller-supplied webhooks")
	}
}

func TestLimitsAnyEmailDomainWhenUnrestricted(t *testing.T) {
	req := baseRequest()
	req.EmailRecipients = []string{"someone@anywhere.org"}
	if err := (Limits{}).Validate(req); err != nil {
		t.Fatalf("unrestricted domains should allow valid address: %v", err)
	}
}

type countingService struct{ calls int }

func (c *countingService) Send(context.Context, NotificationRequest) error {
	c.calls++
	return nil
}

func TestValidatingServiceBlocksInvalidRequests(t *testing.T) {
	inner := &countingService{}
	svc := NewValidatingService(inner, Limits{})

	bad := baseRequest()
	bad.Title = ""
	if err := svc.Send(context.Background(), bad); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
	if inner.calls != 0 {
		t.Fatal("invalid request must not reach the wrapped service")
	}

	if err := svc.Send(context.Background(), baseRequest()); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("expected 1 forwarded call, got %d", inner.calls)
	}
}

func TestValidateRejectsCallerSuppliedSMSRecipients(t *testing.T) {
	req := NotificationRequest{Agent: AgentInfo{Name: "a"}, Title: "t", SMSRecipients: []string{"+14155550100"}}
	if err := (Limits{}).Validate(req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}
