package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Predicate;

import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * Payload predicates of a rule: how a field is found in an event, how it is compared, and which predicates are
 * allowed to be saved. A port of the Go service's {@code pkg/policy/predicate.go}; the shared contract file
 * {@code 11-rule-predicates.json} keeps the two the same. Deterministic: no model is ever involved.
 */
public final class Predicates {
    /** How many payload predicates one rule may carry. */
    public static final int MAX_PREDICATES = 5;
    /** The channels a rule may route to. */
    public static final List<String> KNOWN_CHANNELS = List.of("email", "slack", "teams", "sms");

    private static final int MAX_FIELD = 100;
    private static final int MAX_VALUE = 200;
    private static final Pattern FIELD = Pattern.compile("[A-Za-z0-9_][A-Za-z0-9_.\\-]*");
    private static final Pattern DECIMAL = Pattern.compile("[+-]?(\\d+\\.?\\d*|\\.\\d+)([eE][+-]?\\d+)?");
    private static final Set<String> NUMERIC_OPS = Set.of("gt", "gte", "lt", "lte");

    private Predicates() {}

    // ---- matching -----------------------------------------------------------------------------------------

    /** True when the event satisfies every predicate. An empty list matches everything. */
    public static boolean matchAll(List<Predicate> predicates, Event event) {
        for (Predicate p : predicates) {
            if (!matches(p, event)) {
                return false;
            }
        }
        return true;
    }

    static boolean matches(Predicate p, Event event) {
        Found found = resolve(event, p.field());
        String op = p.op().isEmpty() ? "eq" : p.op();
        if (op.equals("exists")) {
            boolean want = !(p.value() instanceof Boolean b) || b;
            return found.present == want;
        }
        if (!found.present) {
            return false;
        }
        if (op.equals("eq")) {
            return equal(found.value, p.value());
        }
        if (op.equals("ne")) {
            return !equal(found.value, p.value());
        }
        Double a = number(found.value);
        Double b = number(p.value());
        if (a == null || b == null) {
            return false;
        }
        return switch (op) {
            case "gt" -> a > b;
            case "gte" -> a >= b;
            case "lt" -> a < b;
            case "lte" -> a <= b;
            default -> false;
        };
    }

    private record Found(boolean present, Object value) {
        static final Found NONE = new Found(false, null);
    }

    /** Finds a field: a dotted path into the payload, or branch, author or severity, which look in several places. */
    private static Found resolve(Event event, String field) {
        Map<String, Object> payload = event.getPayload();
        switch (field) {
            case "branch": {
                Found v = first(payload, List.of("branch"), List.of("pull_request", "head", "ref"), List.of("ref"));
                if (v.present && v.value instanceof String s) {
                    return new Found(true, s.startsWith("refs/heads/") ? s.substring("refs/heads/".length()) : s);
                }
                return Found.NONE;
            }
            case "author":
                return first(payload, List.of("author"), List.of("sender", "login"), List.of("pusher", "name"),
                        List.of("metadata", "author"));
            case "severity":
                return first(payload, List.of("severity"), List.of("metadata", "severity"), List.of("alert", "severity"),
                        List.of("security_advisory", "severity"));
            default:
                return lookup(payload, List.of(field.split("\\.", -1)));
        }
    }

    @SafeVarargs
    private static Found first(Map<String, Object> payload, List<String>... paths) {
        for (List<String> path : paths) {
            Found f = lookup(payload, path);
            if (f.present) {
                return f;
            }
        }
        return Found.NONE;
    }

    @SuppressWarnings("unchecked")
    private static Found lookup(Map<String, Object> payload, List<String> path) {
        Object cur = payload;
        for (String key : path) {
            if (!(cur instanceof Map<?, ?> m) || !m.containsKey(key)) {
                return Found.NONE;
            }
            cur = ((Map<String, Object>) m).get(key);
        }
        return new Found(true, cur);
    }

    private static boolean equal(Object got, Object want) {
        Double a = number(got);
        Double b = number(want);
        if (a != null && b != null) {
            return a.doubleValue() == b.doubleValue();
        }
        if (got instanceof Boolean x) {
            return want instanceof Boolean y && x.equals(y);
        }
        return got instanceof String s && want instanceof String t && s.equalsIgnoreCase(t);
    }

    /** Reads a value as a number. Numeric strings count, because many senders put numbers in strings. */
    static Double number(Object v) {
        if (v instanceof Number n) {
            double d = n.doubleValue();
            return Double.isNaN(d) ? null : d;
        }
        if (v instanceof String s) {
            String t = s.strip();
            if (!DECIMAL.matcher(t).matches()) {
                return null;
            }
            try {
                double d = Double.parseDouble(t);
                return Double.isNaN(d) || Double.isInfinite(d) ? null : d;
            } catch (NumberFormatException e) {
                return null;
            }
        }
        return null;
    }

    // ---- validation ---------------------------------------------------------------------------------------

    /** Checks predicates before a rule is stored. Returns the problem, or null when they are fine. */
    public static String validate(List<Predicate> predicates) {
        if (predicates.size() > MAX_PREDICATES) {
            return "a rule can have at most " + MAX_PREDICATES + " payload predicates";
        }
        for (int i = 0; i < predicates.size(); i++) {
            Predicate p = predicates.get(i);
            String where = "payload predicate " + (i + 1) + ": ";
            if (p.field().isEmpty() || p.field().codePointCount(0, p.field().length()) > MAX_FIELD
                    || !FIELD.matcher(p.field()).matches()) {
                return where + "field must be a dotted name such as metadata.attempt";
            }
            String op = p.op();
            if (op.isEmpty() || op.equals("eq") || op.equals("ne")) {
                if (!scalar(p.value())) {
                    return where + (op.isEmpty() ? "eq" : op) + " needs a string, number or true/false value";
                }
            } else if (NUMERIC_OPS.contains(op)) {
                if (number(p.value()) == null) {
                    return where + op + " needs a number";
                }
            } else if (op.equals("exists")) {
                if (p.value() != null && !(p.value() instanceof Boolean)) {
                    return where + "exists takes true or false";
                }
            } else {
                return where + "unknown operator \"" + op + "\" (use eq, ne, gt, gte, lt, lte or exists)";
            }
            if (p.value() instanceof String s && s.codePointCount(0, s.length()) > MAX_VALUE) {
                return where + "value is at most " + MAX_VALUE + " characters";
            }
        }
        return null;
    }

    private static boolean scalar(Object v) {
        return v instanceof String || v instanceof Boolean || v instanceof Number;
    }

    /**
     * Checks a rule's channel override: known names only, no repeats, and never on a critical rule, because critical
     * events always go to every configured channel. Returns the problem, or null.
     */
    public static String validateChannels(String attentionClass, List<String> channels) {
        if (channels.isEmpty()) {
            return null;
        }
        if ("critical".equals(attentionClass)) {
            return "a critical rule cannot choose channels; critical events always go to every configured channel";
        }
        Set<String> seen = new HashSet<>();
        for (String c : channels) {
            if (!KNOWN_CHANNELS.contains(c)) {
                return "unknown channel \"" + c + "\" (use email, slack, teams or sms)";
            }
            if (!seen.add(c)) {
                return "channel \"" + c + "\" is listed twice";
            }
        }
        return null;
    }
}
