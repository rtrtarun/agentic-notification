package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.TreeMap;
import java.util.function.Supplier;

/**
 * Learning: turns stored feedback into suggestions a person can review. It is the bridge from Layer 4 (feedback)
 * back to Layers 1 and 2: it reads what people dismissed, muted, opened, replied to and always-notified, and
 * proposes small, reversible changes to rules or overrides. It never changes anything on its own; an admin
 * applies or dismisses each suggestion, and applying is audited. A port of the Go service's {@code pkg/learning},
 * kept the same by the shared contract file {@code 14-learning-suggestions.json}.
 *
 * <p>Guardrail, enforced twice (when a suggestion is made and again when it is applied): feedback about a
 * critical event is ignored, and a critical rule, a locked rule or a disabled rule is never the subject of a
 * suggestion and can never be changed by one. Learning is a background analysis over stored feedback; it is not a
 * step in the live event path.
 */
public final class Learning {
    public static final String KIND_ADAPTIVE = "adaptive";
    public static final String KIND_DEMOTE = "demote";
    public static final String KIND_PROMOTE = "promote";
    public static final String KIND_OVERRIDE = "override";

    public static final String PENDING = "pending";
    public static final String APPLIED = "applied";
    public static final String DISMISSED = "dismissed";

    public static final int DEFAULT_MIN_SIGNALS = 5;
    public static final int DEFAULT_MIN_ALWAYS = 2;
    public static final Duration DEFAULT_LOOKBACK = Duration.ofDays(30);
    /** Bounds one analysis. */
    static final int MAX_FEEDBACK_READ = 5000;

    private static final String SEP = "\u0000";

    private Learning() {}

    /** No suggestion has this id. */
    public static final class NotFoundException extends RuntimeException {
        public NotFoundException() {
            super("suggestion not found");
        }
    }

    /** The suggestion was already applied or dismissed. */
    public static final class NotPendingException extends RuntimeException {
        public NotPendingException() {
            super("suggestion is no longer pending");
        }
    }

    /** Applying would touch a critical or locked rule. */
    public static final class GuardrailException extends RuntimeException {
        public GuardrailException() {
            super("critical and locked rules can never be changed by a suggestion");
        }
    }

    /** The rule the suggestion is about no longer exists. */
    public static final class RuleGoneException extends RuntimeException {
        public RuleGoneException() {
            super("the rule this suggestion is about no longer exists");
        }
    }

    /** The counts of signals behind a suggestion. */
    public record Evidence(int dismissed, int never, int opened, int reply, int always) {
        /** Signals that say "this was not worth it". */
        public int negative() {
            return dismissed + never;
        }

        /** Signals that say "this mattered". */
        public int positive() {
            return opened + reply + always;
        }
    }

    /** One reviewable proposal. */
    public record Suggestion(String id, String kind, String status, String source, String eventType, String repository,
                             String ruleId, String ruleName, String summary, String proposed, Evidence evidence,
                             Instant createdAt, Instant updatedAt, String resolvedBy, Instant resolvedAt) {
        public Suggestion withTimes(Instant created, Instant updated) {
            return new Suggestion(id, kind, status, source, eventType, repository, ruleId, ruleName, summary, proposed, evidence,
                    created, updated, resolvedBy, resolvedAt);
        }

        Suggestion resolved(String newStatus, String actor, Instant at) {
            return new Suggestion(id, kind, newStatus, source, eventType, repository, ruleId, ruleName, summary, proposed, evidence,
                    createdAt, at, actor, at);
        }
    }

    /** Keeps suggestions. Implementations: in memory, and PostgreSQL. */
    public interface Repository {
        /** Inserts or replaces a suggestion by id. */
        void save(Suggestion suggestion);

        /** @throws NotFoundException when there is none */
        Suggestion get(String id);

        /** Suggestions with the status (all when empty), newest update first. */
        List<Suggestion> list(String status);
    }

    /** Tunes how much evidence a suggestion needs. */
    public record Config(int minSignals, int minAlways, Duration lookback) {
        public Config {
            minSignals = minSignals <= 0 ? DEFAULT_MIN_SIGNALS : minSignals;
            minAlways = minAlways <= 0 ? DEFAULT_MIN_ALWAYS : minAlways;
            lookback = lookback == null || lookback.isZero() || lookback.isNegative() ? DEFAULT_LOOKBACK : lookback;
        }
    }

    /** Reads stored feedback. {@link Feedback.Store} satisfies it. */
    public interface FeedbackReader {
        List<Feedback.Entry> list(Feedback.Filter filter);
    }

    /** Reads and edits rules. {@link RulesService} satisfies it. */
    public interface RuleEditor {
        List<Rule> list();

        Rule update(String id, Rule rule);
    }

    /** Creates overrides. {@link Overrides.Service} satisfies it. */
    public interface OverrideWriter {
        Overrides.Override upsert(Overrides.Override override);
    }

    /** Analyses feedback and applies reviewed suggestions. One analysis or resolution at a time. */
    public static final class Service {
        private final Repository repo;
        private final FeedbackReader feedback;
        private final Supplier<List<Event>> events;
        private final RuleEditor rules;
        private final OverrideWriter overrides;
        private final Config cfg;
        private final Clock clock;

        public Service(Repository repo, FeedbackReader feedback, Supplier<List<Event>> events, RuleEditor rules,
                       OverrideWriter overrides, Config cfg, Clock clock) {
            this.repo = repo;
            this.feedback = feedback;
            this.events = events;
            this.rules = rules;
            this.overrides = overrides;
            this.cfg = cfg;
            this.clock = clock;
        }

        /** The evidence threshold in use, for the screen to show. */
        public int minSignals() {
            return cfg.minSignals();
        }

        /** Refreshes suggestions from the latest feedback and returns the pending ones. */
        public List<Suggestion> review() {
            analyze();
            return repo.list(PENDING);
        }

        /**
         * Reads recent feedback and stores a pending suggestion wherever the evidence is clear. A suggestion that
         * was already applied or dismissed is not made again.
         */
        public synchronized void analyze() {
            Instant since = clock.instant().minus(cfg.lookback());
            List<Feedback.Entry> signals = feedback.list(new Feedback.Filter("", "", since, MAX_FEEDBACK_READ));
            if (signals.isEmpty()) {
                return;
            }
            Map<String, Event> byId = new HashMap<>();
            for (Event e : events.get()) {
                byId.putIfAbsent(e.getId(), e);
            }
            List<Rule> ruleList = rules.list();

            // Feedback from before a suggestion was applied describes the old behaviour, so it must not argue for
            // the next change to the same thing.
            Map<String, Instant> appliedAt = new HashMap<>();
            for (Suggestion a : repo.list(APPLIED)) {
                if (a.resolvedAt() != null) {
                    appliedAt.merge(scopeKey(a.source(), a.eventType(), a.ruleName()), a.resolvedAt(),
                            (x, y) -> y.isAfter(x) ? y : x);
                }
            }

            Map<String, Group> groups = new TreeMap<>();
            for (Feedback.Entry f : signals) {
                Event evt = byId.get(f.eventId());
                if (evt == null || Attention.CRITICAL.equals(evt.getAttentionClass())) {
                    continue; // gone from the recent window, or critical: never learned from
                }
                if (!Attention.STANDARD.equals(evt.getAttentionClass()) && !Attention.BACKGROUND.equals(evt.getAttentionClass())) {
                    continue;
                }
                String rule = evt.getDecisionState() == null ? "" : evt.getDecisionState().getMatchedRule();
                Instant cutoff = appliedAt.get(scopeKey(f.source(), f.eventType(), rule));
                if (cutoff != null && !f.createdAt().isAfter(cutoff)) {
                    continue;
                }
                String key = String.join(SEP, f.source(), f.eventType(), rule, evt.getAttentionClass());
                Group g = groups.computeIfAbsent(key, k -> new Group(f.source(), f.eventType(), rule, evt.getAttentionClass()));
                if (!f.repository().isEmpty()) {
                    g.repositories.add(f.repository());
                }
                switch (f.action()) {
                    case Feedback.DISMISSED -> g.dismissed++;
                    case Feedback.NEVER -> g.never++;
                    case Feedback.OPENED -> g.opened++;
                    case Feedback.REPLY -> g.reply++;
                    case Feedback.ALWAYS -> g.always++;
                    default -> { }
                }
            }
            for (Group g : groups.values()) {
                Optional<Suggestion> s = suggest(g, ruleList);
                if (s.isPresent()) {
                    store(s.get());
                }
            }
        }

        /** Makes the change a pending suggestion proposes and marks it applied. Re-checks the guardrail now. */
        public synchronized Suggestion apply(String id, String actor) {
            Suggestion sug = pending(id);
            switch (sug.kind()) {
                case KIND_ADAPTIVE, KIND_DEMOTE, KIND_PROMOTE -> changeRule(sug);
                case KIND_OVERRIDE -> {
                    String mode = sug.evidence().always() > 0 && sug.evidence().negative() == 0 ? Overrides.ALWAYS : Overrides.NEVER;
                    overrides.upsert(new Overrides.Override("", sug.source(), sug.eventType(), sug.repository(), mode, actor,
                            "from a learning suggestion", null));
                }
                default -> throw new IllegalStateException("unknown suggestion kind \"" + sug.kind() + "\"");
            }
            return resolve(sug, APPLIED, actor);
        }

        /** Marks a pending suggestion as not wanted; it is not made again. */
        public synchronized Suggestion dismiss(String id, String actor) {
            return resolve(pending(id), DISMISSED, actor);
        }

        private Suggestion pending(String id) {
            Suggestion s = repo.get(id);
            if (!PENDING.equals(s.status())) {
                throw new NotPendingException();
            }
            return s;
        }

        private Suggestion resolve(Suggestion sug, String status, String actor) {
            Suggestion done = sug.resolved(status, actor, clock.instant());
            repo.save(done);
            return done;
        }

        /** Decides whether a group of feedback justifies a suggestion. */
        private Optional<Suggestion> suggest(Group g, List<Rule> ruleList) {
            Optional<Rule> found = findByName(ruleList, g.ruleName);
            if (found.isPresent() && !changeable(found.get())) {
                return Optional.empty(); // locked or critical: never the subject of a suggestion
            }
            String repo = g.repositories.size() == 1 ? g.repositories.iterator().next() : "";
            Evidence e = new Evidence(g.dismissed, g.never, g.opened, g.reply, g.always);
            String what = describe(g.source, g.eventType);
            String kind;
            String summary;
            String proposed;
            String ruleId = "";
            String ruleName = "";
            if (Attention.STANDARD.equals(g.cls) && e.negative() >= cfg.minSignals() && e.positive() == 0) {
                summary = "People dismissed or muted " + what + " " + e.negative() + " times and never opened or replied to one.";
                if (found.isPresent() && Attention.STANDARD.equals(found.get().then().attentionClass()) && !found.get().then().adaptive()) {
                    kind = KIND_ADAPTIVE;
                    ruleId = found.get().id();
                    ruleName = found.get().name();
                    proposed = "Let Attention refine the rule \"" + ruleName + "\", so quiet events like this can move to the digest.";
                } else if (found.isPresent() && Attention.STANDARD.equals(found.get().then().attentionClass())) {
                    kind = KIND_DEMOTE;
                    ruleId = found.get().id();
                    ruleName = found.get().name();
                    proposed = "Change the rule \"" + ruleName + "\" from standard to background.";
                } else {
                    kind = KIND_OVERRIDE;
                    proposed = "Add a \"never notify\" override for " + what + inRepo(repo) + ".";
                }
            } else if (Attention.BACKGROUND.equals(g.cls) && e.always() >= cfg.minAlways() && e.negative() == 0) {
                summary = "People asked to always be told about " + what + " " + e.always() + " times.";
                if (found.isPresent() && Attention.BACKGROUND.equals(found.get().then().attentionClass())) {
                    kind = KIND_PROMOTE;
                    ruleId = found.get().id();
                    ruleName = found.get().name();
                    proposed = "Change the rule \"" + ruleName + "\" from background to standard.";
                } else {
                    kind = KIND_OVERRIDE;
                    proposed = "Add an \"always notify\" override for " + what + inRepo(repo) + ".";
                }
            } else {
                return Optional.empty();
            }
            String id = suggestionId(kind, g.source, g.eventType, repo, ruleName);
            return Optional.of(new Suggestion(id, kind, PENDING, g.source, g.eventType, repo, ruleId, ruleName, summary, proposed, e,
                    null, null, "", null));
        }

        /** Saves a new suggestion, refreshes the evidence of a pending one, and leaves an applied or dismissed one alone. */
        private void store(Suggestion sug) {
            Instant now = clock.instant();
            Instant created = now;
            try {
                Suggestion existing = repo.get(sug.id());
                if (!PENDING.equals(existing.status())) {
                    return;
                }
                created = existing.createdAt();
            } catch (NotFoundException ignored) {
                // a new suggestion
            }
            repo.save(sug.withTimes(created, now));
        }

        private void changeRule(Suggestion sug) {
            Rule rule = rules.list().stream().filter(r -> r.id().equals(sug.ruleId())).findFirst().orElseThrow(RuleGoneException::new);
            if (!changeable(rule)) {
                throw new GuardrailException();
            }
            Rule.Outcome then = rule.then();
            Rule.Outcome changed = switch (sug.kind()) {
                case KIND_ADAPTIVE -> new Rule.Outcome(then.attentionClass(), then.reason(), true, then.channels());
                case KIND_DEMOTE -> new Rule.Outcome(Attention.BACKGROUND, then.reason(), then.adaptive(), then.channels());
                default -> new Rule.Outcome(Attention.STANDARD, then.reason(), then.adaptive(), then.channels());
            };
            if (Attention.CRITICAL.equals(changed.attentionClass())) {
                throw new GuardrailException(); // belt and braces: no path here may produce a critical rule
            }
            rules.update(rule.id(), new Rule(rule.id(), rule.name(), rule.description(), rule.when(), changed, rule.disabled(), rule.locked()));
        }
    }

    /** The guardrail: a rule that is locked, disabled or critical can never be the subject of a suggestion. */
    static boolean changeable(Rule r) {
        return !r.locked() && !r.disabled() && !Attention.CRITICAL.equals(r.then().attentionClass());
    }

    private static Optional<Rule> findByName(List<Rule> rules, String name) {
        if (name.isEmpty()) {
            return Optional.empty();
        }
        return rules.stream().filter(r -> r.name().equals(name)).findFirst();
    }

    /** The feedback about one kind of event handled by one rule. */
    private static final class Group {
        final String source;
        final String eventType;
        final String ruleName;
        final String cls;
        final Set<String> repositories = new LinkedHashSet<>();
        int dismissed;
        int never;
        int opened;
        int reply;
        int always;

        Group(String source, String eventType, String ruleName, String cls) {
            this.source = source;
            this.eventType = eventType;
            this.ruleName = ruleName;
            this.cls = cls;
        }
    }

    /** The id is a hash of what the suggestion is about, so the same suggestion is never made twice. */
    static String suggestionId(String kind, String source, String eventType, String repository, String ruleName) {
        try {
            byte[] sum = MessageDigest.getInstance("SHA-256")
                    .digest(String.join(SEP, kind, source, eventType, repository, ruleName).getBytes(StandardCharsets.UTF_8));
            return HexFormat.of().formatHex(sum, 0, 8);
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    private static String describe(String source, String eventType) {
        if (!source.isEmpty() && !eventType.isEmpty()) {
            return source + " " + eventType + " notifications";
        }
        if (!source.isEmpty()) {
            return source + " notifications";
        }
        return "these notifications";
    }

    private static String inRepo(String repo) {
        return repo.isEmpty() ? "" : " in " + repo;
    }

    private static String scopeKey(String source, String eventType, String rule) {
        return String.join(SEP, source, eventType, rule);
    }

    // ---- JSON shape ----------------------------------------------------------------------------------------

    /** The suggestion as the Go service writes it: empty rule, repository and resolution fields are left out. */
    public static Map<String, Object> toMap(Suggestion s) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", s.id());
        m.put("kind", s.kind());
        m.put("status", s.status());
        m.put("source", s.source());
        m.put("eventType", s.eventType());
        if (!s.repository().isEmpty()) {
            m.put("repository", s.repository());
        }
        if (!s.ruleId().isEmpty()) {
            m.put("ruleId", s.ruleId());
        }
        if (!s.ruleName().isEmpty()) {
            m.put("ruleName", s.ruleName());
        }
        m.put("summary", s.summary());
        m.put("proposed", s.proposed());
        Map<String, Object> ev = new LinkedHashMap<>();
        ev.put("dismissed", s.evidence().dismissed());
        ev.put("never", s.evidence().never());
        ev.put("opened", s.evidence().opened());
        ev.put("reply", s.evidence().reply());
        ev.put("always", s.evidence().always());
        m.put("evidence", ev);
        m.put("createdAt", s.createdAt().toString());
        m.put("updatedAt", s.updatedAt().toString());
        if (!s.resolvedBy().isEmpty()) {
            m.put("resolvedBy", s.resolvedBy());
        }
        if (s.resolvedAt() != null) {
            m.put("resolvedAt", s.resolvedAt().toString());
        }
        return m;
    }

    /** Reads a suggestion written by either service. */
    @SuppressWarnings("unchecked")
    public static Suggestion fromMap(Map<String, Object> m) {
        Map<String, Object> ev = m.get("evidence") instanceof Map<?, ?> e ? (Map<String, Object>) e : Map.of();
        String resolvedAt = text(m, "resolvedAt");
        return new Suggestion(text(m, "id"), text(m, "kind"), text(m, "status"), text(m, "source"), text(m, "eventType"),
                text(m, "repository"), text(m, "ruleId"), text(m, "ruleName"), text(m, "summary"), text(m, "proposed"),
                new Evidence(number(ev, "dismissed"), number(ev, "never"), number(ev, "opened"), number(ev, "reply"), number(ev, "always")),
                Instant.parse(text(m, "createdAt")), Instant.parse(text(m, "updatedAt")), text(m, "resolvedBy"),
                resolvedAt.isEmpty() ? null : Instant.parse(resolvedAt));
    }

    private static String text(Map<String, Object> m, String key) {
        return m.get(key) instanceof String s ? s : "";
    }

    private static int number(Map<String, Object> m, String key) {
        return m.get(key) instanceof Number n ? n.intValue() : 0;
    }

    /** Adapts the services the engine edits through, so the engine depends only on small interfaces. */
    public static RuleEditor ruleEditor(RulesService rules) {
        return new RuleEditor() {
            @Override
            public List<Rule> list() {
                return rules.list();
            }

            @Override
            public Rule update(String id, Rule rule) {
                try {
                    return rules.update(id, rule);
                } catch (RulesService.NotFoundException e) {
                    throw new RuleGoneException();
                } catch (RulesService.LockedException e) {
                    throw new GuardrailException();
                }
            }
        };
    }
}
