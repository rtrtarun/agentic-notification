package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;

import java.time.Duration;
import java.time.Instant;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * Turns stored agent events into traces: one run of an agent, with its ordered steps, totals and final status. It
 * only reads; it decides nothing and calls no model. Ported from the Go service's {@code pkg/traces} with the same
 * arithmetic, the same JSON and the same tie-breaking.
 *
 * <p>An event belongs to a trace when it carries a trace id. Its telemetry lives in the payload's {@code metadata}
 * object: step_kind, tool_name, attempt, duration_ms, tokens_in, tokens_out, cost_usd and confidence.
 *
 * <p>Input order matters, as in Go: events are given <b>oldest first</b> (the repository's order, reversed), steps
 * are sorted by time with a stable sort, so steps stored at the same instant keep their input order; traces that
 * ended at the same instant are ordered by trace id.
 */
public final class Traces {
    public static final String STEP_PLAN = "plan";
    public static final String STEP_TOOL_CALL = "tool_call";
    public static final String STEP_RETRY = "retry";
    public static final String STEP_REFLECT = "reflect";
    public static final String STEP_HUMAN_APPROVAL = "human_approval";
    public static final String STEP_COMPLETE = "complete";
    /** Every step kind an agent may report. */
    public static final Set<String> STEP_KINDS = Set.of(STEP_PLAN, STEP_TOOL_CALL, STEP_RETRY, STEP_REFLECT, STEP_HUMAN_APPROVAL, STEP_COMPLETE);

    public static final String STATUS_RUNNING = "running";
    public static final String STATUS_WAITING = "waiting";
    public static final String STATUS_COMPLETE = "complete";
    public static final String STATUS_FAILED = "failed";

    /** Stops a malformed parent chain (a cycle) from looping. */
    static final int MAX_DEPTH = 32;
    private static final Pattern DECIMAL = Pattern.compile("[+-]?(\\d+\\.?\\d*|\\.\\d+)([eE][+-]?\\d+)?");

    private Traces() {}

    /** One event in a trace, with the platform's decision for it. Zero and empty fields are left out of the JSON. */
    public record Step(String eventId, String parentEventId, String eventType, String stepKind, String toolName,
                       long attempt, long durationMs, long tokensIn, long tokensOut, double costUsd, Double confidence,
                       String attentionClass, String decisionStatus, String decisionReason, int depth, Instant at) {

        /** The JSON Go writes for a step (its {@code omitempty} fields are left out when zero or empty). */
        public Map<String, Object> toJson() {
            Map<String, Object> m = new LinkedHashMap<>();
            m.put("eventId", eventId);
            putIf(m, "parentEventId", parentEventId);
            m.put("eventType", eventType);
            putIf(m, "stepKind", stepKind);
            putIf(m, "toolName", toolName);
            putIf(m, "attempt", attempt);
            putIf(m, "durationMs", durationMs);
            putIf(m, "tokensIn", tokensIn);
            putIf(m, "tokensOut", tokensOut);
            if (costUsd != 0) {
                m.put("costUsd", goNumber(costUsd));
            }
            if (confidence != null) {
                m.put("confidence", goNumber(confidence));
            }
            m.put("attentionClass", attentionClass);
            putIf(m, "decisionStatus", decisionStatus);
            putIf(m, "decisionReason", decisionReason);
            m.put("depth", depth);
            m.put("at", DateTimeFormatter.ISO_INSTANT.format(at));
            return m;
        }
    }

    /** A trace without its steps, for lists. {@code worstClass} is the most serious class of any step. */
    public record Summary(String traceId, String agentName, Instant startedAt, Instant endedAt, long durationMs,
                          int steps, int retries, int failures, long tokensIn, long tokensOut, double costUsd,
                          String worstClass, String status) {

        /** The JSON Go writes for a summary; only {@code agentName} is left out when empty. */
        public Map<String, Object> toJson() {
            Map<String, Object> m = new LinkedHashMap<>();
            m.put("traceId", traceId);
            putIf(m, "agentName", agentName);
            m.put("startedAt", DateTimeFormatter.ISO_INSTANT.format(startedAt));
            m.put("endedAt", DateTimeFormatter.ISO_INSTANT.format(endedAt));
            m.put("durationMs", durationMs);
            m.put("steps", steps);
            m.put("retries", retries);
            m.put("failures", failures);
            m.put("tokensIn", tokensIn);
            m.put("tokensOut", tokensOut);
            m.put("costUsd", goNumber(costUsd));
            m.put("worstClass", worstClass);
            m.put("status", status);
            return m;
        }
    }

    /** A summary and its steps in the order they happened. */
    public record Trace(Summary summary, List<Step> stepList) {
        /** The summary's fields and {@code stepList}, flattened as Go embeds them. */
        public Map<String, Object> toJson() {
            Map<String, Object> m = summary.toJson();
            m.put("stepList", stepList.stream().map(Step::toJson).toList());
            return m;
        }
    }

    /** Every trace in the events (given oldest first), most recently active first, ties by trace id. */
    public static List<Trace> group(List<Event> oldestFirst) {
        Map<String, List<Event>> byTrace = new LinkedHashMap<>();
        for (Event e : oldestFirst) {
            if (!e.getTraceId().isEmpty()) {
                byTrace.computeIfAbsent(e.getTraceId(), k -> new ArrayList<>()).add(e);
            }
        }
        List<Trace> out = new ArrayList<>(byTrace.size());
        byTrace.forEach((id, list) -> out.add(build(id, list)));
        out.sort(Comparator.comparing((Trace t) -> t.summary().endedAt()).reversed()
                .thenComparing(t -> t.summary().traceId()));
        return out;
    }

    /** One trace built from the events (given oldest first); empty when no event carries the id. */
    public static Optional<Trace> find(List<Event> oldestFirst, String traceId) {
        if (traceId == null || traceId.isEmpty()) {
            return Optional.empty();
        }
        List<Event> list = new ArrayList<>();
        for (Event e : oldestFirst) {
            if (traceId.equals(e.getTraceId())) {
                list.add(e);
            }
        }
        return list.isEmpty() ? Optional.empty() : Optional.of(build(traceId, list));
    }

    private static Trace build(String id, List<Event> events) {
        List<Event> list = new ArrayList<>(events);
        list.sort(Comparator.comparing(Event::getCreatedAt)); // stable: ties keep their input order, as Go's SliceStable
        Map<String, String> parent = new HashMap<>();
        for (Event e : list) {
            parent.put(e.getId(), e.getParentEventId());
        }
        List<Step> steps = new ArrayList<>(list.size());
        String agentName = "";
        int retries = 0;
        int failures = 0;
        long spent = 0;
        long tokensIn = 0;
        long tokensOut = 0;
        double cost = 0;
        String worst = Attention.BACKGROUND;
        for (Event e : list) {
            Step s = step(e, depth(e.getId(), parent));
            steps.add(s);
            if (agentName.isEmpty()) {
                agentName = e.getAgentName();
            }
            spent += s.durationMs();
            tokensIn += s.tokensIn();
            tokensOut += s.tokensOut();
            cost += s.costUsd();
            if (STEP_RETRY.equals(s.stepKind())) {
                retries++;
            }
            if (failed(e.getEventType())) {
                failures++;
            }
            if (rank(e.getAttentionClass()) > rank(worst)) {
                worst = e.getAttentionClass();
            }
        }
        Instant started = list.get(0).getCreatedAt();
        Instant ended = list.get(list.size() - 1).getCreatedAt();
        long duration = spent == 0 ? Duration.between(started, ended).toMillis() : spent;
        Summary summary = new Summary(id, agentName, started, ended, duration, steps.size(), retries, failures,
                tokensIn, tokensOut, roundHalfAwayFromZero(cost * 1e6) / 1e6, worst, status(steps));
        return new Trace(summary, List.copyOf(steps));
    }

    private static Step step(Event e, int depth) {
        Map<String, Object> md = e.getPayload().get("metadata") instanceof Map<?, ?> m ? cast(m) : Map.of();
        Double confidence = md.containsKey("confidence") ? num(md, "confidence") : null;
        String status = e.getDecisionState() == null ? "" : nz(e.getDecisionState().getStatus());
        String reason = e.getDecisionState() == null ? "" : nz(e.getDecisionState().getReason());
        return new Step(e.getId(), e.getParentEventId(), e.getEventType(), str(md, "step_kind"), str(md, "tool_name"),
                (long) num(md, "attempt"), (long) num(md, "duration_ms"), (long) num(md, "tokens_in"),
                (long) num(md, "tokens_out"), num(md, "cost_usd"), confidence, e.getAttentionClass(), status, reason,
                depth, e.getCreatedAt());
    }

    /** Reads the last step: complete, waiting for a person, failed, or still running. */
    private static String status(List<Step> steps) {
        Step last = steps.get(steps.size() - 1);
        if (STEP_COMPLETE.equals(last.stepKind())) {
            return STATUS_COMPLETE;
        }
        if (STEP_HUMAN_APPROVAL.equals(last.stepKind())) {
            return STATUS_WAITING;
        }
        return failed(last.eventType()) ? STATUS_FAILED : STATUS_RUNNING;
    }

    static boolean failed(String eventType) {
        String t = eventType.toLowerCase(Locale.ROOT);
        return t.endsWith(".failed") || t.endsWith(".failure") || t.endsWith(".error") || t.endsWith("_failed");
    }

    private static int depth(String id, Map<String, String> parent) {
        int d = 0;
        for (String cur = parent.getOrDefault(id, ""); !cur.isEmpty() && d < MAX_DEPTH; cur = parent.getOrDefault(cur, "")) {
            if (!parent.containsKey(cur)) {
                break; // the parent is not in this trace (or was purged): this step is a root
            }
            d++;
        }
        return d;
    }

    private static int rank(String attentionClass) {
        if (Attention.CRITICAL.equals(attentionClass)) {
            return 3;
        }
        return Attention.STANDARD.equals(attentionClass) ? 2 : 1;
    }

    private static String str(Map<String, Object> m, String key) {
        return m.get(key) instanceof String s ? s : "";
    }

    /** A telemetry number: JSON numbers as they are, decimal text parsed (as Go's ParseFloat does), anything else 0. */
    static double num(Map<String, Object> m, String key) {
        Object v = m.get(key);
        if (v instanceof Number n) {
            return n.doubleValue();
        }
        if (v instanceof String s) {
            String t = AgentEvents.goTrimSpace(s);
            if (DECIMAL.matcher(t).matches()) {
                return Double.parseDouble(t);
            }
        }
        return 0;
    }

    /** Go's math.Round: halves round away from zero (Java's Math.round rounds them up). */
    static double roundHalfAwayFromZero(double v) {
        double a = Math.abs(v);
        double whole = Math.floor(a);
        if (a - whole >= 0.5) { // exact for doubles; adding 0.5 first would round 0.49999999999999994 up
            whole += 1;
        }
        return Math.copySign(whole, v);
    }

    /** A float as Go's JSON encoder writes it: a whole number has no ".0" (0, not 0.0). */
    static Number goNumber(double v) {
        if (v == Math.rint(v) && Math.abs(v) < 1e15) {
            return (long) v;
        }
        return v;
    }

    private static void putIf(Map<String, Object> m, String key, String value) {
        if (value != null && !value.isEmpty()) {
            m.put(key, value);
        }
    }

    private static void putIf(Map<String, Object> m, String key, long value) {
        if (value != 0) {
            m.put(key, value);
        }
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> cast(Map<?, ?> m) {
        return (Map<String, Object>) m;
    }

    private static String nz(String s) {
        return s == null ? "" : s;
    }
}
