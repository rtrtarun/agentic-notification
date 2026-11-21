package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.domain.Predicate;
import com.agentpulse.notification.domain.Status;
import org.junit.jupiter.api.Test;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The rules and messages of the Go service's {@code pkg/agentevents}. */
class AgentEventsTest {

    private static AgentEvents.Input in(String id, String source, String type, String trace, String parent,
                                        String agent, String severity, Map<String, Object> md) {
        return new AgentEvents.Input(id, source, type, null, trace, parent, agent, severity, md, null);
    }

    private static AgentEvents.Input type(String eventType) {
        return in(null, null, eventType, null, null, null, null, null);
    }

    private static void refused(AgentEvents.Input input, String detail) {
        AgentEvents.InvalidException e = assertThrows(AgentEvents.InvalidException.class, () -> AgentEvents.normalize(input));
        assertEquals("invalid agent event: " + detail, e.getMessage());
    }

    @Test
    void aStepBecomesAnEventWithItsChainAndTheAgentsWordsAsData() {
        Map<String, Object> payload = new HashMap<>(Map.of("note", "kept", "severity", "overwritten"));
        AgentEvents.Input input = new AgentEvents.Input("s2", " Research.Bot ", " tool.call.failed ", "acme/api", "run-1",
                "s1", "researcher", " CRITICAL ", Map.of("step_kind", "tool_call", "attempt", 2), payload);
        Event e = AgentEvents.normalize(input);
        assertEquals("s2", e.getId());
        assertEquals("research.bot", e.getSource(), "trimmed and lower-cased");
        assertEquals("tool.call.failed", e.getEventType());
        assertEquals("acme/api", e.getRepository());
        assertEquals("run-1", e.getTraceId());
        assertEquals("s1", e.getParentEventId());
        assertEquals("researcher", e.getAgentName());
        assertEquals("critical", e.getPayload().get("severity"), "severity is stored as data, lower-cased");
        assertEquals("researcher", e.getPayload().get("agentName"));
        assertEquals("kept", e.getPayload().get("note"));
        assertEquals(Map.of("step_kind", "tool_call", "attempt", 2), e.getPayload().get("metadata"));
        assertTrue(payload.containsKey("severity") && payload.get("severity").equals("overwritten"), "the caller's map is not edited");
    }

    @Test
    void defaultsAreTheAgentSourceAndAGeneratedId() {
        Event e = AgentEvents.normalize(type("agent.plan"));
        assertEquals("agent", e.getSource());
        assertTrue(e.getId().matches("agent-[0-9a-f]{20}"), e.getId());
        assertEquals("", e.getTraceId());
        assertFalse(e.getPayload().containsKey("metadata"), "no metadata is added when none was sent");
        assertFalse(e.getPayload().containsKey("severity"));
        assertFalse(e.getPayload().containsKey("agentName"));
        assertTrue(!AgentEvents.normalize(type("agent.plan")).getId().equals(e.getId()), "each generated id is new");
    }

    @Test
    void goWhitespaceIsTrimmedIncludingNonBreakingSpaces() {
        assertEquals("agent.plan", AgentEvents.normalize(type(" agent.plan　\u0085")).getEventType());
        assertEquals("x", AgentEvents.goTrimSpace("\t\n\u000b\f\r x   "));
        assertEquals("​x", AgentEvents.goTrimSpace("​x"), "a zero-width space is not space to Go");
    }

    @Test
    void everyRefusalHasGosMessage() {
        refused(in(null, "My Agent", "a", null, null, null, null, null), "source must be lower-case letters, digits, dots, dashes or underscores");
        refused(in(null, "a".repeat(51), "a", null, null, null, null, null), "source must be lower-case letters, digits, dots, dashes or underscores");
        refused(type(""), "eventType is required, such as tool.call.failed");
        refused(type("has space"), "eventType is required, such as tool.call.failed");
        refused(type("a".repeat(101)), "eventType is required, such as tool.call.failed");
        refused(in("bad id", null, "a", null, null, null, null, null), "id may only contain letters, digits and . _ : - (at most 200 characters)");
        refused(in("a".repeat(201), null, "a", null, null, null, null, null), "id may only contain letters, digits and . _ : - (at most 200 characters)");
        refused(in(null, null, "a", "-run", null, null, null, null), "traceId may only contain letters, digits and . _ : - (at most 200 characters)");
        refused(in(null, null, "a", "run", "p/1", null, null, null), "parentEventId may only contain letters, digits and . _ : - (at most 200 characters)");
        refused(in(null, null, "a", null, "p1", null, null, null), "parentEventId needs a traceId");
        refused(in("s1", null, "a", "run", "s1", null, null, null), "an event cannot be its own parent");
        refused(in(null, null, "a", null, null, "x".repeat(101), null, null), "agentName and repository are too long");
        refused(new AgentEvents.Input(null, null, "a", "r".repeat(201), null, null, null, null, null, null), "agentName and repository are too long");
        refused(in(null, null, "a", null, null, null, "urgent", null), "severity must be info, low, medium, high or critical");
    }

    @Test
    void lengthsCountCharactersNotUtf16Units() {
        String hundredEmoji = "😀".repeat(100);
        assertEquals(hundredEmoji, AgentEvents.normalize(in(null, null, "a", null, null, hundredEmoji, null, null)).getAgentName());
    }

    @Test
    void metadataIsChecked() {
        Map<String, Object> many = new HashMap<>();
        for (int i = 0; i < 31; i++) {
            many.put("k" + i, i);
        }
        refused(in(null, null, "a", null, null, null, null, many), "metadata may have at most 30 keys");
        refused(in(null, null, "a", null, null, null, null, Map.of("step_kind", 3)),
                "metadata.step_kind must be plan, tool_call, retry, reflect, human_approval or complete");
        refused(in(null, null, "a", null, null, null, null, Map.of("duration_ms", -0.5)), "metadata.duration_ms must be a number of zero or more");
        refused(in(null, null, "a", null, null, null, null, Map.of("cost_usd", true)), "metadata.cost_usd must be a number of zero or more");
        refused(in(null, null, "a", null, null, null, null, Map.of("attempt", 1, "tokens_out", "9")), "metadata.tokens_out must be a number of zero or more");
        refused(in(null, null, "a", null, null, null, null, Map.of("confidence", -0.1)), "metadata.confidence must be a number from 0 to 1");
        refused(in(null, null, "a", null, null, null, null, Map.of("confidence", Double.NaN)), "metadata.confidence must be a number from 0 to 1");
        refused(in(null, null, "a", null, null, null, null, Map.of("note", "n".repeat(501))), "metadata.note is longer than 500 characters");
        Map<String, Object> fine = Map.of("step_kind", "complete", "confidence", 1, "tokens_in", 0, "note", "n".repeat(500), "tool_name", "x");
        assertEquals(fine, AgentEvents.normalize(in(null, null, "a", null, null, null, null, fine)).getPayload().get("metadata"));
    }

    @Test
    void anAgentCannotMakeItsOwnStepCriticalButARuleCan() {
        Fakes.RecordingProvider provider = new Fakes.RecordingProvider(new java.util.ArrayList<>());
        Pipeline noRule = new Pipeline(new PolicyEngine(List.of()), provider, new Fakes.RecordingRepository(new java.util.ArrayList<>()));
        Event said = noRule.handle(AgentEvents.normalize(in("a1", null, "tool.call.failed", "run", null, null, "critical", null)));
        assertEquals("standard", said.getAttentionClass(), "the agent's severity alone never sets the class");
        assertTrue(provider.sent.isEmpty(), "and it is not delivered inline");

        Rule critical = new Rule("r", "agent critical", "", new Rule.Condition("agent", "", "", List.of(new Predicate("severity", "eq", "critical"))),
                new Rule.Outcome("critical", "the agent reported a critical step", false), false, false);
        Pipeline withRule = new Pipeline(new PolicyEngine(List.of(critical)), provider, new Fakes.RecordingRepository(new java.util.ArrayList<>()));
        Event ruled = withRule.handle(AgentEvents.normalize(in("a2", null, "tool.call.failed", "run", null, null, "critical", null)));
        assertEquals("critical", ruled.getAttentionClass());
        assertEquals(Status.SENT, ruled.getDecisionState().getStatus());
        assertEquals(1, provider.sent.size(), "a rule-made critical step is delivered inline");
        assertEquals("run", ruled.getTraceId(), "the chain survives the pipeline");
    }
}
