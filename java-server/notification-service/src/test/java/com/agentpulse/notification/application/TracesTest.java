package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.DecisionState;
import com.agentpulse.notification.domain.Event;
import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The same fixtures and expectations as the Go service's {@code pkg/traces/traces_test.go}, plus the JSON shape. */
class TracesTest {
    private static final Instant BASE = Instant.parse("2026-03-01T12:00:00Z");

    private static Event mk(String id, String parent, String type, String kind, int at, String cls, Map<String, Object> md) {
        Map<String, Object> metadata = md == null ? new HashMap<>() : new HashMap<>(md);
        metadata.put("step_kind", kind);
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("metadata", metadata);
        Event e = new Event(id, "agent", type, "", payload);
        e.setTraceId("run-1");
        e.setParentEventId(parent);
        e.setAgentName("researcher");
        e.setAttentionClass(cls);
        e.setCreatedAt(BASE.plusSeconds(at));
        DecisionState d = new DecisionState("rule " + id, "");
        d.setStatus("queued");
        e.setDecisionState(d);
        return e;
    }

    private static List<Event> failingRun() {
        return new ArrayList<>(List.of(
                mk("s1", "", "agent.plan", "plan", 0, "background", Map.of("duration_ms", 120, "tokens_in", 500, "tokens_out", 80, "cost_usd", 0.004)),
                mk("s2", "s1", "tool.call.failed", "tool_call", 1, "standard", Map.of("tool_name", "search", "attempt", 1, "duration_ms", 30000, "confidence", 0.6)),
                mk("s3", "s2", "agent.retry", "retry", 2, "background", Map.of("attempt", 2, "duration_ms", 30000)),
                mk("s4", "s3", "agent.retry", "retry", 3, "background", Map.of("attempt", 3, "duration_ms", 30000)),
                mk("s5", "s4", "agent.reflect", "reflect", 4, "background", Map.of("tokens_in", "300", "tokens_out", 60, "cost_usd", "0.002")),
                mk("s6", "s5", "agent.complete", "complete", 5, "background", null)));
    }

    @Test
    void aTraceSummarisesItsStepsAndTotals() {
        Traces.Summary s = Traces.find(failingRun(), "run-1").orElseThrow().summary();
        assertEquals(6, s.steps());
        assertEquals(2, s.retries());
        assertEquals(1, s.failures());
        assertEquals("researcher", s.agentName());
        assertEquals(800, s.tokensIn());
        assertEquals(140, s.tokensOut());
        assertEquals(0.006, s.costUsd());
        assertEquals(90120, s.durationMs(), "the sum of step durations");
        assertEquals("standard", s.worstClass());
        assertEquals("complete", s.status());
        assertEquals(BASE, s.startedAt());
        assertEquals(BASE.plusSeconds(5), s.endedAt());
    }

    @Test
    void stepsAreOrderedWithTheirParentChainAndDecisions() {
        List<Event> events = failingRun();
        Event first = events.get(0);
        events.set(0, events.get(3));
        events.set(3, first); // arrival order must not matter when the times differ
        Traces.Trace t = Traces.find(events, "run-1").orElseThrow();
        assertEquals(List.of("s1", "s2", "s3", "s4", "s5", "s6"), t.stepList().stream().map(Traces.Step::eventId).toList());
        for (int i = 0; i < t.stepList().size(); i++) {
            assertEquals(i, t.stepList().get(i).depth(), "a straight chain");
        }
        Traces.Step s2 = t.stepList().get(1);
        assertEquals("search", s2.toolName());
        assertEquals(1, s2.attempt());
        assertEquals(0.6, s2.confidence());
        assertEquals("rule s2", s2.decisionReason());
        assertEquals("queued", s2.decisionStatus());
        assertNull(t.stepList().get(0).confidence(), "a step with no confidence must not invent one");
    }

    @Test
    void statusFollowsTheLastStep() {
        Map<String, Event> cases = new LinkedHashMap<>();
        cases.put("complete", mk("x", "", "agent.complete", "complete", 9, "background", null));
        cases.put("waiting", mk("x", "", "agent.approval", "human_approval", 9, "standard", null));
        cases.put("failed", mk("x", "", "tool.call.failed", "tool_call", 9, "standard", null));
        cases.put("failed ", mk("x", "", "run.error", "tool_call", 9, "standard", null));
        cases.put("failed  ", mk("x", "", "job_failed", "tool_call", 9, "standard", null));
        cases.put("failed   ", mk("x", "", "Step.FAILURE", "tool_call", 9, "standard", null));
        cases.put("running", mk("x", "", "tool.call.started", "tool_call", 9, "background", null));
        cases.put("running ", mk("x", "", "agent.retry", "retry", 9, "background", null));
        cases.forEach((want, last) ->
                assertEquals(want.strip(), Traces.find(List.of(last), "run-1").orElseThrow().summary().status(), last.getEventType()));
    }

    @Test
    void worstClassIsCriticalWhenAnyStepIs() {
        List<Event> events = failingRun();
        events.get(2).setAttentionClass("critical");
        assertEquals("critical", Traces.find(events, "run-1").orElseThrow().summary().worstClass());
        Event quiet = mk("a", "", "agent.plan", "plan", 0, "background", null);
        assertEquals("background", Traces.find(List.of(quiet), "run-1").orElseThrow().summary().worstClass());
    }

    @Test
    void groupSeparatesTracesIgnoresOtherEventsAndSortsNewestFirst() {
        Event old = mk("o1", "", "agent.plan", "plan", 0, "background", null);
        old.setTraceId("old-run");
        Event recent = mk("r1", "", "agent.plan", "plan", 100, "background", null);
        recent.setTraceId("new-run");
        Event webhook = new Event("w", "github", "push", "", Map.of());
        webhook.setCreatedAt(BASE.plusSeconds(500));
        List<Traces.Trace> got = Traces.group(List.of(old, webhook, recent));
        assertEquals(List.of("new-run", "old-run"), got.stream().map(t -> t.summary().traceId()).toList());
        assertTrue(Traces.group(List.of(webhook)).isEmpty(), "an event with no trace id is not a trace");
    }

    /** The tie-breaking Go pins in TestTiesKeepTheOrderTheyArriveIn. */
    @Test
    void tiesKeepTheOrderTheyArriveIn() {
        Event a = mk("b-step", "", "agent.plan", "plan", 0, "background", null);
        Event b = mk("a-step", "", "agent.reflect", "reflect", 0, "background", null);
        Event c = mk("c-step", "", "agent.complete", "complete", 0, "background", null);
        Traces.Trace t = Traces.find(List.of(a, b, c), "run-1").orElseThrow();
        assertEquals(List.of("b-step", "a-step", "c-step"), t.stepList().stream().map(Traces.Step::eventId).toList());
        assertEquals("complete", t.summary().status());

        Event x = mk("x1", "", "agent.plan", "plan", 5, "background", null);
        x.setTraceId("zeta");
        Event y = mk("y1", "", "agent.plan", "plan", 5, "background", null);
        y.setTraceId("alpha");
        assertEquals(List.of("alpha", "zeta"), Traces.group(List.of(x, y)).stream().map(tr -> tr.summary().traceId()).toList());
    }

    @Test
    void findReportsAMissingTrace() {
        assertFalse(Traces.find(failingRun(), "nope").isPresent());
        assertFalse(Traces.find(failingRun(), "").isPresent(), "an empty id must not match events that have no trace");
        assertFalse(Traces.find(failingRun(), null).isPresent());
    }

    @Test
    void durationFallsBackToTheTimeSpanWhenStepsReportNone() {
        Event a = mk("a", "", "agent.plan", "plan", 0, "background", null);
        Event b = mk("b", "a", "agent.complete", "complete", 7, "background", null);
        assertEquals(7000, Traces.find(List.of(a, b), "run-1").orElseThrow().summary().durationMs());
    }

    @Test
    void aMalformedParentChainNeitherLoopsNorFails() {
        Event a = mk("a", "b", "agent.plan", "plan", 0, "background", null);
        Event b = mk("b", "a", "agent.reflect", "reflect", 1, "background", null);
        Event orphan = mk("c", "purged-event", "agent.retry", "retry", 2, "background", null);
        Traces.Trace t = Traces.find(List.of(a, b, orphan), "run-1").orElseThrow();
        assertEquals(3, t.summary().steps());
        assertEquals(0, t.stepList().get(2).depth(), "a step whose parent is not in the trace is a root");
        assertEquals(Traces.MAX_DEPTH, t.stepList().get(0).depth(), "a cycle stops at the cap, as in Go");
    }

    @Test
    void telemetryAcceptsNumbersAsStringsAndIgnoresJunk() {
        Map<String, Object> md = new HashMap<>();
        md.put("tokens_in", " 12 ");
        md.put("tokens_out", "lots");
        md.put("attempt", 2L);
        md.put("duration_ms", 5);
        md.put("cost_usd", null);
        md.put("tool_name", 7);
        Traces.Step s = Traces.find(List.of(mk("a", "", "agent.plan", "plan", 0, "background", md)), "run-1").orElseThrow().stepList().get(0);
        assertEquals(12, s.tokensIn());
        assertEquals(0, s.tokensOut());
        assertEquals(2, s.attempt());
        assertEquals(5, s.durationMs());
        assertEquals(0, s.costUsd());
        assertEquals("", s.toolName(), "a tool name that is not text is ignored");

        Event bare = new Event("b", "agent", "agent.plan", "", Map.of());
        bare.setTraceId("run-2");
        bare.setCreatedAt(BASE);
        Traces.Trace t = Traces.find(List.of(bare), "run-2").orElseThrow();
        assertEquals(1, t.summary().steps());
        assertEquals("", t.stepList().get(0).stepKind(), "an event with no metadata still forms a trace");
        assertEquals("", t.stepList().get(0).decisionStatus());
    }

    @Test
    void decimalTextParsesAsGoParseFloatDoesAndOtherTextIsZero() {
        Map<String, Object> m = new HashMap<>();
        m.put("a", "1.5e3");
        m.put("b", "-.25");
        m.put("c", "1d");
        m.put("d", "0x10");
        m.put("e", "");
        assertEquals(1500, Traces.num(m, "a"));
        assertEquals(-0.25, Traces.num(m, "b"));
        assertEquals(0, Traces.num(m, "c"), "Java's 1d suffix is not a Go number");
        assertEquals(0, Traces.num(m, "d"));
        assertEquals(0, Traces.num(m, "e"));
        assertEquals(0, Traces.num(m, "missing"));
    }

    @Test
    void costIsRoundedToSixPlacesWithGoRounding() {
        assertEquals(1, Traces.roundHalfAwayFromZero(0.5));
        assertEquals(-1, Traces.roundHalfAwayFromZero(-0.5));
        assertEquals(0, Traces.roundHalfAwayFromZero(0.49999999999999994));
        assertEquals(3, Traces.roundHalfAwayFromZero(2.5));
        Event a = mk("a", "", "agent.plan", "plan", 0, "background", Map.of("cost_usd", 0.1));
        Event b = mk("b", "a", "agent.plan", "plan", 1, "background", Map.of("cost_usd", 0.2));
        assertEquals(0.3, Traces.find(List.of(a, b), "run-1").orElseThrow().summary().costUsd(), "0.30000000000000004 is rounded");
    }

    @Test
    void theJsonMatchesGoFieldForField() {
        Event a = mk("a", "", "agent.plan", "plan", 0, "standard", Map.of("confidence", 0));
        Event b = mk("b", "a", "agent.complete", "complete", 1, "standard", Map.of("cost_usd", 0.25, "confidence", 1));
        b.setDecisionState(null);
        b.setAgentName("");
        a.setAgentName("");
        Map<String, Object> json = Traces.find(List.of(a, b), "run-1").orElseThrow().toJson();
        assertFalse(json.containsKey("agentName"), "an empty agent name is left out");
        assertEquals(0.25, json.get("costUsd"));
        assertEquals(0L, Traces.find(List.of(a), "run-1").orElseThrow().summary().toJson().get("costUsd"), "Go writes 0, not 0.0");
        assertEquals(0L, json.get("tokensIn"), "summary counters are always present");
        assertEquals("2026-03-01T12:00:01Z", json.get("endedAt"));
        @SuppressWarnings("unchecked")
        List<Map<String, Object>> steps = (List<Map<String, Object>>) json.get("stepList");
        Map<String, Object> first = steps.get(0);
        assertEquals(0L, first.get("confidence"), "a confidence of zero is kept");
        for (String key : List.of("parentEventId", "toolName", "attempt", "durationMs", "tokensIn", "tokensOut", "costUsd")) {
            assertFalse(first.containsKey(key), key + " is left out when empty, as Go's omitempty");
        }
        assertEquals(0, first.get("depth"), "depth is always present");
        Map<String, Object> second = steps.get(1);
        assertEquals(0.25, second.get("costUsd"));
        assertEquals(1L, second.get("confidence"));
        assertFalse(second.containsKey("decisionStatus"));
        assertFalse(second.containsKey("decisionReason"));
        assertInstanceOf(Long.class, Traces.goNumber(2.0));
        assertInstanceOf(Double.class, Traces.goNumber(2.5));
    }
}
