package notification

import (
	"context"
	"errors"
)

// ErrInvalidRequest marks a request rejected for caller-fixable reasons.
// Wrap it so handlers can answer 400 instead of 500.
var ErrInvalidRequest = errors.New("invalid notification request")

// NotificationService delivers a notification request.
type NotificationService interface {
	Send(ctx context.Context, req NotificationRequest) error
}
