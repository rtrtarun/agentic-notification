package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Adjustment;
import com.agentpulse.notification.application.Ports.Budget;
import com.agentpulse.notification.application.Ports.BudgetResult;
import com.agentpulse.notification.application.Ports.Classification;
import com.agentpulse.notification.application.Ports.Classifier;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.Outbox;
import com.agentpulse.notification.application.Ports.Preferences;
import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.DecisionState;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.PolicyDecision;
import com.agentpulse.notification.domain.Status;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Optional;

/**
 * Runs one event through policy, dedup, delivery and persistence.
 *
 * <p>The sacred invariant: a critical event is decided by rules alone. It never reaches the classifier, the
 * preferences (mutes and quiet hours), the budget or the outbox queue, it is never merged by dedup, and it is
 * delivered inline before it is saved. Every one of those steps is only reachable from the non-critical branch
 * of {@link #handle}.
 */
public final class Pipeline {
    private static final Logger LOG = LoggerFactory.getLogger(Pipeline.class);

    /** Thrown when an event with the same id was already processed, typically a webhook redelivery. */
    public static final class DuplicateDeliveryException extends RuntimeException {
        public DuplicateDeliveryException(String id) {
            super("event already processed: " + id);
        }
    }

    /** Thrown when a critical event could not be delivered or an event could not be saved. */
    public static final class PipelineException extends RuntimeException {
        public PipelineException(String message, Throwable cause) {
            super(message, cause);
        }
    }

    private final PolicyEngine policy;
    private final NotificationProvider provider;
    private final EventRepository repository;
    private Dedup.Deduper deduper;
    private Classifier classifier;
    private Preferences preferences;
    private Budget budget;
    private Outbox outbox;
    private Targets criticalTargets = Targets.none();

    public Pipeline(PolicyEngine policy, NotificationProvider provider, EventRepository repository) {
        this.policy = policy;
        this.provider = provider;
        this.repository = repository;
    }

    /** Enables deduplication and threading. Critical events are tagged but never merged. */
    public Pipeline withDeduper(Dedup.Deduper deduper) {
        this.deduper = deduper;
        return this;
    }

    /** Lets Attention Intelligence refine standard events whose rule allows it. It can only demote. */
    public Pipeline withClassifier(Classifier classifier) {
        this.classifier = classifier;
        return this;
    }

    /** Applies quiet hours and mutes to non-critical events. */
    public Pipeline withPreferences(Preferences preferences) {
        this.preferences = preferences;
        return this;
    }

    /** Caps standard interruptions per window. */
    public Pipeline withBudget(Budget budget) {
        this.budget = budget;
        return this;
    }

    /** Where critical events are delivered inline: every configured channel. */
    public Pipeline withCriticalTargets(Targets targets) {
        this.criticalTargets = targets;
        return this;
    }

    /** Queues standard and background deliveries. Without it they are only recorded. */
    public Pipeline withOutbox(Outbox outbox) {
        this.outbox = outbox;
        return this;
    }

    /**
     * Processes one event and returns it with its decision.
     *
     * @throws DuplicateDeliveryException when the event id was already processed
     * @throws PipelineException when a critical delivery or the save fails
     */
    public Event handle(Event incoming) {
        if (repository.exists(incoming.getId())) {
            throw new DuplicateDeliveryException(incoming.getId());
        }

        Event event = incoming.copy();
        boolean adaptive = applyPolicy(event);
        boolean duplicate = applyDedup(event);

        if (event.isCritical()) {
            RuntimeException sendError = null;
            try {
                deliverCritical(event);
            } catch (RuntimeException e) {
                sendError = e;
            }
            logDirect(event, sendError);
            if (sendError != null) {
                forgetDedup(event);
                throw new PipelineException("deliver critical event: " + sendError.getMessage(), sendError);
            }
            event.getDecisionState().setStatus(Status.SENT);
        } else {
            attendNonCritical(event, adaptive, duplicate);
        }

        try {
            repository.save(event);
        } catch (RuntimeException e) {
            // Without this, GitHub's retry of this event would be merged as a duplicate and lost.
            forgetDedup(event);
            throw new PipelineException("persist event: " + e.getMessage(), e);
        }
        return event;
    }

    private void attendNonCritical(Event event, boolean adaptive, boolean duplicate) {
        DecisionState state = event.getDecisionState();
        if (duplicate) {
            state.setStatus(Status.MERGED);
            return;
        }
        applyAttention(event, adaptive);
        applyPreferences(event);
        applyBudget(event);
        if (state.getStatus().isEmpty()) {
            state.setStatus(defaultStatus(event));
        }
        if (Status.QUEUED.equals(state.getStatus()) && outbox != null) {
            try {
                outbox.enqueue(event);
            } catch (RuntimeException e) {
                // A queue failure never fails the event; it is saved either way.
                LOG.warn("queue delivery failed; event kept without delivery event_id={} error={}",
                        event.getId(), e.getMessage());
            }
        }
    }

    /** Returns true when the matched rule lets Attention Intelligence refine the outcome. */
    private boolean applyPolicy(Event event) {
        Optional<PolicyDecision> decision = policy.evaluate(event);
        if (decision.isPresent()) {
            PolicyDecision d = decision.get();
            event.setAttentionClass(d.attentionClass());
            event.setDecisionState(new DecisionState(d.reason(), d.matchedRule()));
            return d.adaptive();
        }
        // No rule decided, so nothing explicit can be second-guessed: the event is adaptive.
        event.setAttentionClass(Attention.STANDARD);
        event.setDecisionState(new DecisionState("no matching rule, default standard", ""));
        return true;
    }

    /** Tags the event with its key and thread; returns true only for a non-critical duplicate. */
    private boolean applyDedup(Event event) {
        if (deduper == null) {
            return false;
        }
        Dedup.Result result;
        try {
            result = deduper.check(event);
        } catch (RuntimeException e) {
            // Fail open: an unavailable dedup store must never suppress an event. It is still threaded and keyed.
            LOG.warn("dedup check failed; treating event as unique event_id={} error={}", event.getId(), e.getMessage());
            event.setDedupKey(Dedup.dedupKey(event));
            event.setThreadId(Dedup.threadId(event));
            return false;
        }
        event.setDedupKey(result.dedupKey());
        event.setThreadId(result.threadId());
        if (!result.duplicate() || event.isCritical()) {
            return false;
        }
        event.setMergedInto(result.originalId());
        event.getDecisionState().setReason("duplicate of " + result.originalId());
        return true;
    }

    private void forgetDedup(Event event) {
        if (deduper == null || event.getDedupKey().isEmpty()) {
            return;
        }
        try {
            deduper.forget(event.getDedupKey(), event.getId());
        } catch (RuntimeException e) {
            LOG.warn("dedup forget failed event_id={} error={}", event.getId(), e.getMessage());
        }
    }

    /** Only ever moves a standard event to background; a failure keeps the rule's decision. */
    private void applyAttention(Event event, boolean adaptive) {
        if (classifier == null || !adaptive || !Attention.STANDARD.equals(event.getAttentionClass())) {
            return;
        }
        Classification c;
        try {
            c = classifier.classify(event);
        } catch (RuntimeException e) {
            LOG.warn("classification failed; keeping the rule's decision event_id={} error={}",
                    event.getId(), e.getMessage());
            return;
        }
        event.getDecisionState().setModelVersion(c.model());
        event.getDecisionState().setConfidence(c.confidence());
        event.getDecisionState().setReason(event.getDecisionState().getReason() + "; attention " + c.reason());
        if (Attention.BACKGROUND.equals(c.attentionClass())) {
            event.setAttentionClass(Attention.BACKGROUND);
        }
    }

    private void applyPreferences(Event event) {
        if (preferences == null) {
            return;
        }
        Optional<Adjustment> adjustment = preferences.decide(event);
        if (adjustment.isEmpty()) {
            return;
        }
        Adjustment a = adjustment.get();
        event.setAttentionClass(a.attentionClass());
        event.getDecisionState().setStatus(a.status());
        event.getDecisionState().setReason(a.reason());
    }

    /** Holds a standard event that would interrupt someone once the budget is used up. */
    private void applyBudget(Event event) {
        DecisionState state = event.getDecisionState();
        if (budget == null || !Attention.STANDARD.equals(event.getAttentionClass()) || !state.getStatus().isEmpty()) {
            return;
        }
        if (outbox == null || !outbox.routed(Attention.STANDARD)) {
            return;
        }
        BudgetResult result;
        try {
            result = budget.take();
        } catch (RuntimeException e) {
            LOG.warn("attention budget unavailable; letting the event through event_id={} error={}",
                    event.getId(), e.getMessage());
            return;
        }
        if (result.allowed()) {
            return;
        }
        state.setStatus(Status.BUDGET_EXCEEDED);
        state.setReason(state.getReason() + "; attention budget used (" + result.used() + " of " + result.limit()
                + " interruptions this hour), held for the next digest");
    }

    /** "queued", except background events with nowhere to go: those are held for the digest. */
    private String defaultStatus(Event event) {
        boolean background = Attention.BACKGROUND.equals(event.getAttentionClass());
        if (background && (outbox == null || !outbox.routed(Attention.BACKGROUND))) {
            return Status.DIGEST;
        }
        return Status.QUEUED;
    }

    /** Logs a critical delivery next to the queued ones. It decides nothing and never fails the event. */
    private void logDirect(Event event, RuntimeException sendError) {
        if (outbox == null) {
            return;
        }
        try {
            outbox.recordDirect(event, provider.name(), sendError);
        } catch (RuntimeException e) {
            LOG.warn("log critical delivery failed event_id={} error={}", event.getId(), Delivery.redact(e.getMessage()));
        }
    }

    private void deliverCritical(Event event) {
        String title = "[CRITICAL] " + event.getSource() + "/" + event.getEventType();
        String message = "Critical alert\nSource: " + event.getSource()
                + "\nEvent: " + event.getEventType()
                + "\nRepository: " + event.getRepository()
                + "\nReason: " + event.getDecisionState().getReason();
        provider.send(new Notification(title, message, "attention-orchestration", "", event.getSource(), criticalTargets));
    }
}
