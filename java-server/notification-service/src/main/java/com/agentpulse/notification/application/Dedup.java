package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/**
 * Deterministic deduplication and threading (Layer 1, never AI). The thread and key formats are identical to the
 * Go service so both services group events the same way.
 */
public final class Dedup {
    /** Go's shortest {@code 'g'} format switches to exponent form at this decimal exponent (strconv's precision 6). */
    private static final int GO_SHORTEST_EXPONENT_LIMIT = 6;

    private Dedup() {}

    /** What a {@link Deduper} decided for one event. */
    public record Result(String dedupKey, String threadId, boolean duplicate, String originalId) {}

    /**
     * Assigns keys and detects duplicates. Implementations must fail open: when a backing store is unavailable
     * they report "not a duplicate", so no event is ever suppressed by an infrastructure fault.
     */
    public interface Deduper {
        /** Records the event and reports whether it duplicates a recent one. */
        Result check(Event event);

        /** Removes the entry for {@code key} if it was recorded by event {@code id}; used after a failed save. */
        void forget(String key, String id);
    }

    /** Groups related events about one subject, for example one pull request, or all the steps of one agent run. */
    public static String threadId(Event event) {
        if (!event.getTraceId().isEmpty()) {
            return event.getSource() + ":trace:" + event.getTraceId();
        }
        List<String> parts = new ArrayList<>(List.of(event.getSource(), event.getRepository()));
        String subject = subjectOf(event.getPayload());
        if (!subject.isEmpty()) {
            parts.add(subject);
        }
        return String.join(":", parts);
    }

    /**
     * Identifies a repeat of the same thing: same thread, type, action and, for pushes, the same head commit.
     * Without the commit, every push to a repository inside the window would collapse into the first.
     *
     * <p>Steps of an agent run are different: a retry or a repeated failure is a new fact, not a repeat, so two
     * steps merge only when they share the run, the type, the parent step, the step kind and the attempt number.
     * The key is Go's text exactly, so a step seen by one service is a duplicate for the other through Redis.
     */
    public static String dedupKey(Event event) {
        Map<String, Object> payload = event.getPayload();
        if (!event.getTraceId().isEmpty()) {
            Map<?, ?> md = payload.get("metadata") instanceof Map<?, ?> m ? m : Map.of();
            String kind = md.get("step_kind") instanceof String s ? s : "";
            return String.join("|", threadId(event), event.getEventType(), event.getParentEventId(), kind,
                    goSprint(md.get("attempt")));
        }
        String action = payload.get("action") instanceof String s ? s : "";
        String head = payload.get("after") instanceof String s ? s : "";
        return String.join("|", threadId(event), event.getEventType(), action, head);
    }

    /**
     * A JSON value as Go's {@code fmt.Sprint} writes it after {@code encoding/json} decoded it: absent or null is
     * {@code <nil>}, and every number is a float64 written in the fewest digits that identify it, in exponent form
     * below 1e-4 and from 1e6 ({@code 3}, {@code 2.5}, {@code 999999}, {@code 1e+06}, {@code 1e-05}), as Go's
     * {@code %v} does with {@code strconv}'s shortest {@code 'g'} format.
     */
    static String goSprint(Object value) {
        if (value == null) {
            return "<nil>";
        }
        if (!(value instanceof Number n)) {
            return String.valueOf(value);
        }
        double d = n.doubleValue();
        if (Double.isNaN(d)) {
            return "NaN";
        }
        if (Double.isInfinite(d)) {
            return d > 0 ? "+Inf" : "-Inf";
        }
        if (d == 0) {
            return 1 / d < 0 ? "-0" : "0";
        }
        BigDecimal exact = new BigDecimal(Double.toString(d)).stripTrailingZeros();
        int exponent = exact.precision() - exact.scale() - 1;
        if (exponent >= -4 && exponent < GO_SHORTEST_EXPONENT_LIMIT) {
            return exact.toPlainString();
        }
        String digits = exact.unscaledValue().abs().toString();
        String mantissa = digits.length() == 1 ? digits : digits.charAt(0) + "." + digits.substring(1);
        return (d < 0 ? "-" : "") + mantissa + (exponent < 0 ? "e-" : "e+")
                + String.format(Locale.ROOT, "%02d", Math.abs(exponent));
    }

    private static String subjectOf(Map<String, Object> payload) {
        if (payload.get("number") instanceof Number n) {
            return Long.toString(n.longValue());
        }
        for (String key : List.of("pull_request", "issue")) {
            if (payload.get(key) instanceof Map<?, ?> obj && obj.get("number") instanceof Number n) {
                return Long.toString(n.longValue());
            }
        }
        return "";
    }
}
