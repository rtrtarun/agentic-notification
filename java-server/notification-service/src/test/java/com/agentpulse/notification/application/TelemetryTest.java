package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The traceparent rules and span behaviour of the Go service's {@code pkg/telemetry}, and the pipeline's spans. */
class TelemetryTest {
    private static final String TRACE = "4bf92f3577b34da6a3ce929d0e0e4736";
    private static final String SPAN = "00f067aa0ba902b7";

    /** Keeps exported spans in memory. */
    static final class Capture implements Telemetry.Exporter {
        final List<Telemetry.SpanData> spans = new ArrayList<>();

        @Override
        public synchronized void export(Telemetry.SpanData span) {
            spans.add(span);
        }

        synchronized Telemetry.SpanData named(String name) {
            return spans.stream().filter(s -> s.name().equals(name)).findFirst().orElseThrow(() -> new AssertionError("no span " + name + " in " + spans));
        }
    }

    @Test
    void traceparentIsReadAsGoReadsIt() {
        Telemetry.SpanContext sc = Telemetry.parseTraceparent("00-" + TRACE + "-" + SPAN + "-01").orElseThrow();
        assertEquals(TRACE, sc.traceId());
        assertEquals(SPAN, sc.spanId());
        assertTrue(sc.sampled());
        assertEquals("00-" + TRACE + "-" + SPAN + "-01", sc.traceparent());
        assertEquals("00-" + TRACE + "-" + SPAN + "-00", new Telemetry.SpanContext(TRACE, SPAN, false).traceparent());
        assertTrue(Telemetry.parseTraceparent("  00-" + TRACE + "-" + SPAN + "-01\t").isPresent(), "Go's whitespace is trimmed");
        Map<String, Boolean> flags = Map.of("00", false, "01", true, "02", false, "03", true, "ff", true, "fe", false);
        flags.forEach((f, sampled) -> assertEquals(sampled, Telemetry.parseTraceparent("00-" + TRACE + "-" + SPAN + "-" + f).orElseThrow().sampled(), f));
        for (String bad : List.of("", "00-" + TRACE + "-" + SPAN, "01-" + TRACE + "-" + SPAN + "-01", "00-" + TRACE.toUpperCase() + "-" + SPAN + "-01",
                "00-" + TRACE + "-" + SPAN.toUpperCase() + "-01", "00-" + TRACE + "-" + SPAN + "-0F", "00-" + "0".repeat(32) + "-" + SPAN + "-01",
                "00-" + TRACE + "-" + "0".repeat(16) + "-01", "00-" + TRACE + "x-" + SPAN + "-01", "00-" + TRACE + "-" + SPAN + "-01-extra",
                "00-" + TRACE.substring(1) + "g-" + SPAN + "-01", "00-" + TRACE + "-" + SPAN + "-1")) {
            assertFalse(Telemetry.parseTraceparent(bad).isPresent(), bad);
        }
        assertFalse(Telemetry.parseTraceparent(null).isPresent());
    }

    @Test
    void aRootIsANewSampledTraceAndAChildKeepsItsParentsTraceAndSampling() {
        Capture out = new Capture();
        Telemetry.Tracer tracer = new Telemetry.Tracer(Clock.systemUTC(), out);
        Telemetry.Span root = tracer.start(null, "root");
        assertTrue(root.context().sampled());
        assertEquals(32, root.context().traceId().length());
        Telemetry.Span child = tracer.start(root.context(), "child");
        assertEquals(root.context().traceId(), child.context().traceId());
        assertNotEquals(root.context().spanId(), child.context().spanId());
        child.end();
        root.end();
        root.end(); // a second end does nothing
        assertEquals(2, out.spans.size());
        assertEquals(root.context().spanId(), out.named("child").parentSpanId());
        assertEquals("", out.named("root").parentSpanId());
        assertEquals(Telemetry.Kind.INTERNAL, out.named("root").kind());

        Telemetry.Span unsampled = tracer.start(new Telemetry.SpanContext(TRACE, SPAN, false), "quiet", Telemetry.Kind.SERVER);
        assertFalse(unsampled.context().sampled());
        unsampled.end();
        assertEquals(2, out.spans.size(), "a caller that did not sample gets no exported span");
    }

    @Test
    void onlyAllowListedAttributesAreKeptAndNoErrorTextIsExported() {
        Capture out = new Capture();
        Telemetry.Span span = new Telemetry.Tracer(Clock.systemUTC(), out).start(null, "pipeline.Handle");
        span.setAttribute(Telemetry.ATTR_EVENT_TYPE, "push");
        span.setAttribute("payload.password", "hunter2");
        span.setAttribute(Telemetry.ATTR_RULE_NAME, "");
        span.setAttribute(Telemetry.ATTR_RULE_NAME, null);
        span.setAttribute(Telemetry.ATTR_AGENT_TRACE_ID, "😀".repeat(300));
        span.setIntAttribute("http.request.body", 7);
        span.setIntAttribute(Telemetry.ATTR_HTTP_STATUS_CODE, 503);
        span.end("smtp: auth failed for ops@example.com with password hunter2");
        Telemetry.SpanData data = out.spans.get(0);
        assertTrue(data.failed());
        assertEquals(3, data.attributes().size());
        String agent = data.attributes().get(1).value();
        assertEquals(Telemetry.MAX_ATTRIBUTE_CHARS, agent.codePointCount(0, agent.length()), "cut by characters, as Go counts runes");
        assertTrue(data.attributes().get(2).isInt());
        assertFalse(data.toString().contains("hunter2"), "the error text never reaches the exporter");
        assertFalse(data.toString().contains("ops@example.com"));
    }

    @Test
    void withoutATracerSpansDoNothingButKeepTheCallersContext() {
        Telemetry.SpanContext caller = new Telemetry.SpanContext(TRACE, SPAN, true);
        Telemetry.Span span = Telemetry.start(null, caller, "x");
        assertSame(caller, span.context());
        span.setAttribute(Telemetry.ATTR_EVENT_TYPE, "push");
        span.setIntAttribute(Telemetry.ATTR_HTTP_STATUS_CODE, 1);
        span.end("ignored");
        span.end();
    }

    private static Pipeline traced(Capture out, List<Rule> rules, Ports.NotificationProvider provider) {
        return new Pipeline(new PolicyEngine(rules), provider, new Fakes.RecordingRepository(new ArrayList<>()))
                .withDeduper(new MemoryDeduper(java.time.Duration.ofMinutes(10), Clock.systemUTC()))
                .withClassifier(GuardedClassifier.of(new Heuristic()))
                .withTracer(new Telemetry.Tracer(Clock.systemUTC(), out));
    }

    @Test
    void eachPipelineStepIsAChildSpanAndThePipelineSpanNamesTheDecisionNotThePayload() {
        Capture out = new Capture();
        Pipeline pipeline = traced(out, List.of(), new Fakes.RecordingProvider(new ArrayList<>()));
        Event e = AgentEvents.normalize(new AgentEvents.Input("s1", null, "agent.plan", null, "run-7", null, "bot", null, null,
                Map.of("token", "ghp_supersecret", "email", "dev@example.com")));
        Telemetry.SpanContext request = new Telemetry.SpanContext(TRACE, SPAN, true);
        pipeline.handle(e, request);

        Telemetry.SpanData handle = out.named("pipeline.Handle");
        assertEquals(TRACE, handle.traceId());
        assertEquals(SPAN, handle.parentSpanId(), "the pipeline span is a child of the request's span");
        for (String step : List.of("policy.Evaluate", "dedup.Check", "attention.Classify")) {
            assertEquals(handle.spanId(), out.named(step).parentSpanId(), step + " is a child of pipeline.Handle");
        }
        Map<String, String> attrs = new java.util.HashMap<>();
        handle.attributes().forEach(a -> attrs.put(a.key(), a.value()));
        assertEquals("agent", attrs.get(Telemetry.ATTR_EVENT_SOURCE));
        assertEquals("agent.plan", attrs.get(Telemetry.ATTR_EVENT_TYPE));
        assertEquals("run-7", attrs.get(Telemetry.ATTR_AGENT_TRACE_ID));
        assertFalse(attrs.getOrDefault(Telemetry.ATTR_ATTENTION_CLASS, "").isEmpty());
        assertFalse(attrs.getOrDefault(Telemetry.ATTR_DECISION_STATUS, "").isEmpty());
        assertEquals(5, attrs.size(), "no rule matched, so no rule name; nothing else: " + attrs);
        assertFalse(out.spans.toString().contains("ghp_supersecret"));
        assertFalse(out.spans.toString().contains("dev@example.com"));
    }

    @Test
    void aCriticalStepIsSentInsideItsSpanAndAFailedSendFailsBothSpansWithoutItsText() {
        Rule critical = new Rule("r", "sec", "", new Rule.Condition("github", "security_advisory", ""),
                new Rule.Outcome("critical", "bad", false), false, true);
        Capture out = new Capture();
        Fakes.RecordingProvider ok = new Fakes.RecordingProvider(new ArrayList<>());
        traced(out, List.of(critical), ok).handle(Fakes.event("c1", "security_advisory"));
        assertFalse(out.named("delivery.Send").failed());
        assertFalse(out.spans.stream().anyMatch(s -> s.name().equals("attention.Classify")), "a critical event never reaches attention, even as a span");
        Map<String, String> attrs = new java.util.HashMap<>();
        out.named("pipeline.Handle").attributes().forEach(a -> attrs.put(a.key(), a.value()));
        assertEquals("sent", attrs.get(Telemetry.ATTR_DECISION_STATUS));
        assertEquals("sec", attrs.get(Telemetry.ATTR_RULE_NAME));

        Capture failing = new Capture();
        Ports.NotificationProvider broken = new Ports.NotificationProvider() {
            @Override public void send(Ports.Notification n) { throw new Ports.PortException("smtp password hunter2 refused", null); }
            @Override public String name() { return "broken"; }
        };
        Pipeline p = traced(failing, List.of(critical), broken);
        assertThrows(Pipeline.PipelineException.class, () -> p.handle(Fakes.event("c2", "security_advisory")));
        assertTrue(failing.named("delivery.Send").failed());
        assertTrue(failing.named("pipeline.Handle").failed());
        assertFalse(failing.spans.toString().contains("hunter2"));
    }

    @Test
    void aRedeliveryIsAnsweredNotFailedAndAddsNoDecision() {
        Capture out = new Capture();
        Pipeline p = traced(out, List.of(), new Fakes.RecordingProvider(new ArrayList<>()));
        p.handle(Fakes.event("d1", "push"));
        out.spans.clear();
        assertThrows(Pipeline.DuplicateDeliveryException.class, () -> p.handle(Fakes.event("d1", "push")));
        Telemetry.SpanData handle = out.named("pipeline.Handle");
        assertFalse(handle.failed());
        assertTrue(handle.attributes().stream().noneMatch(a -> a.key().equals(Telemetry.ATTR_DECISION_STATUS)));
        assertEquals(Optional.empty(), out.spans.stream().filter(s -> s.name().equals("policy.Evaluate")).findFirst());
    }
}
