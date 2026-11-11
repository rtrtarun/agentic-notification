package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Classification;
import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.domain.Status;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.security.SecureRandom;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.TreeMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.function.Supplier;

/**
 * Offline score tuning: turns stored feedback into reviewable, bounded score adjustments for the built-in heuristic.
 * A port of the Go service's {@code pkg/learning/tuning.go} and {@code pkg/attention/tuning.go}, kept the same by
 * the shared contract file {@code 15-learning-adjustments.json}.
 *
 * <p>It is offline: a run reads stored feedback, events and rules and writes proposals; it is never a step in the
 * live event path. Nothing it proposes changes a score until an admin approves it; the heuristic reads only the
 * in-memory {@link Snapshot} of approved adjustments, so the live path does a map lookup and no I/O. If the snapshot
 * was never loaded, or has not been refreshed for five minutes, events are scored exactly as without learning.
 *
 * <p>The rule is deterministic and explainable: for each source and event type, signals about delivered
 * (queued or sent), non-critical events are weighted by age (a fixed half-life), capped per person, and combined
 * as {@code offset = round(max * (P - N) / (P + N + 2))}, where P and N are the weighted positive (opened, reply,
 * always) and negative (dismissed, never) totals. The same feedback always gives the same proposals.
 *
 * <p>Guardrail, enforced when proposing, when approving and when building the snapshot: feedback about a critical
 * event is never counted, and a scope covered by a critical or locked rule is never adjusted. Explicit overrides
 * are never read or written here, and the pipeline does not classify events an override covers, so an override
 * always wins.
 */
public final class Tuning {
    public static final String PROPOSED = "proposed";
    public static final String ACTIVE = "active";
    public static final String REJECTED = "rejected";
    public static final String RETIRED = "retired";

    public static final String RUN_RUNNING = "running";
    public static final String RUN_DONE = "done";
    public static final String RUN_FAILED = "failed";

    public static final int DEFAULT_MIN_PEOPLE = 2;
    public static final int DEFAULT_MAX_ADJUSTMENT = 20;
    public static final Duration DEFAULT_HALF_LIFE = Duration.ofDays(14);
    /** Bounds any score offset the heuristic will apply, whatever a store holds. */
    public static final int MAX_DELTA = 50;
    /** How long a snapshot may go without a refresh before the heuristic stops using it. */
    public static final Duration DEFAULT_MAX_AGE = Duration.ofMinutes(5);
    /** How much one person's decayed signals can count for or against a scope. */
    static final double PER_PERSON_CAP = 3.0;
    /** Keeps small evidence small: offset = max * (P - N) / (P + N + SMOOTHING). */
    static final double SMOOTHING = 2.0;
    /** How long a run may stay running before it is treated as abandoned. */
    public static final Duration STALE_RUN_AFTER = Duration.ofMinutes(10);
    /** How often the snapshot is reloaded, so a change made through another service reaches this one. */
    public static final Duration SNAPSHOT_REFRESH = Duration.ofSeconds(30);
    /** What a run that never finished is marked with. */
    public static final String ABANDONED_RUN_TEXT = "abandoned: the run did not finish";
    private static final String SEP = "\u001f";

    private Tuning() {}

    // ---- errors --------------------------------------------------------------------------------------------

    /** No adjustment has this id. */
    public static final class NotFoundException extends RuntimeException {
        public NotFoundException() {
            super("adjustment not found");
        }
    }

    /** The adjustment was already approved, rejected or retired. */
    public static final class NotProposedException extends RuntimeException {
        public NotProposedException() {
            super("adjustment is no longer proposed");
        }
    }

    /** Only an active adjustment can be retired. */
    public static final class NotActiveException extends RuntimeException {
        public NotActiveException() {
            super("adjustment is not active");
        }
    }

    /** The adjustment would touch critical or locked routing, or is out of bounds. */
    public static final class GuardrailException extends RuntimeException {
        public GuardrailException() {
            super("critical and locked rules can never be adjusted by learning");
        }
    }

    /** Another learning run is running, in this process or any other sharing the database. */
    public static final class RunInProgressException extends RuntimeException {
        public RunInProgressException() {
            super("a learning run is already in progress");
        }
    }

    // ---- the snapshot the heuristic reads ------------------------------------------------------------------

    /** One approved score offset, as the heuristic sees it. {@code repository} empty means every repository. */
    public record Applied(String id, String source, String eventType, String repository, int delta, String summary) {}

    /** What the heuristic found for an event: the offset and the version of the set it came from. */
    public record Match(Applied adjustment, int version) {}

    /**
     * An in-memory snapshot of the approved adjustments. It is replaced whole, so a reader never sees a half-built
     * one. It answers "no adjustment" until it is first loaded and once it has not been refreshed within its
     * maximum age.
     */
    public static final class Snapshot {
        private record State(int version, Map<String, Applied> byScope, Instant loadedAt) {}

        private final Duration maxAge;
        private final Clock clock;
        private volatile State state;

        public Snapshot(Duration maxAge, Clock clock) {
            this.maxAge = maxAge == null || maxAge.isZero() || maxAge.isNegative() ? DEFAULT_MAX_AGE : maxAge;
            this.clock = clock;
        }

        /** Replaces the snapshot with the active adjustments at the given set version. Offsets are clamped to the bound. */
        public void set(int version, List<Applied> adjustments) {
            Map<String, Applied> m = new HashMap<>();
            for (Applied a : adjustments) {
                int delta = Math.max(-MAX_DELTA, Math.min(MAX_DELTA, a.delta()));
                if (delta == 0) {
                    continue;
                }
                m.put(key(a.source(), a.eventType(), a.repository()), new Applied(a.id(), a.source(), a.eventType(), a.repository(), delta, a.summary()));
            }
            state = new State(version, Map.copyOf(m), clock.instant());
        }

        /** The version of the set in force, or 0 when none is. */
        public int version() {
            State s = fresh();
            return s == null ? 0 : s.version();
        }

        /** How many adjustments are in force. */
        public int size() {
            State s = fresh();
            return s == null ? 0 : s.byScope().size();
        }

        /** The adjustment for an event, preferring one for its repository over one for the whole event type. */
        public Match lookup(Event event) {
            State s = fresh();
            if (s == null || s.byScope().isEmpty()) {
                return null;
            }
            if (!event.getRepository().isEmpty()) {
                Applied a = s.byScope().get(key(event.getSource(), event.getEventType(), event.getRepository()));
                if (a != null) {
                    return new Match(a, s.version());
                }
            }
            Applied a = s.byScope().get(key(event.getSource(), event.getEventType(), ""));
            return a == null ? null : new Match(a, s.version());
        }

        private State fresh() {
            State s = state;
            if (s == null || Duration.between(s.loadedAt(), clock.instant()).compareTo(maxAge) > 0) {
                return null;
            }
            return s;
        }

        private static String key(String source, String eventType, String repository) {
            return source.toLowerCase(Locale.ROOT) + SEP + eventType.toLowerCase(Locale.ROOT) + SEP + repository.toLowerCase(Locale.ROOT);
        }
    }

    /** The name of the heuristic as tuned by the approved set with this version. */
    public static String tunedModel(int version) {
        return Heuristic.MODEL + "+tuned-" + version;
    }

    // ---- records and stores --------------------------------------------------------------------------------

    /** What people did, in counts, with a plain-language summary. */
    public record Evidence(int positive, int negative, int people, int signals, int windowDays, String summary) {
        public static final Evidence NONE = new Evidence(0, 0, 0, 0, 0, "");
    }

    /** The shadow estimate: recent events replayed through the heuristic with and without the adjustment. */
    public record Impact(int replayed, int wouldChange) {}

    /**
     * One proposed or approved score offset for a kind of event. {@code version} is the version of the active set
     * when this adjustment last changed it (approved or retired); 0 until then.
     */
    public record Adjustment(String id, String source, String eventType, String repository, int delta, String status,
                             Evidence evidence, Impact impact, int version, Instant createdAt, Instant updatedAt,
                             String resolvedBy, Instant resolvedAt) {
        Adjustment resolved(String newStatus, String actor, Instant at, int newVersion) {
            return new Adjustment(id, source, eventType, repository, delta, newStatus, evidence, impact, newVersion, createdAt, at, actor, at);
        }
    }

    /** One background learning pass. */
    public record Run(String id, String status, Instant startedAt, Instant finishedAt, int feedbackRead, int eventsRead,
                      int proposalsMade, String error) {}

    /** Keeps adjustments, one per scope. Implementations: in memory, and PostgreSQL. */
    public interface AdjustmentRepository {
        /** Inserts or replaces the adjustment with this id. */
        void save(Adjustment adjustment);

        /** @throws NotFoundException when there is none */
        Adjustment get(String id);

        /** Adjustments with the status (all when empty), newest update first. */
        List<Adjustment> list(String status);

        /** The highest version of any adjustment, 0 when there are none. */
        int maxVersion();
    }

    /** Keeps the log of learning runs and makes sure only one is running. */
    public interface RunRepository {
        /**
         * Records a new running run. A run still marked running that started before {@code staleBefore} is first
         * marked failed as abandoned.
         *
         * @throws RunInProgressException when another run is running
         */
        void start(Run run, Instant staleBefore);

        /** Records the outcome of a run. */
        void finish(Run run);

        /** The latest runs, newest first. */
        List<Run> list(int limit);
    }

    /** Reads the rules, to find the protected ones. */
    public interface RuleReader {
        List<Rule> list();
    }

    /** Tunes how much evidence a proposal needs. */
    public record Config(int minSignals, int minPeople, int maxAdjustment, Duration lookback, Duration halfLife) {
        public Config {
            minSignals = minSignals <= 0 ? Learning.DEFAULT_MIN_SIGNALS : minSignals;
            minPeople = minPeople <= 0 ? DEFAULT_MIN_PEOPLE : minPeople;
            maxAdjustment = maxAdjustment <= 0 ? DEFAULT_MAX_ADJUSTMENT : Math.min(maxAdjustment, MAX_DELTA);
            lookback = lookback == null || lookback.isZero() || lookback.isNegative() ? Learning.DEFAULT_LOOKBACK : lookback;
            halfLife = halfLife == null || halfLife.isZero() || halfLife.isNegative() ? DEFAULT_HALF_LIFE : halfLife;
        }
    }

    // ---- the tuner -----------------------------------------------------------------------------------------

    /** Runs the analysis and applies an admin's decisions. One analysis or decision at a time in this process. */
    public static final class Tuner {
        private static final Logger LOG = LoggerFactory.getLogger(Tuner.class);

        private final AdjustmentRepository adjustments;
        private final RunRepository runs;
        private final Learning.FeedbackReader feedback;
        private final Supplier<List<Event>> events;
        private final RuleReader rules;
        private final Snapshot snapshot;
        private final Config cfg;
        private final Clock clock;
        private final Object lock = new Object();
        private final ExecutorService background = Executors.newCachedThreadPool(r -> {
            Thread t = new Thread(r, "learning-run");
            t.setDaemon(true);
            return t;
        });
        private final List<Future<?>> started = new ArrayList<>();
        private final SecureRandom random = new SecureRandom();

        public Tuner(AdjustmentRepository adjustments, RunRepository runs, Learning.FeedbackReader feedback,
                     Supplier<List<Event>> events, RuleReader rules, Snapshot snapshot, Config cfg, Clock clock) {
            this.adjustments = adjustments;
            this.runs = runs;
            this.feedback = feedback;
            this.events = events;
            this.rules = rules;
            this.snapshot = snapshot;
            this.cfg = cfg;
            this.clock = clock;
        }

        /** The settings in use, for the screen to show. */
        public Config config() {
            return cfg;
        }

        /** The version of the active set the heuristic is using, 0 when none. */
        public int version() {
            return snapshot == null ? 0 : snapshot.version();
        }

        public List<Adjustment> list(String status) {
            return adjustments.list(status);
        }

        public List<Run> runs(int limit) {
            return runs.list(limit);
        }

        /** Records a run and returns at once; the analysis continues in the background. */
        public Run start() {
            Run run = begin();
            synchronized (started) {
                started.removeIf(Future::isDone);
                started.add(background.submit(() -> finish(run)));
            }
            return run;
        }

        /** Records a run and waits for it to finish. Schedules and tests use it. */
        public Run runNow() {
            return finish(begin());
        }

        /** Blocks until runs started by {@link #start()} have finished; tests use it. */
        public void await() {
            List<Future<?>> pending;
            synchronized (started) {
                pending = new ArrayList<>(started);
            }
            for (Future<?> f : pending) {
                try {
                    f.get(30, TimeUnit.SECONDS);
                } catch (Exception e) {
                    throw new IllegalStateException(e);
                }
            }
        }

        private Run begin() {
            Instant now = clock.instant();
            Run run = new Run(newRunId(), RUN_RUNNING, now, null, 0, 0, 0, "");
            runs.start(run, now.minus(STALE_RUN_AFTER));
            return run;
        }

        private Run finish(Run run) {
            int feedbackRead = 0;
            int eventsRead = 0;
            int made = 0;
            String error = "";
            try {
                synchronized (lock) {
                    Result r = analyze();
                    feedbackRead = r.feedbackRead;
                    eventsRead = r.eventsRead;
                    made = r.made;
                }
            } catch (RuntimeException e) {
                error = redact(e);
                LOG.warn("learning run failed run={} error={}", run.id(), e.getMessage());
            }
            Run done = new Run(run.id(), error.isEmpty() ? RUN_DONE : RUN_FAILED, run.startedAt(), clock.instant(), feedbackRead,
                    eventsRead, made, error);
            try {
                runs.finish(done);
            } catch (RuntimeException e) {
                LOG.warn("recording the learning run failed run={} error={}", run.id(), e.getMessage());
            }
            return done;
        }

        private record Result(int feedbackRead, int eventsRead, int made) {}

        /** Evidence for one source and event type. */
        private static final class Scope {
            final String source;
            final String eventType;
            final Map<String, Voter> voters = new TreeMap<>();
            int positive;
            int negative;

            Scope(String source, String eventType) {
                this.source = source;
                this.eventType = eventType;
            }
        }

        /** What one person did in one scope. */
        private static final class Voter {
            double pos;
            double neg;
            int n;
        }

        private Result analyze() {
            Instant now = clock.instant();
            List<Feedback.Entry> signals = feedback.list(new Feedback.Filter("", "", now.minus(cfg.lookback()), Learning.MAX_FEEDBACK_READ));
            List<Event> recent = events.get();
            List<Rule> ruleList = rules.list();
            Map<String, Adjustment> byScope = new HashMap<>();
            for (Adjustment a : adjustments.list("")) {
                byScope.put(scopeKey(a.source(), a.eventType(), a.repository()), a);
            }
            Map<String, Event> byId = new HashMap<>();
            for (Event e : recent) {
                byId.putIfAbsent(e.getId(), e);
            }
            java.util.Set<String> protectedByName = new java.util.HashSet<>();
            for (Rule r : ruleList) {
                if (isProtected(r)) {
                    protectedByName.add(r.name());
                }
            }

            Map<String, Scope> scopes = new TreeMap<>();
            for (Feedback.Entry f : signals) {
                boolean positive = f.action().equals(Feedback.OPENED) || f.action().equals(Feedback.REPLY) || f.action().equals(Feedback.ALWAYS);
                boolean negative = f.action().equals(Feedback.DISMISSED) || f.action().equals(Feedback.NEVER);
                if (!positive && !negative) {
                    continue;
                }
                Event evt = byId.get(f.eventId());
                if (evt == null || !learnable(evt)) {
                    continue; // gone from the recent window, critical, or not delivered: never learned from
                }
                if (evt.getDecisionState() != null && protectedByName.contains(evt.getDecisionState().getMatchedRule())) {
                    continue;
                }
                String key = scopeKey(f.source(), f.eventType(), "");
                Adjustment prior = byScope.get(key);
                if (prior != null && RETIRED.equals(prior.status()) && prior.resolvedAt() != null && !f.createdAt().isAfter(prior.resolvedAt())) {
                    continue; // describes the behaviour before it was retired
                }
                Scope sc = scopes.computeIfAbsent(key, k -> new Scope(f.source(), f.eventType()));
                Voter v = sc.voters.computeIfAbsent(f.userId() == null ? "" : f.userId(), k -> new Voter());
                double w = weight(Duration.between(f.createdAt(), now), cfg.halfLife());
                v.n++;
                if (positive) {
                    v.pos += w;
                    sc.positive++;
                } else {
                    v.neg += w;
                    sc.negative++;
                }
            }

            int made = 0;
            for (Map.Entry<String, Scope> entry : scopes.entrySet()) {
                Scope sc = entry.getValue();
                Adjustment prior = byScope.get(entry.getKey());
                if (prior != null && (ACTIVE.equals(prior.status()) || REJECTED.equals(prior.status()))) {
                    continue; // an admin already decided; a run never overrides that
                }
                if (coveredByProtectedRule(ruleList, sc.source, sc.eventType)) {
                    continue;
                }
                Adjustment adj = propose(sc, recent, now);
                if (adj == null) {
                    continue;
                }
                if (prior != null) {
                    adj = new Adjustment(adj.id(), adj.source(), adj.eventType(), adj.repository(), adj.delta(), adj.status(), adj.evidence(),
                            adj.impact(), prior.version(), prior.createdAt(), adj.updatedAt(), "", null);
                }
                adjustments.save(adj);
                made++;
            }
            return new Result(signals.size(), recent.size(), made);
        }

        /** Applies the rule to one scope's evidence; null when there is not enough, or the offset is zero. */
        private Adjustment propose(Scope sc, List<Event> recent, Instant now) {
            // Sums are taken in a fixed order (by person) and rounded to six decimals, so the result does not depend
            // on map order, and the Go service, which does the same, gets the very same numbers.
            double p = 0;
            double n = 0;
            int signals = 0;
            for (Voter v : sc.voters.values()) {
                p += Math.min(round6(v.pos), PER_PERSON_CAP);
                n += Math.min(round6(v.neg), PER_PERSON_CAP);
                signals += v.n;
            }
            p = round6(p);
            n = round6(n);
            int people = sc.voters.size();
            if (signals < cfg.minSignals() || people < cfg.minPeople()) {
                return null;
            }
            int delta = offset(p, n, cfg.maxAdjustment());
            if (delta == 0) {
                return null;
            }
            int count = delta > 0 ? sc.positive : sc.negative;
            String word = delta > 0 ? "positive" : "negative";
            Evidence evidence = new Evidence(sc.positive, sc.negative, people, signals, (int) cfg.lookback().toDays(),
                    count + " of " + signals + " signals from " + people2(people) + " were " + word);
            Adjustment adj = new Adjustment(adjustmentId(sc.source, sc.eventType, ""), sc.source, sc.eventType, "", delta, PROPOSED, evidence,
                    new Impact(0, 0), 0, now, now, "", null);
            return new Adjustment(adj.id(), adj.source(), adj.eventType(), adj.repository(), adj.delta(), adj.status(), adj.evidence(),
                    impact(adj, recent), 0, now, now, "", null);
        }

        /** Makes a proposed adjustment active. Re-checks the guardrail against the rules as they are now. */
        public Adjustment approve(String id, String actor) {
            synchronized (lock) {
                Adjustment a = adjustments.get(id);
                if (!PROPOSED.equals(a.status())) {
                    throw new NotProposedException();
                }
                List<Rule> ruleList = rules.list();
                if (coveredByProtectedRule(ruleList, a.source(), a.eventType()) || a.delta() == 0 || Math.abs(a.delta()) > MAX_DELTA) {
                    throw new GuardrailException();
                }
                Adjustment done = a.resolved(ACTIVE, actor, clock.instant(), adjustments.maxVersion() + 1);
                adjustments.save(done);
                refreshOrWarn(ruleList);
                return done;
            }
        }

        /** Refuses a proposed adjustment; it is not proposed again. */
        public Adjustment reject(String id, String actor) {
            synchronized (lock) {
                Adjustment a = adjustments.get(id);
                if (!PROPOSED.equals(a.status())) {
                    throw new NotProposedException();
                }
                Adjustment done = a.resolved(REJECTED, actor, clock.instant(), a.version());
                adjustments.save(done);
                return done;
            }
        }

        /** Switches an active adjustment off; scores return to what they were without it. */
        public Adjustment retire(String id, String actor) {
            synchronized (lock) {
                Adjustment a = adjustments.get(id);
                if (!ACTIVE.equals(a.status())) {
                    throw new NotActiveException();
                }
                Adjustment done = a.resolved(RETIRED, actor, clock.instant(), adjustments.maxVersion() + 1);
                adjustments.save(done);
                try {
                    refreshOrWarn(rules.list());
                } catch (RuntimeException e) {
                    // Retiring must always succeed in the store; the snapshot catches up on its next refresh.
                    LOG.warn("reading rules to refresh the learned adjustments failed error={}", e.getMessage());
                }
                return done;
            }
        }

        /** Reloads the in-memory set of approved adjustments the heuristic reads. */
        public void refreshSnapshot() {
            refresh(rules.list());
        }

        private void refreshOrWarn(List<Rule> ruleList) {
            try {
                refresh(ruleList);
            } catch (RuntimeException e) {
                LOG.warn("refreshing the learned adjustments failed; the next refresh will catch up error={}", e.getMessage());
            }
        }

        /** An adjustment whose scope a critical or locked rule now covers is left out. */
        private void refresh(List<Rule> ruleList) {
            if (snapshot == null) {
                return;
            }
            List<Applied> set = new ArrayList<>();
            for (Adjustment a : adjustments.list(ACTIVE)) {
                if (!coveredByProtectedRule(ruleList, a.source(), a.eventType())) {
                    set.add(new Applied(a.id(), a.source(), a.eventType(), a.repository(), a.delta(), a.evidence().summary()));
                }
            }
            snapshot.set(adjustments.maxVersion(), set);
        }

        private String newRunId() {
            byte[] b = new byte[12];
            random.nextBytes(b);
            return HexFormat.of().formatHex(b);
        }
    }

    // ---- the rule, and the guardrail -----------------------------------------------------------------------

    /** The whole rule: max * (P - N) / (P + N + 2), rounded half away from zero and clamped. */
    static int offset(double p, double n, int max) {
        int d = (int) roundHalfAway((double) max * (p - n) / (p + n + SMOOTHING));
        return Math.max(-max, Math.min(max, d));
    }

    /** A signal halves every half-life; kept to six decimals so two implementations agree. */
    static double weight(Duration age, Duration halfLife) {
        if (age.isNegative()) {
            age = Duration.ZERO;
        }
        return round6(StrictMath.pow(0.5, (double) age.toNanos() / (double) halfLife.toNanos()));
    }

    static double round6(double x) {
        return roundHalfAway(x * 1e6) / 1e6;
    }

    /** Go's math.Round: halves go away from zero. */
    static double roundHalfAway(double v) {
        double r = Math.floor(Math.abs(v));
        if (Math.abs(v) - r >= 0.5) {
            r += 1;
        }
        return Math.copySign(r, v);
    }

    /**
     * Whether an event's feedback may be learned from: a non-critical event that was actually delivered (queued or
     * sent). Events held for a digest, merged, delayed or over budget are left out, so a demoted event that can no
     * longer be opened does not look worse and worse.
     */
    static boolean learnable(Event e) {
        if (!Attention.STANDARD.equals(e.getAttentionClass()) && !Attention.BACKGROUND.equals(e.getAttentionClass())) {
            return false;
        }
        if (e.getDecisionState() == null) {
            return false;
        }
        String status = e.getDecisionState().getStatus();
        return Status.QUEUED.equals(status) || Status.SENT.equals(status);
    }

    /** A critical rule, or any locked rule, is never tuned around. */
    static boolean isProtected(Rule r) {
        return r.locked() || Attention.CRITICAL.equals(r.then().attentionClass());
    }

    /** Whether a protected rule applies to the source and event type (a repository restriction is ignored, to err towards protection). */
    static boolean coveredByProtectedRule(List<Rule> rules, String source, String eventType) {
        for (Rule r : rules) {
            if (isProtected(r) && Glob.matches(r.when().source(), source) && Glob.matches(r.when().eventType(), eventType)) {
                return true;
            }
        }
        return false;
    }

    /** Recent standard events of the scope replayed through the heuristic with and without the adjustment. */
    static Impact impact(Adjustment adj, List<Event> recent) {
        Snapshot with = new Snapshot(Duration.ofHours(1), Clock.systemUTC());
        with.set(1, List.of(new Applied(adj.id(), adj.source(), adj.eventType(), adj.repository(), adj.delta(), adj.evidence().summary())));
        Heuristic before = new Heuristic();
        Heuristic after = new Heuristic().withTuning(with);
        int replayed = 0;
        int wouldChange = 0;
        for (Event e : recent) {
            if (!Attention.STANDARD.equals(e.getAttentionClass()) || !e.getSource().equalsIgnoreCase(adj.source())
                    || !e.getEventType().equalsIgnoreCase(adj.eventType())) {
                continue;
            }
            Classification a = before.classify(e);
            Classification b = after.classify(e);
            replayed++;
            if (!a.attentionClass().equals(b.attentionClass())) {
                wouldChange++;
            }
        }
        return new Impact(replayed, wouldChange);
    }

    /** The key of an adjustment's scope: lower-cased source, event type and repository. */
    static String scopeKey(String source, String eventType, String repository) {
        return source.toLowerCase(Locale.ROOT) + SEP + eventType.toLowerCase(Locale.ROOT) + SEP + repository.toLowerCase(Locale.ROOT);
    }

    /** The first 8 bytes of sha256 over the scope key, as hex. The Go service computes the same. */
    public static String adjustmentId(String source, String eventType, String repository) {
        try {
            byte[] sum = MessageDigest.getInstance("SHA-256").digest(scopeKey(source, eventType, repository).getBytes(StandardCharsets.UTF_8));
            return HexFormat.of().formatHex(sum, 0, 8);
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    private static String people2(int n) {
        return n == 1 ? "1 person" : n + " people";
    }

    /** Keeps an error short and drops any word that looks like an address with credentials (anything containing "://"). */
    static String redact(RuntimeException e) {
        String message = e.getMessage() == null ? e.getClass().getSimpleName() : e.getMessage();
        StringBuilder kept = new StringBuilder();
        for (String word : message.trim().split("\\s+")) {
            if (!word.contains("://")) {
                if (kept.length() > 0) {
                    kept.append(' ');
                }
                kept.append(word);
            }
        }
        String out = kept.toString();
        return out.length() > 200 ? out.substring(0, 200) : (out.isEmpty() ? "run failed" : out);
    }

    // ---- JSON shape (the Go service's) ---------------------------------------------------------------------

    /** The adjustment as the Go service writes it: an empty repository and resolution are left out. */
    public static Map<String, Object> toMap(Adjustment a) {
        Map<String, Object> m = new java.util.LinkedHashMap<>();
        m.put("id", a.id());
        m.put("source", a.source());
        m.put("eventType", a.eventType());
        if (!a.repository().isEmpty()) {
            m.put("repository", a.repository());
        }
        m.put("delta", a.delta());
        m.put("status", a.status());
        Map<String, Object> ev = new java.util.LinkedHashMap<>();
        ev.put("positive", a.evidence().positive());
        ev.put("negative", a.evidence().negative());
        ev.put("people", a.evidence().people());
        ev.put("signals", a.evidence().signals());
        ev.put("windowDays", a.evidence().windowDays());
        ev.put("summary", a.evidence().summary());
        m.put("evidence", ev);
        Map<String, Object> im = new java.util.LinkedHashMap<>();
        im.put("replayed", a.impact().replayed());
        im.put("wouldChange", a.impact().wouldChange());
        m.put("impact", im);
        m.put("version", a.version());
        m.put("createdAt", a.createdAt().toString());
        m.put("updatedAt", a.updatedAt().toString());
        if (!a.resolvedBy().isEmpty()) {
            m.put("resolvedBy", a.resolvedBy());
        }
        if (a.resolvedAt() != null) {
            m.put("resolvedAt", a.resolvedAt().toString());
        }
        return m;
    }

    /** The run as the Go service writes it. */
    public static Map<String, Object> toMap(Run r) {
        Map<String, Object> m = new java.util.LinkedHashMap<>();
        m.put("id", r.id());
        m.put("status", r.status());
        m.put("startedAt", r.startedAt().toString());
        if (r.finishedAt() != null) {
            m.put("finishedAt", r.finishedAt().toString());
        }
        m.put("feedbackRead", r.feedbackRead());
        m.put("eventsRead", r.eventsRead());
        m.put("proposalsMade", r.proposalsMade());
        if (!r.error().isEmpty()) {
            m.put("error", r.error());
        }
        return m;
    }

    // ---- background work -----------------------------------------------------------------------------------

    /**
     * Keeps the snapshot fresh every {@link #SNAPSHOT_REFRESH} and, when {@code trainEvery} is positive, makes a run
     * on that interval. With a zero interval no run is scheduled: runs happen only when an admin starts one.
     */
    public static final class Background {
        private static final Logger LOG = LoggerFactory.getLogger(Background.class);

        private final Tuner tuner;
        private final Duration trainEvery;
        private ScheduledExecutorService executor;

        public Background(Tuner tuner, Duration trainEvery) {
            this.tuner = tuner;
            this.trainEvery = trainEvery;
        }

        public synchronized void start() {
            if (executor != null) {
                return;
            }
            executor = Executors.newScheduledThreadPool(2, r -> {
                Thread t = new Thread(r, "learning-background");
                t.setDaemon(true);
                return t;
            });
            executor.scheduleWithFixedDelay(this::refresh, SNAPSHOT_REFRESH.toMillis(), SNAPSHOT_REFRESH.toMillis(), TimeUnit.MILLISECONDS);
            if (!trainEvery.isZero() && !trainEvery.isNegative()) {
                executor.scheduleWithFixedDelay(this::train, trainEvery.toMillis(), trainEvery.toMillis(), TimeUnit.MILLISECONDS);
            }
        }

        void refresh() {
            try {
                tuner.refreshSnapshot();
            } catch (RuntimeException e) {
                LOG.warn("refreshing the learned adjustments failed; using the last good set until it goes stale error={}", e.getMessage());
            }
        }

        void train() {
            try {
                Run run = tuner.runNow();
                LOG.info("learning run finished run={} status={} proposals={}", run.id(), run.status(), run.proposalsMade());
            } catch (RunInProgressException e) {
                LOG.info("scheduled learning run skipped reason={}", e.getMessage());
            } catch (RuntimeException e) {
                LOG.error("scheduled learning run failed error={}", e.getMessage());
            }
        }

        public synchronized void stop() {
            if (executor != null) {
                executor.shutdownNow();
                executor = null;
            }
        }
    }
}
