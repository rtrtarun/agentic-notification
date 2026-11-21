package com.agentpulse.notification.application;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.security.SecureRandom;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.List;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.regex.Pattern;

/**
 * W3C Trace Context and spans, ported from the Go service's {@code pkg/telemetry} with the same rules, span names and
 * attributes. A request keeps the trace it arrived with (and its sampled flag) or starts a new sampled one; each step
 * that matters is a span. Spans are logged at debug level and, with an {@link Exporter}, sampled ones are also sent to
 * an OpenTelemetry collector.
 *
 * <p><b>The span context is always passed explicitly</b>, as a parameter: nothing here keeps state per thread.
 *
 * <p>A span carries only allow-listed attributes (names, ids, classes and status codes), never payloads, titles,
 * recipients, keys or error text; a failed span's exported status message is the fixed word {@code failed}.
 */
public final class Telemetry {
    /** The W3C Trace Context header. */
    public static final String HEADER = "traceparent";

    public static final String ATTR_HTTP_METHOD = "http.request.method";
    public static final String ATTR_HTTP_ROUTE = "http.route";
    public static final String ATTR_HTTP_STATUS_CODE = "http.response.status_code";
    public static final String ATTR_EVENT_SOURCE = "event.source";
    public static final String ATTR_EVENT_TYPE = "event.type";
    public static final String ATTR_ATTENTION_CLASS = "attention.class";
    public static final String ATTR_DECISION_STATUS = "decision.status";
    public static final String ATTR_RULE_NAME = "rule.name";
    public static final String ATTR_AGENT_TRACE_ID = "agent.trace_id";
    static final Set<String> ALLOWED_ATTRIBUTES = Set.of(ATTR_HTTP_METHOD, ATTR_HTTP_ROUTE, ATTR_HTTP_STATUS_CODE,
            ATTR_EVENT_SOURCE, ATTR_EVENT_TYPE, ATTR_ATTENTION_CLASS, ATTR_DECISION_STATUS, ATTR_RULE_NAME, ATTR_AGENT_TRACE_ID);
    /** An attribute value is cut to this many characters (code points, as Go counts runes). */
    static final int MAX_ATTRIBUTE_CHARS = 200;
    /** The only status message an exported span carries. */
    public static final String FAILED_STATUS_MESSAGE = "failed";

    private static final Logger LOG = LoggerFactory.getLogger(Telemetry.class);
    private static final SecureRandom RANDOM = new SecureRandom();
    private static final HexFormat HEX = HexFormat.of();
    private static final Pattern TRACE_ID = Pattern.compile("[0-9a-f]{32}");
    private static final Pattern SPAN_ID = Pattern.compile("[0-9a-f]{16}");
    private static final Pattern FLAGS = Pattern.compile("[0-9a-f]{2}");
    private static final String ZERO_TRACE = "0".repeat(32);
    private static final String ZERO_SPAN = "0".repeat(16);

    private Telemetry() {}

    /** What a span is, as OpenTelemetry numbers it. */
    public enum Kind {
        INTERNAL(1), SERVER(2);

        public final int number;

        Kind(int number) {
            this.number = number;
        }
    }

    /** The span context that travels between services: lower-case hex ids and the sampled flag. */
    public record SpanContext(String traceId, String spanId, boolean sampled) {
        /** Renders the header value, for example {@code 00-<trace>-<span>-01}. */
        public String traceparent() {
            return "00-" + traceId + "-" + spanId + (sampled ? "-01" : "-00");
        }
    }

    /**
     * Reads a version 00 header exactly as Go does: Go's whitespace is trimmed, the four parts must have the right
     * lengths, hex must be lower case, and all-zero ids are refused. Only bit 0 of the flags means sampled.
     */
    public static Optional<SpanContext> parseTraceparent(String header) {
        if (header == null) {
            return Optional.empty();
        }
        String[] parts = AgentEvents.goTrimSpace(header).split("-", -1);
        if (parts.length != 4 || !parts[0].equals("00") || !TRACE_ID.matcher(parts[1]).matches()
                || !SPAN_ID.matcher(parts[2]).matches() || !FLAGS.matcher(parts[3]).matches()
                || parts[1].equals(ZERO_TRACE) || parts[2].equals(ZERO_SPAN)) {
            return Optional.empty();
        }
        boolean sampled = (Integer.parseInt(parts[3], 16) & 1) == 1;
        return Optional.of(new SpanContext(parts[1], parts[2], sampled));
    }

    /** One allow-listed attribute; {@code isInt} marks an integer value. */
    public record Attribute(String key, String value, long intValue, boolean isInt) {}

    /** A finished span as an exporter receives it. {@code parentSpanId} is empty for a root span. */
    public record SpanData(String traceId, String spanId, String parentSpanId, String name, Kind kind, Instant start,
                           Instant end, List<Attribute> attributes, boolean failed) {}

    /** Receives finished, sampled spans. {@link #export} must not block. */
    public interface Exporter {
        void export(SpanData span);
    }

    /** Starts spans. Without an exporter spans are only logged. */
    public static final class Tracer {
        private final Clock clock;
        private final Exporter exporter;

        public Tracer(Clock clock, Exporter exporter) {
            this.clock = clock;
            this.exporter = exporter;
        }

        /** A tracer that only logs. */
        public Tracer(Clock clock) {
            this(clock, null);
        }

        /**
         * Begins a span named {@code name} as a child of {@code parent}, or as the root of a new sampled trace when
         * {@code parent} is null. A child keeps the parent's trace and sampled flag.
         */
        public Span start(SpanContext parent, String name, Kind kind) {
            SpanContext own = parent == null
                    ? new SpanContext(newId(16, ZERO_TRACE), newId(8, ZERO_SPAN), true)
                    : new SpanContext(parent.traceId(), newId(8, ZERO_SPAN), parent.sampled());
            return new Span(this, name, kind, own, parent == null ? "" : parent.spanId(), clock.instant());
        }

        /** An internal span. */
        public Span start(SpanContext parent, String name) {
            return start(parent, name, Kind.INTERNAL);
        }
    }

    /** Starts a span on {@code tracer}, or a span that does nothing when there is no tracer. */
    public static Span start(Tracer tracer, SpanContext parent, String name) {
        return tracer == null ? Span.noop(parent) : tracer.start(parent, name);
    }

    /** One timed step. End it exactly once; later calls do nothing. Use it from one thread. */
    public static final class Span {
        private final Tracer tracer;
        private final String name;
        private final Kind kind;
        private final SpanContext context;
        private final String parentSpanId;
        private final Instant start;
        private final List<Attribute> attributes = new ArrayList<>();
        private final AtomicBoolean ended = new AtomicBoolean();

        private Span(Tracer tracer, String name, Kind kind, SpanContext context, String parentSpanId, Instant start) {
            this.tracer = tracer;
            this.name = name;
            this.kind = kind;
            this.context = context;
            this.parentSpanId = parentSpanId;
            this.start = start;
        }

        /** A span that records nothing; its context is the parent's, so children still join the caller's trace. */
        static Span noop(SpanContext parent) {
            return new Span(null, "", Kind.INTERNAL, parent, "", null);
        }

        /** This span's own context, to pass to children and to return in a response header. */
        public SpanContext context() {
            return context;
        }

        /** Records key=value when the key is allow-listed and the value not empty; long values are cut. */
        public void setAttribute(String key, String value) {
            if (tracer == null || !ALLOWED_ATTRIBUTES.contains(key) || value == null || value.isEmpty()) {
                return;
            }
            String v = value;
            if (v.codePointCount(0, v.length()) > MAX_ATTRIBUTE_CHARS) {
                v = v.substring(0, v.offsetByCodePoints(0, MAX_ATTRIBUTE_CHARS));
            }
            attributes.add(new Attribute(key, v, 0, false));
        }

        /** Records an allow-listed integer attribute. */
        public void setIntAttribute(String key, long value) {
            if (tracer == null || !ALLOWED_ATTRIBUTES.contains(key)) {
                return;
            }
            attributes.add(new Attribute(key, Long.toString(value), value, true));
        }

        /** Ends the span successfully. */
        public void end() {
            end(null);
        }

        /**
         * Ends the span; a non-null {@code error} marks it failed. The error's text goes only to the local debug log,
         * never to the exporter.
         */
        public void end(String error) {
            if (tracer == null || !ended.compareAndSet(false, true)) {
                return;
            }
            Instant end = tracer.clock.instant();
            if (tracer.exporter != null && context.sampled()) {
                tracer.exporter.export(new SpanData(context.traceId(), context.spanId(), parentSpanId, name, kind, start, end,
                        List.copyOf(attributes), error != null));
            }
            if (LOG.isDebugEnabled()) {
                double ms = Duration.between(start, end).toNanos() / 1000 / 1000.0;
                LOG.debug("span name={} trace_id={} span_id={} duration_ms={}{}{}", name, context.traceId(), context.spanId(), ms,
                        parentSpanId.isEmpty() ? "" : " parent_span_id=" + parentSpanId, error == null ? "" : " error=" + error);
            }
        }
    }

    private static String newId(int bytes, String zero) {
        byte[] b = new byte[bytes];
        String id;
        do {
            RANDOM.nextBytes(b);
            id = HEX.formatHex(b);
        } while (id.equals(zero));
        return id;
    }
}
