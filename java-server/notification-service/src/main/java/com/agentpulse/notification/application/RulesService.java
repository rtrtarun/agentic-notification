package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.RuleStore;
import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Predicate;
import com.agentpulse.notification.domain.Rule;

import java.security.SecureRandom;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.function.Supplier;

/**
 * Lets people manage the Layer 1 rules while the service runs: list, create, edit, delete, reorder and dry-run
 * them. It owns validation and persistence; evaluation stays in {@link PolicyEngine}, and AI is not involved.
 *
 * <p>Locked rules protect critical routing: they cannot be edited, disabled or removed, and they always run before
 * unlocked rules, so a broad rule added later can never capture a security event ahead of them.
 */
public final class RulesService {
    public static final int MAX_RULES = 100;
    private static final int MAX_NAME = 80;
    private static final int MAX_DESCRIPTION = 300;
    private static final int MAX_REASON = 200;
    private static final int MAX_PATTERN = 100;
    private static final SecureRandom RANDOM = new SecureRandom();

    /** No rule has this id. */
    public static final class NotFoundException extends RuntimeException {
        public NotFoundException() {
            super("rule not found");
        }
    }

    /** The rule is locked. */
    public static final class LockedException extends RuntimeException {
        public LockedException() {
            super("this rule is locked: it protects critical routing");
        }
    }

    /** The rule or the request is not valid. */
    public static final class InvalidException extends RuntimeException {
        public InvalidException(String detail) {
            super("invalid rule: " + detail);
        }
    }

    private final RuleStore store;
    private final PolicyEngine engine;
    private final Supplier<String> newId;

    /**
     * Loads the stored rules, seeds them from {@code seed} when the store is empty, adds any locked seed rule that is
     * missing, and hands the result to the engine.
     */
    public RulesService(RuleStore store, PolicyEngine engine, List<Rule> seed) {
        this(store, engine, seed, RulesService::randomId);
    }

    RulesService(RuleStore store, PolicyEngine engine, List<Rule> seed, Supplier<String> newId) {
        this.store = store;
        this.engine = engine;
        this.newId = newId;
        synchronized (this) {
            List<Rule> stored = store.list();
            List<Rule> merged = mergeSeed(stored, seed);
            if (!sameOrder(merged, stored)) {
                store.replace(merged);
            }
            engine.setRules(merged);
        }
    }

    private static String randomId() {
        byte[] b = new byte[8];
        RANDOM.nextBytes(b);
        return HexFormat.of().formatHex(b);
    }

    private List<Rule> mergeSeed(List<Rule> stored, List<Rule> seed) {
        List<Rule> list = new ArrayList<>();
        if (stored.isEmpty()) {
            list.addAll(seed);
        } else {
            list.addAll(stored);
            Set<String> have = new HashSet<>();
            for (Rule r : stored) {
                have.add(r.name().toLowerCase(Locale.ROOT));
            }
            for (Rule r : seed) {
                if (r.locked() && !have.contains(r.name().toLowerCase(Locale.ROOT))) {
                    list.add(r);
                }
            }
        }
        Set<String> used = new HashSet<>();
        for (int i = 0; i < list.size(); i++) {
            if (list.get(i).id().isEmpty() || used.contains(list.get(i).id())) {
                list.set(i, list.get(i).withId(newId.get()));
            }
            used.add(list.get(i).id());
        }
        return lockedFirst(list);
    }

    private static List<Rule> lockedFirst(List<Rule> list) {
        List<Rule> out = new ArrayList<>();
        list.stream().filter(Rule::locked).forEach(out::add);
        list.stream().filter(r -> !r.locked()).forEach(out::add);
        return out;
    }

    private static boolean sameOrder(List<Rule> a, List<Rule> b) {
        if (a.size() != b.size()) {
            return false;
        }
        for (int i = 0; i < a.size(); i++) {
            if (!a.get(i).id().equals(b.get(i).id())) {
                return false;
            }
        }
        return true;
    }

    /** The rules in evaluation order. */
    public List<Rule> list() {
        return store.list();
    }

    /** Appends an unlocked rule after the existing ones. */
    public synchronized Rule create(Rule in) {
        List<Rule> list = new ArrayList<>(store.list());
        if (list.size() >= MAX_RULES) {
            throw new InvalidException("at most " + MAX_RULES + " rules");
        }
        Rule rule = normalise(in, list, "").withId(newId.get());
        list.add(rule);
        save(list);
        return rule;
    }

    /** Replaces an unlocked rule's content. The id and position stay. */
    public synchronized Rule update(String id, Rule in) {
        List<Rule> list = new ArrayList<>(store.list());
        int idx = indexOf(list, id);
        if (idx < 0) {
            throw new NotFoundException();
        }
        if (list.get(idx).locked()) {
            throw new LockedException();
        }
        Rule rule = normalise(in, list, id).withId(id);
        list.set(idx, rule);
        save(list);
        return rule;
    }

    /** Removes an unlocked rule. */
    public synchronized void delete(String id) {
        List<Rule> list = new ArrayList<>(store.list());
        int idx = indexOf(list, id);
        if (idx < 0) {
            throw new NotFoundException();
        }
        if (list.get(idx).locked()) {
            throw new LockedException();
        }
        list.remove(idx);
        save(list);
    }

    /** Sets the evaluation order: every rule exactly once, and every locked rule before every unlocked one. */
    public synchronized void reorder(List<String> ids) {
        List<Rule> list = store.list();
        if (ids.size() != list.size()) {
            throw new InvalidException("send every rule id exactly once");
        }
        Map<String, Rule> byId = new HashMap<>();
        list.forEach(r -> byId.put(r.id(), r));
        List<Rule> out = new ArrayList<>();
        boolean seenUnlocked = false;
        for (String id : ids) {
            Rule r = byId.remove(id);
            if (r == null) {
                throw new InvalidException("unknown or repeated rule id");
            }
            if (r.locked() && seenUnlocked) {
                throw new InvalidException("locked rules must stay above all other rules");
            }
            seenUnlocked = seenUnlocked || !r.locked();
            out.add(r);
        }
        save(out);
    }

    private void save(List<Rule> list) {
        store.replace(list);
        engine.setRules(list);
    }

    private static int indexOf(List<Rule> list, String id) {
        for (int i = 0; i < list.size(); i++) {
            if (list.get(i).id().equals(id)) {
                return i;
            }
        }
        return -1;
    }

    /** Validates a rule from the API. {@code ownId} is the id being edited, so a rule may keep its own name. */
    static Rule normalise(Rule in, List<Rule> existing, String ownId) {
        String name = in.name() == null ? "" : in.name().trim();
        String description = in.description().trim();
        String reason = in.then() == null || in.then().reason() == null ? "" : in.then().reason().trim();
        String source = in.when().source().trim();
        String eventType = in.when().eventType().trim();
        String repository = in.when().repository().trim();
        String attentionClass = in.then() == null ? null : in.then().attentionClass();
        boolean adaptive = in.then() != null && in.then().adaptive();
        List<Predicate> predicates = new ArrayList<>();
        for (Predicate x : in.when().payloadMatch()) {
            predicates.add(new Predicate(x.field().trim(), x.op().trim().toLowerCase(Locale.ROOT), x.value()));
        }
        List<String> channels = new ArrayList<>();
        if (in.then() != null) {
            for (String c : in.then().channels()) {
                channels.add(c == null ? "" : c.trim().toLowerCase(Locale.ROOT));
            }
        }

        if (name.isEmpty() || cp(name) > MAX_NAME) {
            throw new InvalidException("name is required and at most " + MAX_NAME + " characters");
        }
        for (Rule other : existing) {
            if (!other.id().equals(ownId) && other.name().equalsIgnoreCase(name)) {
                throw new InvalidException("a rule named \"" + name + "\" already exists");
            }
        }
        if (cp(description) > MAX_DESCRIPTION) {
            throw new InvalidException("description is at most " + MAX_DESCRIPTION + " characters");
        }
        if (!Attention.isKnown(attentionClass)) {
            throw new InvalidException("attentionClass must be critical, standard or background");
        }
        if (adaptive && Attention.CRITICAL.equals(attentionClass)) {
            throw new InvalidException("a critical rule cannot be adaptive; critical events never reach Attention Intelligence");
        }
        if (reason.isEmpty()) {
            reason = "matched rule " + name;
        }
        if (cp(reason) > MAX_REASON) {
            throw new InvalidException("reason is at most " + MAX_REASON + " characters");
        }
        Map<String, String> patterns = new LinkedHashMap<>();
        patterns.put("source", source);
        patterns.put("eventType", eventType);
        patterns.put("repository", repository);
        for (Map.Entry<String, String> p : patterns.entrySet()) {
            if (cp(p.getValue()) > MAX_PATTERN) {
                throw new InvalidException(p.getKey() + " is at most " + MAX_PATTERN + " characters");
            }
            if (Glob.validate(p.getValue()) != null) {
                throw new InvalidException(p.getKey() + " is not a valid pattern");
            }
        }
        String badPredicate = Predicates.validate(predicates);
        if (badPredicate != null) {
            throw new InvalidException(badPredicate);
        }
        String badChannels = Predicates.validateChannels(attentionClass, channels);
        if (badChannels != null) {
            throw new InvalidException(badChannels);
        }
        // Only seeds and the rules file can lock a rule.
        return new Rule("", name, description, new Rule.Condition(source, eventType, repository, predicates),
                new Rule.Outcome(attentionClass, reason, adaptive, channels), in.disabled(), false);
    }

    private static int cp(String s) {
        return s.codePointCount(0, s.length());
    }

    // ---- dry run ------------------------------------------------------------------------------------------

    /** An event described by the three fields rules look at, plus optional sample content for payload predicates. */
    public record Sample(String source, String eventType, String repository, Map<String, Object> payload) {
        public Sample {
            source = source == null ? "" : source;
            eventType = eventType == null ? "" : eventType;
            repository = repository == null ? "" : repository;
            payload = payload == null ? Map.of() : payload;
        }

        public Sample(String source, String eventType, String repository) {
            this(source, eventType, repository, null);
        }
    }

    /** One rule's part in a dry run. */
    public record Step(String id, String name, boolean disabled, boolean matched, boolean winner) {}

    /** What a dry run decided. */
    public record Result(boolean matched, String attentionClass, String reason, String matchedRule, boolean aiBypassed,
                         List<Step> steps, List<String> channels) {}

    /** Dry-runs the sample through the current rules. Nothing is stored or delivered. */
    public Result test(Sample in) {
        return evaluate(store.list(), in);
    }

    /** The pure dry run. An unmatched event falls back to standard, exactly as the pipeline does. */
    static Result evaluate(List<Rule> list, Sample in) {
        Event evt = new Event("", in.source(), in.eventType(), in.repository(), new LinkedHashMap<>(in.payload()));
        boolean matched = false;
        String cls = Attention.STANDARD;
        String reason = "no matching rule, default standard";
        String matchedRule = "";
        List<String> channels = List.of();
        List<Step> steps = new ArrayList<>();
        for (Rule r : list) {
            boolean hit = !r.disabled() && PolicyEngine.matches(r.when(), evt);
            boolean winner = hit && !matched;
            if (winner) {
                matched = true;
                cls = r.then().attentionClass();
                reason = r.then().reason();
                matchedRule = r.name();
                channels = Attention.CRITICAL.equals(cls) ? List.of() : r.then().channels();
            }
            steps.add(new Step(r.id(), r.name(), r.disabled(), hit, winner));
        }
        return new Result(matched, cls, reason, matchedRule, Attention.CRITICAL.equals(cls), steps, channels);
    }
}
