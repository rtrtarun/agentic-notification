package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/budget"
	"github.com/agentpulse/notification-service/pkg/dedup"
	"github.com/agentpulse/notification-service/pkg/delivery"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/notification"
	"github.com/agentpulse/notification-service/pkg/overrides"
	"github.com/agentpulse/notification-service/pkg/policy"
	"github.com/agentpulse/notification-service/pkg/preferences"
	"github.com/agentpulse/notification-service/pkg/stream"
	"github.com/agentpulse/notification-service/pkg/telemetry"
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
	// RoutedFor is Routed taking a rule's channel override into account.
	RoutedFor(class string, override []string) bool
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
	budget       budget.Budget
	async        bool
	tracer       *telemetry.Tracer
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
	ctx, span := p.tracer.Start(ctx, "pipeline.Handle")
	out, err := p.handle(ctx, evt)
	if errors.Is(err, ErrDuplicateDelivery) {
		span.End(nil)
		return out, ErrDuplicateDelivery
	}
	span.End(err)
	return out, err
}

func (p *Pipeline) handle(ctx context.Context, evt event.Event) (event.Event, error) {
	exists, err := p.eventRepo.Exists(ctx, evt.ID)
	if err != nil {
		return evt, fmt.Errorf("check duplicate delivery: %w", err)
	}
	if exists {
		return evt, ErrDuplicateDelivery
	}

	_, policySpan := p.tracer.Start(ctx, "policy.Evaluate")
	evt, adaptive := p.applyPolicy(evt)
	policySpan.End(nil)
	dedupCtx, dedupSpan := p.tracer.Start(ctx, "dedup.Check")
	duplicate := p.applyDedup(dedupCtx, &evt)
	dedupSpan.End(nil)

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
		switch {
		case duplicate:
			evt.DecisionState.Status = event.DecisionMerged
		case p.async:
			p.deferAttention(&evt)
		default:
			p.attend(ctx, &evt, adaptive)
		}
		if evt.DecisionState.Status == "" {
			evt.DecisionState.Status = p.defaultStatus(evt)
		}
		if evt.DecisionState.Status == event.DecisionQueued && !duplicate && !evt.AttentionPending {
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
		EventID: evt.ID, Class: string(evt.AttentionClass), Group: evt.Source, Channels: evt.RouteChannels,
		Title: fmt.Sprintf("[%s] %s/%s", strings.ToUpper(string(evt.AttentionClass)), evt.Source, evt.EventType),
		Body: fmt.Sprintf("%s\nSource: %s\nEvent: %s\nRepository: %s\nReason: %s",
			evt.AttentionClass, evt.Source, evt.EventType, evt.Repository, reason),
	}
}

// applyPreferences lets a person's quiet hours and mutes change a non-critical
// event's outcome. It never runs for critical events: Handle only calls it from
// the non-critical branch, and preferences.Decide refuses critical events too.
func (p *Pipeline) applyPreferences(evt *event.Event) (overridden bool) {
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
			return true
		}
	}
	if p.prefs == nil {
		return false
	}
	adj, ok := p.prefs.Decide(*evt)
	if !ok {
		return false
	}
	evt.AttentionClass = adj.AttentionClass
	evt.DecisionState.Status = adj.Status
	evt.DecisionState.Reason = adj.Reason
	return false
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
	if evt.AttentionClass == event.AttentionBackground && (p.outbox == nil || !p.outbox.RoutedFor(event.AttentionBackground, evt.RouteChannels)) {
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
	cctx, span := p.tracer.Start(ctx, "attention.Classify")
	c, err := p.classifier.Classify(cctx, *evt)
	span.End(err)
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
		evt.RouteChannels = decision.Channels
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

	sctx, span := p.tracer.Start(ctx, "delivery.Send")
	err := p.deliverySvc.Send(sctx, notification.NotificationRequest{
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
	span.End(err)
	return err
}

// waitingMarker is added to an event's reason while it waits for the attention worker.
const waitingMarker = "; waiting for the attention worker"

// WithBudget caps how many standard events interrupt people per window. When it is
// used up, further standard events are held for the next digest with the status
// budget_exceeded. It is never consulted for a critical event, and an "always notify"
// override outranks it. A budget store that fails lets the event through.
func (p *Pipeline) WithBudget(b budget.Budget) *Pipeline {
	p.budget = b
	return p
}

// WithAsyncAttention makes Handle save non-critical events as queued and pending
// instead of deciding on them, leaving classification, preferences, the budget and
// delivery to the attention worker (see ProcessPending). Only enable it when a
// worker is running, or those events will wait. Critical events are unaffected:
// they are always delivered inline.
func (p *Pipeline) WithAsyncAttention() *Pipeline {
	p.async = true
	return p
}

// attend runs the steps that decide how a non-critical event interrupts people.
func (p *Pipeline) attend(ctx context.Context, evt *event.Event, adaptive bool) {
	p.applyAttention(ctx, evt, adaptive)
	overridden := p.applyPreferences(evt)
	p.applyBudget(ctx, evt, overridden)
}

// deferAttention leaves a non-critical event for the attention worker.
func (p *Pipeline) deferAttention(evt *event.Event) {
	evt.DecisionState.Status = event.DecisionQueued
	evt.DecisionState.Reason += waitingMarker
	evt.AttentionPending = true
}

// applyBudget holds a standard event that would interrupt someone when the budget
// is used up. It only counts events that would really be delivered (a standard
// route exists), never touches background events, and never runs for critical ones.
func (p *Pipeline) applyBudget(ctx context.Context, evt *event.Event, overridden bool) {
	if p.budget == nil || overridden || evt.AttentionClass != event.AttentionStandard || evt.DecisionState.Status != "" {
		return
	}
	if p.outbox == nil || !p.outbox.RoutedFor(event.AttentionStandard, evt.RouteChannels) {
		return
	}
	res, err := p.budget.Take(ctx, budget.ScopeWorkspace)
	if err != nil {
		p.logger.Warn("attention budget unavailable; letting the event through", "event_id", evt.ID, "error", err)
		return
	}
	if res.Allowed {
		return
	}
	evt.DecisionState.Status = event.DecisionBudgetExceeded
	evt.DecisionState.Reason += fmt.Sprintf("; attention budget used (%d of %d interruptions this hour), held for the next digest", res.Used, res.Limit)
}

// ProcessPending finishes the attention step for an event the worker claimed. It
// refuses a critical event: that never waits for a worker and is never throttled.
// The returned event carries the decision to store; delivery is queued here when
// the event will interrupt.
func (p *Pipeline) ProcessPending(ctx context.Context, evt event.Event) (event.Event, error) {
	if evt.AttentionClass == event.AttentionCritical {
		return evt, attention.ErrCritical
	}
	if evt.DecisionState == nil {
		evt.DecisionState = &event.DecisionState{}
	}
	state := *evt.DecisionState
	evt.DecisionState = &state
	evt.DecisionState.Reason = strings.TrimSuffix(evt.DecisionState.Reason, waitingMarker)
	evt.DecisionState.Status = ""

	adaptive, channels := p.ruleNow(evt)
	evt.RouteChannels = channels
	p.attend(ctx, &evt, adaptive)
	p.settle(ctx, &evt)
	p.announce(evt)
	return evt, nil
}

// ReleaseDelayed lets an event that was held for quiet hours go out once they have
// ended. It reports whether the event changed and needs its decision stored; a
// critical event is refused and never changes.
func (p *Pipeline) ReleaseDelayed(ctx context.Context, evt event.Event) (event.Event, bool, error) {
	if evt.AttentionClass == event.AttentionCritical {
		return evt, false, attention.ErrCritical
	}
	if evt.DecisionState == nil || evt.DecisionState.Status != event.DecisionDelayed {
		return evt, false, nil
	}
	state := *evt.DecisionState
	evt.DecisionState = &state
	if p.prefs != nil {
		if adj, ok := p.prefs.Decide(evt); ok {
			if adj.Status == event.DecisionDelayed {
				return evt, false, nil // still inside quiet hours
			}
			evt.AttentionClass, evt.DecisionState.Status, evt.DecisionState.Reason = adj.AttentionClass, adj.Status, adj.Reason
			p.announce(evt)
			return evt, true, nil
		}
	}
	evt.DecisionState.Status = ""
	_, evt.RouteChannels = p.ruleNow(evt)
	evt.DecisionState.Reason += "; released after quiet hours"
	p.applyBudget(ctx, &evt, false)
	p.settle(ctx, &evt)
	p.announce(evt)
	return evt, true, nil
}

// settle gives an event its final status when nothing else has, and queues its delivery.
func (p *Pipeline) settle(ctx context.Context, evt *event.Event) {
	if evt.DecisionState.Status == "" {
		evt.DecisionState.Status = p.defaultStatus(*evt)
	}
	if evt.DecisionState.Status == event.DecisionQueued {
		p.enqueueDelivery(ctx, *evt)
	}
}

func (p *Pipeline) announce(evt event.Event) {
	if p.publisher != nil {
		p.publisher.Publish(stream.Message{Type: stream.TypeEvent, ID: evt.ID, AttentionClass: evt.AttentionClass})
	}
}

// ruleNow reads the current rule for an event: whether it is adaptive (an event no rule
// decides is adaptive, as it is at ingest) and the channels it routes to, if any.
func (p *Pipeline) ruleNow(evt event.Event) (adaptive bool, channels []string) {
	if decision, matched := p.policyEngine.Evaluate(evt); matched {
		return decision.Adaptive, decision.Channels
	}
	return true, nil
}

// WithTracer records a span for each step of Handle (pipeline.Handle, policy.Evaluate,
// dedup.Check, attention.Classify and delivery.Send), joined to the caller's trace when
// the context carries one. Without it, nothing is recorded.
func (p *Pipeline) WithTracer(t *telemetry.Tracer) *Pipeline {
	p.tracer = t
	return p
}
