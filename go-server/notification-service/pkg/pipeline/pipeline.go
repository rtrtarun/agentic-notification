package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/example/notification-service/pkg/dedup"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/notification"
	"github.com/example/notification-service/pkg/policy"
)

// ErrDuplicateDelivery means an event with the same ID was already processed,
// typically a webhook redelivery. The caller should acknowledge it without
// delivering anything again.
var ErrDuplicateDelivery = errors.New("event already processed")

// forgetTimeout bounds the cleanup that runs after a failed save.
const forgetTimeout = 2 * time.Second

// Config holds the default channels used for critical events.
type Config struct {
	DefaultEmailRecipients []string
	SlackWebhookURLs       []string
	TeamsWebhookURLs       []string
}

type Pipeline struct {
	policyEngine *policy.Engine
	deliverySvc  *delivery.Service
	eventRepo    event.Repository
	cfg          Config
	deduper      dedup.Deduper
	logger       *slog.Logger
}

func New(
	engine *policy.Engine,
	deliverySvc *delivery.Service,
	eventRepo event.Repository,
	cfg Config,
) *Pipeline {
	return &Pipeline{
		policyEngine: engine,
		deliverySvc:  deliverySvc,
		eventRepo:    eventRepo,
		cfg:          cfg,
		logger:       slog.Default(),
	}
}

// WithLogger sets the logger used for non-fatal problems such as a dedup store outage.
func (p *Pipeline) WithLogger(l *slog.Logger) *Pipeline {
	if l != nil {
		p.logger = l
	}
	return p
}

// WithDeduper enables deduplication and threading for non-critical events.
func (p *Pipeline) WithDeduper(d dedup.Deduper) *Pipeline {
	p.deduper = d
	return p
}

// Handle runs one event through policy, dedup, delivery and persistence. It
// returns ErrDuplicateDelivery when the event ID was already processed.
func (p *Pipeline) Handle(ctx context.Context, evt event.Event) (event.Event, error) {
	exists, err := p.eventRepo.Exists(ctx, evt.ID)
	if err != nil {
		return evt, fmt.Errorf("check duplicate delivery: %w", err)
	}
	if exists {
		return evt, ErrDuplicateDelivery
	}

	evt = p.applyPolicy(evt)
	duplicate := p.applyDedup(ctx, &evt)

	switch evt.AttentionClass {
	case event.AttentionCritical:
		if err := p.deliverCritical(ctx, evt); err != nil {
			p.forgetDedup(evt)
			return evt, fmt.Errorf("deliver critical event: %w", err)
		}
		evt.DecisionState.Status = event.DecisionSent
	default:
		if duplicate {
			evt.DecisionState.Status = event.DecisionMerged
		}
		if evt.DecisionState.Status == "" {
			evt.DecisionState.Status = event.DecisionQueued
		}
	}

	if err := p.eventRepo.Save(ctx, evt); err != nil {
		// Without this, GitHub's retry of this event would be merged as a duplicate and lost.
		p.forgetDedup(evt)
		return evt, fmt.Errorf("persist event: %w", err)
	}
	return evt, nil
}

// applyDedup tags the event with its dedup key and thread. It reports whether the
// event duplicates a recent one. Critical events are tagged but never merged.
func (p *Pipeline) applyDedup(ctx context.Context, evt *event.Event) bool {
	if p.deduper == nil {
		return false
	}
	res, err := p.deduper.Check(ctx, *evt)
	if err != nil {
		// Fail open: an unavailable dedup store must never suppress an event.
		p.logger.Warn("dedup check failed; treating event as unique", "event_id", evt.ID, "error", err)
	}
	evt.DedupKey = res.DedupKey
	evt.ThreadID = res.ThreadID
	if !res.Duplicate || evt.AttentionClass == event.AttentionCritical {
		return false
	}
	evt.MergedInto = res.OriginalID
	evt.DecisionState.Reason = "duplicate of " + res.OriginalID
	return true
}

// forgetDedup releases the dedup entry this event created after a failure, using a
// fresh context because the request context may be the reason processing failed.
func (p *Pipeline) forgetDedup(evt event.Event) {
	if p.deduper == nil || evt.DedupKey == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), forgetTimeout)
	defer cancel()
	if err := p.deduper.Forget(ctx, evt.DedupKey, evt.ID); err != nil {
		p.logger.Warn("dedup forget failed", "event_id", evt.ID, "error", err)
	}
}

func (p *Pipeline) applyPolicy(evt event.Event) event.Event {
	decision, matched := p.policyEngine.Evaluate(evt)
	if matched {
		evt.AttentionClass = decision.AttentionClass
		evt.DecisionState = &event.DecisionState{
			Reason:      decision.Reason,
			MatchedRule: decision.MatchedRule,
		}
	} else {
		evt.AttentionClass = event.AttentionStandard
		evt.DecisionState = &event.DecisionState{
			Reason: "no matching rule, default standard",
		}
	}
	return evt
}

func (p *Pipeline) deliverCritical(ctx context.Context, evt event.Event) error {
	title := fmt.Sprintf("[CRITICAL] %s/%s", evt.Source, evt.EventType)
	message := fmt.Sprintf(
		"Critical alert\nSource: %s\nEvent: %s\nRepository: %s\nReason: %s",
		evt.Source, evt.EventType, evt.Repository, evt.DecisionState.Reason,
	)

	return p.deliverySvc.Send(ctx, notification.NotificationRequest{
		Agent: notification.AgentInfo{
			Name:  "attention-orchestration",
			Group: evt.Source,
		},
		Title:            title,
		Message:          message,
		EmailRecipients:  p.cfg.DefaultEmailRecipients,
		SlackWebhookURLs: p.cfg.SlackWebhookURLs,
		TeamsWebhookURLs: p.cfg.TeamsWebhookURLs,
	})
}
