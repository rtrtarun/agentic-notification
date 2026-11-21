package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;

import java.security.SecureRandom;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * Turns what an AI agent reports about one step of its run into the platform's event. Deterministic Layer 1 input
 * handling, ported from the Go service's {@code pkg/agentevents} with the same rules and messages: it validates and
 * normalises, and decides nothing about importance.
 *
 * <p>An agent's own {@code severity} is only data. It lands in the payload where rules can match on it, but it never
 * sets the attention class: only a rule does, so an agent cannot declare its own event critical.
 */
public final class AgentEvents {
    /** Used when an agent does not name a source, and as the prefix of a generated id. */
    public static final String DEFAULT_SOURCE = "agent";

    static final int MAX_NAME = 100;
    static final int MAX_ID = 200;
    static final int MAX_METADATA = 30;
    static final int MAX_STRING_VALUE = 500;
    private static final int MAX_SOURCE = 50;

    private static final Pattern ID = Pattern.compile("[A-Za-z0-9][A-Za-z0-9_.:\\-]*");
    private static final Pattern TYPE = Pattern.compile("[A-Za-z0-9][A-Za-z0-9_.\\-]*");
    private static final Pattern SOURCE = Pattern.compile("[a-z0-9][a-z0-9_.\\-]*");
    private static final Set<String> SEVERITIES = Set.of("info", "low", "medium", "high", "critical");
    /** Telemetry numbers that cannot be below zero, checked in this order (as in Go). */
    private static final List<String> NON_NEGATIVE = List.of("attempt", "duration_ms", "tokens_in", "tokens_out", "cost_usd");
    private static final SecureRandom RANDOM = new SecureRandom();

    private AgentEvents() {}

    /** One step as an agent reports it. Null strings and maps count as absent. */
    public record Input(String id, String source, String eventType, String repository, String traceId,
                        String parentEventId, String agentName, String severity, Map<String, Object> metadata,
                        Map<String, Object> payload) {
        public Input {
            id = nz(id);
            source = nz(source);
            eventType = nz(eventType);
            repository = nz(repository);
            traceId = nz(traceId);
            parentEventId = nz(parentEventId);
            agentName = nz(agentName);
            severity = nz(severity);
            metadata = metadata == null ? Map.of() : metadata;
            payload = payload == null ? Map.of() : payload;
        }
    }

    /** A step the rules refuse; the message is safe to show and matches Go's word for word. */
    public static final class InvalidException extends RuntimeException {
        InvalidException(String detail) {
            super("invalid agent event: " + detail);
        }
    }

    /**
     * Validates an input and returns the event to hand to the pipeline. The checks run in Go's order, so an input
     * with one mistake gets the same message from both services.
     */
    public static Event normalize(Input in) {
        String source = goTrimSpace(in.source()).toLowerCase(Locale.ROOT);
        if (source.isEmpty()) {
            source = DEFAULT_SOURCE;
        }
        if (!SOURCE.matcher(source).matches() || source.length() > MAX_SOURCE) {
            throw new InvalidException("source must be lower-case letters, digits, dots, dashes or underscores");
        }
        String eventType = goTrimSpace(in.eventType());
        if (eventType.isEmpty() || eventType.length() > MAX_NAME || !TYPE.matcher(eventType).matches()) {
            throw new InvalidException("eventType is required, such as tool.call.failed");
        }
        checkId("id", in.id());
        checkId("traceId", in.traceId());
        checkId("parentEventId", in.parentEventId());
        if (!in.parentEventId().isEmpty() && in.traceId().isEmpty()) {
            throw new InvalidException("parentEventId needs a traceId");
        }
        if (!in.parentEventId().isEmpty() && in.parentEventId().equals(in.id())) {
            throw new InvalidException("an event cannot be its own parent");
        }
        if (runes(in.agentName()) > MAX_NAME || runes(in.repository()) > MAX_NAME * 2) {
            throw new InvalidException("agentName and repository are too long");
        }
        String severity = goTrimSpace(in.severity()).toLowerCase(Locale.ROOT);
        if (!severity.isEmpty() && !SEVERITIES.contains(severity)) {
            throw new InvalidException("severity must be info, low, medium, high or critical");
        }
        checkMetadata(in.metadata());

        Map<String, Object> payload = new LinkedHashMap<>(in.payload());
        if (!in.metadata().isEmpty()) {
            payload.put("metadata", new LinkedHashMap<>(in.metadata()));
        }
        if (!severity.isEmpty()) {
            payload.put("severity", severity); // data for rules; it never sets the class
        }
        if (!in.agentName().isEmpty()) {
            payload.put("agentName", in.agentName());
        }
        String id = in.id().isEmpty() ? newId() : in.id();
        Event event = new Event(id, source, eventType, in.repository(), payload);
        event.setTraceId(in.traceId());
        event.setParentEventId(in.parentEventId());
        event.setAgentName(in.agentName());
        return event;
    }

    private static void checkId(String name, String value) {
        // Go checks the three ids in map order, which is random; an input with two bad ids may name either one.
        if (!value.isEmpty() && (value.length() > MAX_ID || !ID.matcher(value).matches())) {
            throw new InvalidException(name + " may only contain letters, digits and . _ : - (at most " + MAX_ID + " characters)");
        }
    }

    private static void checkMetadata(Map<String, Object> md) {
        if (md.size() > MAX_METADATA) {
            throw new InvalidException("metadata may have at most " + MAX_METADATA + " keys");
        }
        if (md.containsKey("step_kind")) {
            Object kind = md.get("step_kind");
            if (!(kind instanceof String s) || !Traces.STEP_KINDS.contains(s)) {
                throw new InvalidException("metadata.step_kind must be plan, tool_call, retry, reflect, human_approval or complete");
            }
        }
        for (String key : NON_NEGATIVE) {
            if (md.containsKey(key)) {
                Double n = number(md.get(key));
                if (n == null || n < 0) {
                    throw new InvalidException("metadata." + key + " must be a number of zero or more");
                }
            }
        }
        if (md.containsKey("confidence")) {
            Double n = number(md.get("confidence"));
            if (n == null || n < 0 || n > 1) {
                throw new InvalidException("metadata.confidence must be a number from 0 to 1");
            }
        }
        for (Map.Entry<String, Object> e : md.entrySet()) {
            if (e.getValue() instanceof String s && runes(s) > MAX_STRING_VALUE) {
                throw new InvalidException("metadata." + e.getKey() + " is longer than " + MAX_STRING_VALUE + " characters");
            }
        }
    }

    /** A JSON number as a double, or null for anything else (text, booleans, NaN and infinities). */
    static Double number(Object v) {
        if (v instanceof Number n) {
            double d = n.doubleValue();
            return Double.isNaN(d) || Double.isInfinite(d) ? null : d;
        }
        return null;
    }

    private static String newId() {
        byte[] b = new byte[10];
        RANDOM.nextBytes(b);
        return DEFAULT_SOURCE + "-" + HexFormat.of().formatHex(b);
    }

    /** Characters counted as Go counts runes (code points), not UTF-16 units. */
    private static int runes(String s) {
        return s.codePointCount(0, s.length());
    }

    /** Trims what Go's {@code strings.TrimSpace} trims: the characters {@code unicode.IsSpace} accepts. */
    static String goTrimSpace(String s) {
        int start = 0;
        int end = s.length();
        while (start < end && isGoSpace(s.charAt(start))) {
            start++;
        }
        while (end > start && isGoSpace(s.charAt(end - 1))) {
            end--;
        }
        return s.substring(start, end);
    }

    private static boolean isGoSpace(char c) {
        return switch (c) {
            case '\t', '\n', '\u000B', '\f', '\r', ' ', '\u0085', ' ' -> true;
            default -> c > 'ÿ' && Character.isSpaceChar(c);
        };
    }

    private static String nz(String s) {
        return s == null ? "" : s;
    }
}
