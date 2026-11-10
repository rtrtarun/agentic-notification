package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;

import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.time.Clock;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.Optional;
import java.util.concurrent.CopyOnWriteArrayList;

/**
 * The "always notify" and "never notify" choices people make from feedback. They are plain, visible rules in the
 * deterministic layer: an override matches by source, event type and repository, the most specific one wins, and
 * none of them can ever touch a critical event. A port of the Go service's {@code pkg/overrides}; the shared
 * contract file {@code 12-feedback.json} keeps the two the same.
 */
public final class Overrides {
    public static final String ALWAYS = "always";
    public static final String NEVER = "never";

    private static final int MAX_FIELD_BYTES = 200;
    private static final String SCOPE_SEP = "\u001f";
    private static final SecureRandom RANDOM = new SecureRandom();

    private Overrides() {}

    /** Says what to do with non-critical events that match. An empty field matches anything. */
    public record Override(String id, String source, String eventType, String repository, String mode, String createdBy,
                           String note, Instant createdAt) {
        public Override {
            id = id == null ? "" : id;
            source = source == null ? "" : source;
            eventType = eventType == null ? "" : eventType;
            repository = repository == null ? "" : repository;
            createdBy = createdBy == null ? "" : createdBy;
            note = note == null ? "" : note;
        }

        public Override withIdAndTime(String newId, Instant time) {
            return new Override(newId, source, eventType, repository, mode, createdBy, note, time);
        }
    }

    /** What an override decided for one event. {@code reason} names the override in the event's decision. */
    public record Decision(String mode, String overrideId, String reason) {}

    /** The override is not valid. */
    public static final class InvalidException extends RuntimeException {
        public InvalidException(String detail) {
            super("invalid override: " + detail);
        }
    }

    /** No override has this id. */
    public static final class NotFoundException extends RuntimeException {
        public NotFoundException() {
            super("override not found");
        }
    }

    /** Keeps overrides. Implementations: in memory, and PostgreSQL. */
    public interface Store {
        List<Override> load();

        /** Inserts an override or, when one with the same scope exists, replaces its mode (keeping id and time). */
        Override save(Override override);

        /** @throws NotFoundException when there is no such override */
        void delete(String id);
    }

    /** Serves decisions from a cached copy, so deciding costs no query. The cache is per process. */
    public static final class Service {
        private final Store store;
        private final Clock clock;
        private final List<Override> list;

        public Service(Store store, Clock clock) {
            this.store = store;
            this.clock = clock;
            this.list = new CopyOnWriteArrayList<>(store.load());
        }

        /** Every override, newest first. */
        public List<Override> list() {
            List<Override> out = new ArrayList<>(list);
            out.sort(Comparator.comparing(Override::createdAt, Comparator.nullsFirst(Comparator.naturalOrder())).reversed());
            return out;
        }

        /** Validates and saves an override. The same scope replaces the old one. */
        public synchronized Override upsert(Override in) {
            Override o = new Override("", in.source().strip(), in.eventType().strip(), in.repository().strip(), in.mode(),
                    in.createdBy(), in.note().strip(), null);
            validate(o);
            Override saved = store.save(o.withIdAndTime(newId(), clock.instant()));
            boolean replaced = false;
            for (int i = 0; i < list.size(); i++) {
                if (scopeKey(list.get(i)).equals(scopeKey(saved))) {
                    list.set(i, saved);
                    replaced = true;
                }
            }
            if (!replaced) {
                list.add(saved);
            }
            return saved;
        }

        /** Removes an override. */
        public synchronized void delete(String id) {
            store.delete(id);
            list.removeIf(o -> o.id().equals(id));
        }

        /**
         * The override that applies to a non-critical event, or empty. It never answers for a critical event. The
         * most specific match wins; between equally specific ones "always" wins, because missing something is worse
         * than one extra message.
         */
        public Optional<Decision> decide(Event event) {
            if (Attention.CRITICAL.equals(event.getAttentionClass())) {
                return Optional.empty();
            }
            Override best = null;
            for (Override o : list) {
                if (!matches(o, event)) {
                    continue;
                }
                if (best == null || specificity(o) > specificity(best)
                        || (specificity(o) == specificity(best) && ALWAYS.equals(o.mode()) && !ALWAYS.equals(best.mode()))) {
                    best = o;
                }
            }
            if (best == null) {
                return Optional.empty();
            }
            return Optional.of(new Decision(best.mode(), best.id(), best.mode() + " notify override for " + describe(best)));
        }
    }

    /** Names an override's scope in a few words. */
    public static String describe(Override o) {
        List<String> parts = new ArrayList<>();
        if (!o.source().isEmpty()) {
            parts.add(o.source());
        }
        if (!o.eventType().isEmpty()) {
            parts.add(o.eventType());
        }
        if (!o.repository().isEmpty()) {
            parts.add("repository " + o.repository());
        }
        return String.join(" / ", parts);
    }

    /** The identity of an override's scope, for stores that enforce uniqueness. */
    public static String scopeKey(Override o) {
        return o.source().toLowerCase(Locale.ROOT) + SCOPE_SEP + o.eventType().toLowerCase(Locale.ROOT) + SCOPE_SEP
                + o.repository().toLowerCase(Locale.ROOT);
    }

    static void validate(Override o) {
        if (!ALWAYS.equals(o.mode()) && !NEVER.equals(o.mode())) {
            throw new InvalidException("mode must be always or never");
        }
        if (o.source().isEmpty() && o.eventType().isEmpty() && o.repository().isEmpty()) {
            throw new InvalidException("choose a source, an event type or a repository");
        }
        for (String v : List.of(o.source(), o.eventType(), o.repository(), o.note())) {
            if (v.getBytes(StandardCharsets.UTF_8).length > MAX_FIELD_BYTES) {
                throw new InvalidException("values must be at most " + MAX_FIELD_BYTES + " characters");
            }
        }
        if (!o.repository().isEmpty() && Glob.validate(o.repository().toLowerCase(Locale.ROOT)) != null) {
            throw new InvalidException("the repository pattern is malformed");
        }
    }

    private static int specificity(Override o) {
        int n = 0;
        for (String v : List.of(o.source(), o.eventType(), o.repository())) {
            if (!v.isEmpty()) {
                n++;
            }
        }
        return n;
    }

    private static boolean matches(Override o, Event e) {
        if (!o.source().isEmpty() && !o.source().equalsIgnoreCase(e.getSource())) {
            return false;
        }
        if (!o.eventType().isEmpty() && !o.eventType().equalsIgnoreCase(e.getEventType())) {
            return false;
        }
        return o.repository().isEmpty()
                || Glob.matches(o.repository().toLowerCase(Locale.ROOT), e.getRepository().toLowerCase(Locale.ROOT));
    }

    static String newId() {
        byte[] b = new byte[12];
        RANDOM.nextBytes(b);
        return HexFormat.of().formatHex(b);
    }
}
