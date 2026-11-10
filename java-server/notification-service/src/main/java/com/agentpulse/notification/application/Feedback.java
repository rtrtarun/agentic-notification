package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;

import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.util.List;
import java.util.Optional;
import java.util.function.Function;

/**
 * Layer 4: records what people do with a notification (opened, dismissed, undo, always, never, reply) as stored,
 * attributed signals, and turns "always" and "never" into visible overrides. Feedback never changes a critical
 * event: "never" on one is refused. A port of the Go service's {@code pkg/feedback}, kept the same by the shared
 * contract file {@code 12-feedback.json}.
 */
public final class Feedback {
    public static final String OPENED = "opened";
    public static final String DISMISSED = "dismissed";
    public static final String UNDO = "undo";
    public static final String ALWAYS = "always";
    public static final String NEVER = "never";
    public static final String REPLY = "reply";

    public static final String SCOPE_TYPE = "type";
    public static final String SCOPE_REPOSITORY = "repository";
    public static final String SCOPE_SOURCE = "source";

    /** The longest reply or note, in bytes. */
    public static final int MAX_REASON_BYTES = 1000;

    private Feedback() {}

    /** The request is not valid. */
    public static final class InvalidException extends RuntimeException {
        public InvalidException(String detail) {
            super("invalid feedback: " + detail);
        }
    }

    /** No such event among the recent ones. */
    public static final class EventNotFoundException extends RuntimeException {
        public EventNotFoundException() {
            super("event not found");
        }
    }

    /** One signal. Source, event type and repository are copied from the event at the time. */
    public record Entry(String id, String eventId, String userId, String action, String scope, String reason,
                        String overrideId, String source, String eventType, String repository, Instant createdAt) {}

    /** Narrows a list. Empty or zero fields match everything. */
    public record Filter(String eventId, String userId, Instant since, int limit) {}

    /** Keeps feedback. Implementations: in memory, and PostgreSQL. */
    public interface Store {
        void add(Entry entry);

        /** Matching feedback, newest first. */
        List<Entry> list(Filter filter);
    }

    /** One feedback request. */
    public record Input(String eventId, String action, String scope, String reason) {
        public Input {
            eventId = eventId == null ? "" : eventId;
            action = action == null ? "" : action;
            scope = scope == null ? "" : scope;
            reason = reason == null ? "" : reason;
        }
    }

    /** What recording did. */
    public record Result(Entry entry, Overrides.Override override, boolean overrideSkipped, boolean reverted) {}

    /** Records feedback and applies its consequences. */
    public static final class Service {
        private final Store store;
        private final Function<String, Optional<Event>> lookup;
        private final Overrides.Service overrides;
        private final Clock clock;

        /** {@code overrides} may be null, in which case feedback is only recorded. */
        public Service(Store store, Function<String, Optional<Event>> lookup, Overrides.Service overrides, Clock clock) {
            this.store = store;
            this.lookup = lookup;
            this.overrides = overrides;
            this.clock = clock;
        }

        /**
         * Stores a signal. {@code actor} names the person (or the API key). {@code allowOverride} says whether this
         * caller may create or remove overrides; when false the signal is still stored for the learning layer.
         */
        public Result record(String actor, Input in, boolean allowOverride) {
            validate(in);
            Event evt = lookup.apply(in.eventId()).orElseThrow(EventNotFoundException::new);
            String scope = in.scope();
            String overrideId = "";
            Overrides.Override created = null;
            boolean skipped = false;
            boolean reverted = false;
            switch (in.action()) {
                case ALWAYS, NEVER -> {
                    if (NEVER.equals(in.action()) && Attention.CRITICAL.equals(evt.getAttentionClass())) {
                        throw new InvalidException("critical events cannot be muted");
                    }
                    if (scope.isEmpty()) {
                        scope = SCOPE_TYPE;
                    }
                    if (overrides == null || !allowOverride) {
                        skipped = true;
                    } else {
                        created = overrides.upsert(overrideFor(evt, in.action(), scope, actor));
                        overrideId = created.id();
                    }
                }
                case UNDO -> reverted = undo(actor, evt.getId(), allowOverride);
                default -> { }
            }
            Entry entry = new Entry(Overrides.newId(), evt.getId(), actor, in.action(), scope, in.reason().strip(), overrideId,
                    evt.getSource(), evt.getEventType(), evt.getRepository(), clock.instant());
            store.add(entry);
            return new Result(entry, created, skipped, reverted);
        }

        /** Notes that a person opened an event, once per person and event. */
        public void recordOpened(String actor, String eventId) {
            for (Entry e : store.list(new Filter(eventId, actor, Instant.EPOCH, 50))) {
                if (OPENED.equals(e.action())) {
                    return;
                }
            }
            record(actor, new Input(eventId, OPENED, "", ""), false);
        }

        /** Reverts this person's latest always or never on the event. Anything else only records the signal. */
        private boolean undo(String actor, String eventId, boolean allowOverride) {
            for (Entry f : store.list(new Filter(eventId, actor, Instant.EPOCH, 20))) {
                if (UNDO.equals(f.action())) {
                    return false;
                }
                if (f.overrideId().isEmpty()) {
                    continue;
                }
                if (!allowOverride || overrides == null) {
                    return false;
                }
                try {
                    overrides.delete(f.overrideId());
                } catch (Overrides.NotFoundException ignored) {
                    // already removed by hand; the undo still counts as done
                }
                return true;
            }
            return false;
        }

        private static Overrides.Override overrideFor(Event evt, String action, String scope, String actor) {
            return switch (scope) {
                case SCOPE_REPOSITORY -> new Overrides.Override("", "", "", evt.getRepository(), action, actor, "", null);
                case SCOPE_SOURCE -> new Overrides.Override("", evt.getSource(), "", "", action, actor, "", null);
                default -> new Overrides.Override("", evt.getSource(), evt.getEventType(), "", action, actor, "", null);
            };
        }
    }

    static void validate(Input in) {
        switch (in.action()) {
            case OPENED, DISMISSED, UNDO, ALWAYS, NEVER, REPLY -> { }
            default -> throw new InvalidException("action must be opened, dismissed, undo, always, never or reply");
        }
        if (in.eventId().isEmpty()) {
            throw new InvalidException("event id is required");
        }
        switch (in.scope()) {
            case "", SCOPE_TYPE, SCOPE_REPOSITORY, SCOPE_SOURCE -> { }
            default -> throw new InvalidException("scope must be type, repository or source");
        }
        if (!in.scope().isEmpty() && !ALWAYS.equals(in.action()) && !NEVER.equals(in.action())) {
            throw new InvalidException("scope only applies to always and never");
        }
        if (REPLY.equals(in.action()) && in.reason().strip().isEmpty()) {
            throw new InvalidException("a reply needs text");
        }
        if (in.reason().getBytes(StandardCharsets.UTF_8).length > MAX_REASON_BYTES) {
            throw new InvalidException("text must be at most " + MAX_REASON_BYTES + " characters");
        }
    }
}
