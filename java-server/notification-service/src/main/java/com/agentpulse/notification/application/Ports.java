package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * The ports the application depends on. Infrastructure implements them; nothing here knows about SQL, SMTP, Redis
 * or HTTP. A critical event only ever uses {@link EventRepository}, {@link NotificationProvider} and the logging
 * half of {@link Outbox}; the classifier, preferences, budget and the queue are never consulted for it.
 */
public final class Ports {
    private Ports() {}

    /** Thrown by a port when its backing store or channel fails. */
    public static final class PortException extends RuntimeException {
        public PortException(String message, Throwable cause) {
            super(message, cause);
        }
    }

    /** Persists events. {@code save} is idempotent on the event id. */
    public interface EventRepository {
        void save(Event event);

        boolean exists(String id);

        /** The most recent events, newest first, at most {@code limit}. */
        List<Event> listRecent(int limit);
    }

    /** What a provider is asked to deliver, and to whom. */
    public record Notification(String title, String message, String agentName, String agentOwner, String agentGroup,
                               Targets targets) {}

    /** Delivers a notification to the targets it carries. Implementations throw {@link PortException} on failure. */
    public interface NotificationProvider {
        void send(Notification notification);

        String name();
    }

    /** What Attention Intelligence decided for one event. */
    public record Classification(String attentionClass, String model, double confidence, String reason) {}

    /** Layer 2. The pipeline never calls it for a critical event and only accepts "background" back. */
    public interface Classifier {
        Classification classify(Event event);
    }

    /** A person's quiet hours or mute changing the outcome of a non-critical event. */
    public record Adjustment(String attentionClass, String status, String reason) {}

    /** Quiet hours and mutes. Consulted only for events that are not critical. */
    public interface Preferences {
        Optional<Adjustment> decide(Event event);
    }

    /** Result of taking one interruption from the budget. */
    public record BudgetResult(boolean allowed, int used, int limit) {}

    /** Caps how many standard events interrupt people per window. Never consulted for a critical event. */
    public interface Budget {
        BudgetResult take();
    }

    /** Queues non-critical deliveries and logs critical ones. A routing table plus a queue; nothing decides by model. */
    public interface Outbox {
        /** Reports whether a class has a configured channel to be delivered to. */
        boolean routed(String attentionClass);

        /** Queues the event for the channels its class is routed to. Never called for a critical event. */
        void enqueue(Event event);

        /** Logs an inline (critical) delivery next to the queued ones. It decides nothing; its failure is only logged. */
        void recordDirect(Event event, String channels, RuntimeException sendError);
    }

    /** Keeps the ordered rule list. */
    public interface RuleStore {
        List<Rule> list();

        /** Stores exactly this list, in this order, all or nothing. */
        void replace(List<Rule> rules);
    }

    /** Keeps the delivery queue. */
    public interface DeliveryStore {
        /** Adds a pending job; false, and nothing changes, when a job for the same event and channel exists. */
        boolean enqueue(Delivery.Job job);

        /** Stores or replaces the log entry of an inline delivery. */
        void record(Delivery.Job job);

        /** Leases up to {@code limit} due jobs and counts the attempt. Several instances may claim at once. */
        List<Delivery.Job> claim(Instant now, Duration lease, int limit);

        void complete(String id, Instant now);

        /** Records an error. A null {@code retryAt} moves the job to dead. */
        void fail(String id, String error, Instant retryAt, Instant now);

        /** Moves a dead job back to pending, due now, with its attempts reset. */
        void requeue(String id, Instant now);

        /** Newest first. */
        List<Delivery.Job> list(int limit);

        Map<String, Integer> counts();
    }
}
