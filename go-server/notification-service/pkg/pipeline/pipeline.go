package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/example/notification-service/pkg/attention"
	"github.com/example/notification-service/pkg/dedup"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/notification"
	"github.com/example/notification-service/pkg/overrides"
	"github.com/example/notification-service/pkg/policy"
	"github.com/example/notification-service/pkg/preferences"
	"github.com/example/notification-service/pkg/stream"
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
	SMSRecipients          []string
}

// Preferences adjusts non-critical events (quiet hours, muted sources). It is
// consulted only for events that are not critical.
type Preferences interface {
	Decide(evt event.Event) (preferences.Adjustment, bool)
}

// Overrides holds the always and never notify choices people made from feedback.
// They outrank preferences and never answer for a critical event.
type Overrides interface {
	Decide(evt event.Event) (overrides.Decision, bool)
}

// Outbox queues non-critical deliveries and logs critical ones. It is a plain
// routing table plus a queue; nothing in it decides by model.
type Outbox interface {
	Enqueue(ctx context.Context, m delivery.Message) (int, error)
	// Routed reports whether the class has a configured channel to be sent to.
	Routed(class string) bool
	RecordDirect(ctx context.Context, m delivery.Message, channels string, sendErr error) error
}

// Publisher is told when an event has been processed, so connected consoles can refresh.
type Publisher interface {
	Publish(m stream.Message)
}

type Pipeline struct {
	prefs        Preferences
	overrides    Overrides
	classifier   attention.Classifier
	outbox       Outbox
	publisher    Publisher
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

// WithPreferences applies quiet hours and muted sources to non-critical events.
func (p *Pipeline) WithPreferences(pref Preferences) *Pipeline {
	p.prefs = pref
	return p
}

// WithClassifier lets Attention Intelligence refine non-critical events whose rule
// allows it. The classifier is wrapped so it can never see a critical event or
// escalate one. Without it, rules alone decide.
func (p *Pipeline) WithClassifier(c attention.Classifier) *Pipeline {
	p.classifier = attention.Guarded(c)
	return p
}

// WithOverrides applies always and never notify choices before quiet hours and mutes.
func (p *Pipeline) WithOverrides(o Overrides) *Pipeline {
	p.overrides = o
	return p
}

// WithOutbox sends standard and background events to the channels they are routed to,
// and logs each critical delivery.
func (p *Pipeline) WithOutbox(o Outbox) *Pipeline {
	p.outbox = o
	return p
}

// WithPublisher announces each processed event to live consoles.
func (p *Pipeline) WithPublisher(pub Publisher) *Pipeline {
	p.publisher = pub
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

	evt, adaptive := p.applyPolicy(evt)
	duplicate := p.applyDedup(ctx, &evt)

	switch evt.AttentionClass {
	case event.AttentionCritical:
		err := p.deliverCritical(ctx, evt)
		p.logDirect(ctx, evt, err)
		if err != nil {
			p.forgetDedup(evt)
			return evt, fmt.Errorf("deliver critical event: %w", err)
		}
		evt.DecisionState.Status = event.DecisionSent
	default:
		if duplicate {
			evt.DecisionState.Status = event.DecisionMerged
		} else {
			p.applyAttention(ctx, &evt, adaptive)
			p.applyPreferences(&evt)
		}
		if evt.DecisionState.Status == "" {
			evt.DecisionState.Status = p.defaultStatus(evt)
		}
		if evt.DecisionState.Status == event.DecisionQueued && !duplicate {
			p.enqueueDelivery(ctx, evt)
		}
	}

	if err := p.eventRepo.Save(ctx, evt); err != nil {
		// Without this, GitHub's retry of this event would be merged as a duplicate and lost.
		p.forgetDedup(evt)
		return evt, fmt.Errorf("persist event: %w", err)
	}
	if p.publisher != nil {
		p.publisher.Publish(stream.Message{Type: stream.TypeEvent, ID: evt.ID, AttentionClass: evt.AttentionClass})
	}
	return evt, nil
}

// enqueueDelivery queues the event for the channels its class is routed to. Only
// events still "queued" get here: a muted, held or merged event stays quiet. A queue
// failure is logged and never fails the event, which is saved either way.
func (p *Pipeline) enqueueDelivery(ctx context.Context, evt event.Event) {
	if p.outbox == nil {
		return
	}
	if _, err := p.outbox.Enqueue(ctx, deliveryMessage(evt)); err != nil {
		p.logger.Warn("queue delivery failed; event kept without delivery", "event_id", evt.ID, "error", delivery.RedactError(err))
	}
}

// logDirect records a critical delivery next to the queued ones.
func (p *Pipeline) logDirect(ctx context.Context, evt event.Event, sendErr error) {
	if p.outbox == nil {
		return
	}
	if err := p.outbox.RecordDirect(ctx, deliveryMessage(evt), p.deliverySvc.ProviderName(), sendErr); err != nil {
		p.logger.Warn("log critical delivery failed", "event_id", evt.ID, "error", delivery.RedactError(err))
	}
}

func deliveryMessage(evt event.Event) delivery.Message {
	reason := ""
	if evt.DecisionState != nil {
		reason = evt.DecisionState.Reason
	}
	return delivery.Message{
		EventID: evt.ID, Class: string(evt.AttentionClass), Group: evt.Source,
		Title: fmt.Sprintf("[%s] %s/%s", strings.ToUpper(string(evt.AttentionClass)), evt.Source, evt.EventType),
		Body: fmt.Sprintf("%s\nSource: %s\nEvent: %s\nRepository: %s\nReason: %s",
			evt.AttentionClass, evt.Source, evt.EventType, evt.Repository, reason),
	}
}

// applyPreferences lets a person's quiet hours and mutes change a non-critical
// event's outcome. It never runs for critical events: Handle only calls it from
// the non-critical branch, and preferences.Decide refuses critical events too.
func (p *Pipeline) applyPreferences(evt *event.Event) {
	if evt.AttentionClass == event.AttentionCritical {
		return
	}
	// A person's explicit choice outranks quiet hours and mutes: "never" sends the
	// event to the digest, and "always" keeps it queued whatever the preferences say.
	if p.overrides != nil {
		if d, ok := p.overrides.Decide(*evt); ok {
			evt.DecisionState.Reason = d.Reason
			if d.Mode == overrides.ModeNever {
				evt.AttentionClass = event.AttentionBackground
				evt.DecisionState.Status = event.DecisionDigest
			}
			return
		}
	}
	if p.prefs == nil {
		return
	}
	adj, ok := p.prefs.Decide(*evt)
	if !ok {
		return
	}
	evt.AttentionClass = adj.AttentionClass
	evt.DecisionState.Status = adj.Status
	evt.DecisionState.Reason = adj.Reason
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

// defaultStatus is "queued", except for background events when nothing is
// configured to deliver them: those are held for the digest.
func (p *Pipeline) defaultStatus(evt event.Event) string {
	if evt.AttentionClass == event.AttentionBackground && (p.outbox == nil || !p.outbox.Routed(event.AttentionBackground)) {
		return event.DecisionDigest
	}
	return event.DecisionQueued
}

// applyAttention lets Attention Intelligence refine an event whose rule allows it
// (or that no rule decided). It only ever moves a standard event to background, and
// only for an event that is not critical. A failure keeps the rule's decision.
func (p *Pipeline) applyAttention(ctx context.Context, evt *event.Event, adaptive bool) {
	if p.classifier == nil || !adaptive || evt.AttentionClass != event.AttentionStandard {
		return
	}
	// A person's explicit choice outranks judgement: leave events an override covers alone.
	if p.overrides != nil {
		if _, ok := p.overrides.Decide(*evt); ok {
			return
		}
	}
	c, err := p.classifier.Classify(ctx, *evt)
	if err != nil {
		p.logger.Warn("classification failed; keeping the rule's decision", "event_id", evt.ID, "error", err)
		return
	}
	evt.DecisionState.ModelVersion, evt.DecisionState.Confidence = c.Model, c.Confidence
	evt.DecisionState.Reason += "; attention " + c.Reason()
	if c.Class == event.AttentionBackground {
		evt.AttentionClass = event.AttentionBackground
	}
}

func (p *Pipeline) applyPolicy(evt event.Event) (event.Event, bool) {
	decision, matched := p.policyEngine.Evaluate(evt)
	if matched {
		evt.AttentionClass = decision.AttentionClass
		evt.DecisionState = &event.DecisionState{
			Reason:      decision.Reason,
			MatchedRule: decision.MatchedRule,
		}
		return evt, decision.Adaptive
	}
	// No rule decided, so nothing explicit can be second-guessed: the event is adaptive.
	evt.AttentionClass = event.AttentionStandard
	evt.DecisionState = &event.DecisionState{
		Reason: "no matching rule, default standard",
	}
	return evt, true
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
		SMSRecipients:    p.cfg.SMSRecipients,
	})
}
