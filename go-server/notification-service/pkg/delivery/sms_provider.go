package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agentpulse/notification-service/pkg/notification"
	"github.com/agentpulse/notification-service/pkg/sms"
)

// maxSMSChars keeps one alert to a few segments; a text is a nudge, not a report.
const maxSMSChars = 320

// ErrPermanent marks a failure that retrying cannot fix, such as a malformed
// phone number. The dispatcher dead-letters these at once.
var ErrPermanent = errors.New("permanent delivery failure")

// SMSProvider delivers a short text through an sms.Gateway.
type SMSProvider struct {
	gateway sms.Gateway
}

// NewSMSProvider wraps a gateway.
func NewSMSProvider(gateway sms.Gateway) *SMSProvider { return &SMSProvider{gateway: gateway} }

// Name identifies the provider.
func (p *SMSProvider) Name() string { return "sms" }

// Send texts every number on the request; it is a no-op without numbers.
func (p *SMSProvider) Send(ctx context.Context, req notification.NotificationRequest) error {
	if len(req.SMSRecipients) == 0 {
		return nil
	}
	if p.gateway == nil {
		return fmt.Errorf("%w: sms gateway is not configured", ErrPermanent)
	}
	for _, to := range req.SMSRecipients {
		if !sms.ValidNumber(to) {
			return fmt.Errorf("%w: %w", ErrPermanent, sms.ErrInvalidNumber)
		}
	}
	text := smsText(req)
	var errs []error
	for _, to := range req.SMSRecipients {
		if err := p.gateway.Send(ctx, to, text); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// smsText folds the title and message onto one trimmed line.
func smsText(req notification.NotificationRequest) string {
	text := strings.Join(strings.Fields(req.Title+": "+req.Message), " ")
	if r := []rune(text); len(r) > maxSMSChars {
		text = string(r[:maxSMSChars-1]) + "…"
	}
	return text
}
