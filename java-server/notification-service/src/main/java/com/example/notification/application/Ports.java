package com.example.notification.application;

import com.example.notification.domain.Event;

import java.util.List;
import java.util.Optional;

/**
 * The ports the pipeline depends on. Infrastructure implements them; nothing here knows about SQL, SMTP or HTTP.
 * Every port may only be consulted for events that are not critical, except {@link EventRepository} and
 * {@link NotificationProvider}, which critical events do use.
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

    /** What a provider is asked to deliver. */
    public record Notification(String title, String message, String group) {}

    /** Delivers a notification to people. Implementations throw {@link PortException} on failure. */
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

    /** Queues non-critical deliveries. Critical events never go through it. */
    public interface Outbox {
        /** Reports whether a class has a channel to be delivered to. */
        boolean routed(String attentionClass);

        void enqueue(Event event);
    }
}
