// Package sms is the one place the service talks to a carrier. The delivery
// layer depends only on Gateway, so a vendor can be swapped without touching
// the notification or attention logic.
package sms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// Gateway sends one text message.
type Gateway interface {
	// Name identifies the gateway in logs and in the console ("mock", "http", "sns").
	Name() string
	// Send delivers text to one E.164 number.
	Send(ctx context.Context, to, text string) error
}

// ErrInvalidNumber is returned for a number that is not E.164.
var ErrInvalidNumber = errors.New("sms number must be E.164, for example +14155550100")

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// ValidNumber reports whether s is an E.164 phone number.
func ValidNumber(s string) bool { return e164.MatchString(s) }

// MaskNumber keeps the country prefix and the last two digits: "+14155550100" becomes "+14*****00".
func MaskNumber(s string) string {
	if len(s) < 6 {
		return "***"
	}
	return s[:3] + strings.Repeat("*", len(s)-5) + s[len(s)-2:]
}

// Message is one text recorded by the Mock gateway.
type Message struct {
	To   string
	Text string
}

// Mock is the local development gateway. It sends nothing; it logs and keeps
// the messages so tests and the console can see what would have been sent.
type Mock struct {
	logger *slog.Logger
	mu     sync.Mutex
	sent   []Message
}

// NewMock returns a mock gateway. A nil logger is allowed.
func NewMock(logger *slog.Logger) *Mock { return &Mock{logger: logger} }

// Name identifies the gateway.
func (m *Mock) Name() string { return "mock" }

// Send records the message.
func (m *Mock) Send(ctx context.Context, to, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidNumber(to) {
		return ErrInvalidNumber
	}
	m.mu.Lock()
	m.sent = append(m.sent, Message{To: to, Text: text})
	m.mu.Unlock()
	if m.logger != nil {
		// The number is masked: logs are not the place for phone numbers.
		m.logger.Info("sms (mock, not sent)", "to", MaskNumber(to), "chars", len(text))
	}
	return nil
}

// Sent returns a copy of the recorded messages.
func (m *Mock) Sent() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message(nil), m.sent...)
}

// statusError describes a non-2xx reply without echoing the body, which can contain the number or a token.
func statusError(gateway string, code int) error {
	return fmt.Errorf("%s gateway returned status %d", gateway, code)
}
