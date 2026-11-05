package delivery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/example/notification-service/pkg/notification"
)

// Targets are where each channel delivers. A channel with no targets is not
// configured and is never routed to.
type Targets struct {
	Email []string
	Slack []string
	Teams []string
	SMS   []string
}

var channelOrder = []string{ChannelEmail, ChannelSlack, ChannelTeams, ChannelSMS}

// For returns a channel's targets.
func (t Targets) For(channel string) []string {
	switch channel {
	case ChannelEmail:
		return t.Email
	case ChannelSlack:
		return t.Slack
	case ChannelTeams:
		return t.Teams
	case ChannelSMS:
		return t.SMS
	}
	return nil
}

// Channels lists the configured channels in a stable order.
func (t Targets) Channels() []string {
	var out []string
	for _, c := range channelOrder {
		if len(t.For(c)) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// Request builds a request that reaches only the given channel.
func (t Targets) Request(channel, title, message, group string) (notification.NotificationRequest, bool) {
	targets := t.For(channel)
	if len(targets) == 0 {
		return notification.NotificationRequest{}, false
	}
	req := notification.NotificationRequest{
		Agent: notification.AgentInfo{Name: "attention-orchestration", Group: group},
		Title: title, Message: message,
	}
	switch channel {
	case ChannelEmail:
		req.EmailRecipients = targets
	case ChannelSlack:
		req.SlackWebhookURLs = targets
	case ChannelTeams:
		req.TeamsWebhookURLs = targets
	case ChannelSMS:
		req.SMSRecipients = targets
	}
	return req, true
}

// ParseChannels turns "slack, email" into channel IDs and rejects unknown names.
func ParseChannels(list []string) ([]string, error) {
	var out []string
	for _, raw := range list {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		known := false
		for _, c := range channelOrder {
			known = known || c == name
		}
		if !known {
			return nil, fmt.Errorf("unknown delivery channel %q (use email, slack, teams or sms)", raw)
		}
		out = append(out, name)
	}
	return out, nil
}

// Message is what an event asks the outbox to deliver.
type Message struct {
	EventID string
	Class   string
	Title   string
	Body    string
	Group   string
}

// Outbox turns processed events into queued jobs according to a route table.
// The table is plain configuration: which channels receive which attention
// class. There is no model in this path.
type Outbox struct {
	store   Store
	targets Targets
	routes  map[string][]string
	now     func() time.Time
}

// NewOutbox builds an outbox. routes maps an attention class to channel IDs.
// Critical is never routed here: critical events are delivered inline.
func NewOutbox(store Store, targets Targets, routes map[string][]string) *Outbox {
	return &Outbox{store: store, targets: targets, routes: routes, now: time.Now}
}

// Enqueue creates one job per routed, configured channel and returns how many
// were newly created. Calling it again for the same event creates none.
func (o *Outbox) Enqueue(ctx context.Context, m Message) (int, error) {
	if m.Class == "critical" {
		return 0, nil
	}
	created := 0
	for _, channel := range o.routes[m.Class] {
		if len(o.targets.For(channel)) == 0 {
			continue
		}
		now := o.now()
		ok, err := o.store.Enqueue(ctx, Job{
			ID: NewJobID(), EventID: m.EventID, Channel: channel, Class: m.Class,
			Title: m.Title, Message: m.Body, Group: m.Group,
			Status: StatusPending, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return created, fmt.Errorf("enqueue %s delivery: %w", channel, err)
		}
		if ok {
			created++
		}
	}
	return created, nil
}

// RecordDirect logs an inline delivery (critical events) so it shows next to
// queued ones. It never decides anything and its failure is the caller's to log.
func (o *Outbox) RecordDirect(ctx context.Context, m Message, channels string, sendErr error) error {
	now := o.now()
	j := Job{
		ID: NewJobID(), EventID: m.EventID, Channel: channels, Class: m.Class,
		Title: m.Title, Group: m.Group, Status: StatusSent, Attempts: 1, Direct: true,
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if sendErr != nil {
		j.Status, j.LastError = StatusDead, RedactError(sendErr)
	}
	return o.store.Record(ctx, j)
}

// Routed reports whether any configured channel receives the class.
func (o *Outbox) Routed(class string) bool {
	for _, c := range o.routes[class] {
		if len(o.targets.For(c)) > 0 {
			return true
		}
	}
	return false
}

// Targets returns the configured targets.
func (o *Outbox) Targets() Targets { return o.targets }
