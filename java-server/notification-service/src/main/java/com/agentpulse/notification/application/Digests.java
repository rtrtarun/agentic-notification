package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Classification;
import com.agentpulse.notification.application.Ports.Classifier;
import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Status;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.security.SecureRandom;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.function.Supplier;

/**
 * Digests: a summary of what was held back, with the events it covers. A port of the Go service's
 * {@code pkg/attention/digest.go}, kept the same by the shared contract file {@code 13-attention.json}. A digest
 * never contains a critical event or a merged repeat. In this service a digest is stored and readable; it is not
 * yet handed to the delivery queue.
 */
public final class Digests {
    /** The most events one digest covers; the rest wait for the next. */
    public static final int MAX_EVENTS = 500;
    private static final int MAX_HIGHLIGHTS = 3;
    /** The start of time, used as the beginning of the first digest's window, as in the Go service. */
    public static final Instant BEGINNING = Instant.parse("0001-01-01T00:00:00Z");
    private static final Set<String> HELD = Set.of(Status.DIGEST, Status.DELAYED, Status.BUDGET_EXCEEDED);
    private static final SecureRandom RANDOM = new SecureRandom();

    private Digests() {}

    /** The part of a digest about one source or repository. */
    public record Group(String key, String title, String summary, int count, List<String> eventIds) {}

    /** An event worth looking at first. */
    public record Highlight(String eventId, String title, String reason, int score) {}

    /** A summary of what was held back. {@code userId} is empty for the workspace digest. */
    public record Digest(String id, Instant from, Instant to, int count, String title, String summary, List<Group> groups,
                         List<Highlight> highlights, String model, String userId, String source, String createdBy,
                         Instant createdAt) {}

    /** No digest has this id. */
    public static final class NotFoundException extends RuntimeException {
        public NotFoundException() {
            super("digest not found");
        }
    }

    /** Keeps digests. Implementations: in memory, and PostgreSQL. */
    public interface Store {
        void add(Digest digest);

        /** Workspace digests, newest first. Personal digests are never included. */
        List<Digest> list(int limit);

        /** @throws NotFoundException when there is no such digest */
        Digest get(String id);
    }

    /** Writes digests from the events that were held back. One digest at a time, so two triggers cannot overlap. */
    public static final class Generator {
        private final Supplier<List<Event>> events;
        private final Store store;
        private final Classifier classifier;
        private final Heuristic summarizer;
        private final Clock clock;

        /** {@code classifier} picks highlights and may be null. */
        public Generator(Supplier<List<Event>> events, Store store, Classifier classifier, Heuristic summarizer, Clock clock) {
            this.events = events;
            this.store = store;
            this.classifier = GuardedClassifier.of(classifier);
            this.summarizer = summarizer;
            this.clock = clock;
        }

        /**
         * Covers everything held back since the previous digest: events whose outcome is digest, delayed or
         * budget_exceeded, never critical ones and never merged repeats. Returns the digest, or null (and stores
         * nothing) when there is nothing to report.
         */
        public synchronized Digest generate(String actor) {
            List<Digest> latest = store.list(1);
            Instant from = latest.isEmpty() ? BEGINNING : latest.get(0).to();
            Instant to = clock.instant();
            List<Event> held = new ArrayList<>();
            for (Event e : events.get()) {
                if (Attention.CRITICAL.equals(e.getAttentionClass()) || !e.getMergedInto().isEmpty() || e.getDecisionState() == null
                        || !HELD.contains(e.getDecisionState().getStatus())) {
                    continue;
                }
                Instant at = e.getCreatedAt();
                if (at == null || !at.isAfter(from) || at.isAfter(to)) {
                    continue;
                }
                held.add(e);
            }
            if (held.isEmpty()) {
                return null;
            }
            held.sort(Comparator.comparing(Event::getCreatedAt));
            if (held.size() > MAX_EVENTS) {
                // Keep the oldest and move the window up to the last one kept, so the rest go in the next digest.
                held = new ArrayList<>(held.subList(0, MAX_EVENTS));
                to = held.get(held.size() - 1).getCreatedAt();
            }
            Heuristic.Summary sum = summarizer.summarize(held);
            List<Group> groups = new ArrayList<>();
            for (Heuristic.Group g : sum.groups()) {
                groups.add(new Group(g.key(), g.title(), g.summary(), g.count(), g.eventIds()));
            }
            Digest d = new Digest(newId(), from, to, held.size(), sum.title(), sum.text(), groups, highlights(held),
                    Heuristic.MODEL, "", "heuristic", actor, clock.instant());
            store.add(d);
            return d;
        }

        /** The held events the classifier scores highest, so the digest leads with what is most likely to matter. */
        private List<Highlight> highlights(List<Event> held) {
            if (classifier == null) {
                return List.of();
            }
            List<Highlight> out = new ArrayList<>();
            for (Event e : held) {
                Classification c;
                try {
                    c = classifier.classify(e);
                } catch (RuntimeException ex) {
                    continue;
                }
                if (c.score() < Heuristic.BASE_SCORE) { // only events at or above a neutral score are worth leading with
                    continue;
                }
                out.add(new Highlight(e.getId(), eventTitle(e), c.reason(), c.score()));
            }
            out.sort(Comparator.comparingInt(Highlight::score).reversed());
            return out.size() > MAX_HIGHLIGHTS ? new ArrayList<>(out.subList(0, MAX_HIGHLIGHTS)) : out;
        }

        private static String eventTitle(Event e) {
            String title = e.getSource() + " " + e.getEventType();
            return e.getRepository().isEmpty() ? title : title + " in " + e.getRepository();
        }
    }

    /** Generates a digest on a fixed interval. With a zero interval it never runs. */
    public static final class Scheduler {
        private static final Logger LOG = LoggerFactory.getLogger(Scheduler.class);

        private final Generator generator;
        private final Duration every;
        private ScheduledExecutorService executor;

        public Scheduler(Generator generator, Duration every) {
            this.generator = generator;
            this.every = every;
        }

        public synchronized void start() {
            if (every.isZero() || every.isNegative() || executor != null) {
                return;
            }
            executor = Executors.newSingleThreadScheduledExecutor(r -> {
                Thread t = new Thread(r, "digest-scheduler");
                t.setDaemon(true);
                return t;
            });
            executor.scheduleWithFixedDelay(this::runOnce, every.toMillis(), every.toMillis(), TimeUnit.MILLISECONDS);
        }

        /** One scheduled pass; a failure is logged and never stops the schedule. */
        void runOnce() {
            try {
                Digest d = generator.generate("schedule");
                if (d != null) {
                    LOG.info("digest generated digest={} events={}", d.id(), d.count());
                }
            } catch (RuntimeException e) {
                LOG.error("scheduled digest failed error={}", e.getMessage());
            }
        }

        public synchronized void stop() {
            if (executor != null) {
                executor.shutdownNow();
                executor = null;
            }
        }
    }

    // ---- JSON shape ----------------------------------------------------------------------------------------

    /**
     * The digest as the Go service writes it: an empty list of highlights is {@code null}, and a workspace digest
     * has no {@code userId}. Used for the API and for the stored body, so either service can read the other's rows.
     */
    public static Map<String, Object> toMap(Digest d) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", d.id());
        m.put("from", d.from().toString());
        m.put("to", d.to().toString());
        m.put("count", d.count());
        m.put("title", d.title());
        m.put("summary", d.summary());
        List<Map<String, Object>> groups = new ArrayList<>();
        for (Group g : d.groups()) {
            Map<String, Object> gm = new LinkedHashMap<>();
            gm.put("key", g.key());
            gm.put("title", g.title());
            gm.put("summary", g.summary());
            gm.put("count", g.count());
            gm.put("eventIds", g.eventIds());
            groups.add(gm);
        }
        m.put("groups", groups.isEmpty() ? null : groups);
        List<Map<String, Object>> highlights = new ArrayList<>();
        for (Highlight h : d.highlights()) {
            Map<String, Object> hm = new LinkedHashMap<>();
            hm.put("eventId", h.eventId());
            hm.put("title", h.title());
            hm.put("reason", h.reason());
            hm.put("score", h.score());
            highlights.add(hm);
        }
        m.put("highlights", highlights.isEmpty() ? null : highlights);
        m.put("model", d.model());
        if (!d.userId().isEmpty()) {
            m.put("userId", d.userId());
        }
        m.put("source", d.source());
        m.put("createdBy", d.createdBy());
        m.put("createdAt", d.createdAt().toString());
        return m;
    }

    /** Reads a digest written by either service. Missing or null lists are empty. */
    @SuppressWarnings("unchecked")
    public static Digest fromMap(Map<String, Object> m) {
        List<Group> groups = new ArrayList<>();
        for (Object o : listOf(m.get("groups"))) {
            Map<String, Object> g = (Map<String, Object>) o;
            List<String> ids = new ArrayList<>();
            listOf(g.get("eventIds")).forEach(i -> ids.add(String.valueOf(i)));
            groups.add(new Group(text(g, "key"), text(g, "title"), text(g, "summary"), number(g, "count"), ids));
        }
        List<Highlight> highlights = new ArrayList<>();
        for (Object o : listOf(m.get("highlights"))) {
            Map<String, Object> h = (Map<String, Object>) o;
            highlights.add(new Highlight(text(h, "eventId"), text(h, "title"), text(h, "reason"), number(h, "score")));
        }
        return new Digest(text(m, "id"), Instant.parse(text(m, "from")), Instant.parse(text(m, "to")), number(m, "count"),
                text(m, "title"), text(m, "summary"), groups, highlights, text(m, "model"), text(m, "userId"),
                text(m, "source"), text(m, "createdBy"), Instant.parse(text(m, "createdAt")));
    }

    private static List<?> listOf(Object o) {
        return o instanceof List<?> l ? l : List.of();
    }

    private static String text(Map<String, Object> m, String key) {
        return m.get(key) instanceof String s ? s : "";
    }

    private static int number(Map<String, Object> m, String key) {
        return m.get(key) instanceof Number n ? n.intValue() : 0;
    }

    static String newId() {
        byte[] b = new byte[12];
        RANDOM.nextBytes(b);
        return HexFormat.of().formatHex(b);
    }
}
