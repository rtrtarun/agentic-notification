// Package integrations reports the state of inbound integrations: whether the
// endpoint is ready to accept events, how they are authenticated, and what has
// arrived. It reads only; it never exposes a secret, only whether one is set.
package integrations

import (
	"sort"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
)

// Statuses.
const (
	// StatusReady means events are accepted and their signature is verified.
	StatusReady = "ready"
	// StatusUnsigned means events are accepted without verification (local development only).
	StatusUnsigned = "unsigned"
	// StatusBlocked means events are rejected with 503 until a secret is configured.
	StatusBlocked = "blocked"
)

// GitHubSettings is how the GitHub webhook is configured.
type GitHubSettings struct {
	SecretConfigured bool
	AllowUnsigned    bool
}

// EventCount is an event type with how many arrived.
type EventCount struct {
	EventType string `json:"eventType"`
	Count     int    `json:"count"`
}

// Integration is one inbound source.
type Integration struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	WebhookPath string `json:"webhookPath"`
	// Signing says how requests are verified.
	Signing          string       `json:"signing"`
	SecretConfigured bool         `json:"secretConfigured"`
	EventsReceived   int          `json:"eventsReceived"`
	LastEventAt      *time.Time   `json:"lastEventAt,omitempty"`
	LastEventType    string       `json:"lastEventType,omitempty"`
	EventTypes       []EventCount `json:"eventTypes"`
	// Setup names the environment variable that configures it.
	Setup string `json:"setup"`
}

const maxEventTypes = 8

// GitHub reports the GitHub integration from its settings and the stored events.
func GitHub(s GitHubSettings, events []event.Event) Integration {
	in := Integration{
		ID: "github", Name: "GitHub", WebhookPath: "/webhooks/github",
		SecretConfigured: s.SecretConfigured, EventTypes: []EventCount{},
		Setup: "GITHUB_WEBHOOK_SECRET",
	}
	switch {
	case s.SecretConfigured:
		in.Status, in.Signing = StatusReady, "HMAC-SHA256 signature required (X-Hub-Signature-256)"
	case s.AllowUnsigned:
		in.Status, in.Signing = StatusUnsigned, "No verification: ALLOW_UNSIGNED_WEBHOOKS is on"
	default:
		in.Status, in.Signing = StatusBlocked, "Rejected until GITHUB_WEBHOOK_SECRET is set"
	}

	counts := map[string]int{}
	for _, e := range events {
		if e.Source != "github" {
			continue
		}
		in.EventsReceived++
		counts[e.EventType]++
		if in.LastEventAt == nil || e.CreatedAt.After(*in.LastEventAt) {
			at := e.CreatedAt
			in.LastEventAt, in.LastEventType = &at, e.EventType
		}
	}
	for t, n := range counts {
		in.EventTypes = append(in.EventTypes, EventCount{EventType: t, Count: n})
	}
	sort.Slice(in.EventTypes, func(i, j int) bool {
		if in.EventTypes[i].Count != in.EventTypes[j].Count {
			return in.EventTypes[i].Count > in.EventTypes[j].Count
		}
		return in.EventTypes[i].EventType < in.EventTypes[j].EventType
	})
	if len(in.EventTypes) > maxEventTypes {
		in.EventTypes = in.EventTypes[:maxEventTypes]
	}
	return in
}
