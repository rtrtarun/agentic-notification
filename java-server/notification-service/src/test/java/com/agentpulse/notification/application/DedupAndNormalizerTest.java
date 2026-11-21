package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneId;
import java.util.LinkedHashMap;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Dedup keys, threads, the in-memory window, and webhook normalisation. */
class DedupAndNormalizerTest {
    /** A clock the test moves by hand. */
    private static final class Manual extends Clock {
        Instant now = Instant.parse("2026-01-01T00:00:00Z");

        @Override public ZoneId getZone() { return ZoneId.of("UTC"); }
        @Override public Clock withZone(ZoneId zone) { return this; }
        @Override public Instant instant() { return now; }
    }

    private static Event pr(String id, Object number, String action) {
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("action", action);
        payload.put("pull_request", Map.of("number", number));
        return new Event(id, "github", "pull_request", "acme/api", payload);
    }

    @Test
    void theThreadGroupsEventsAboutOneSubjectAndTheKeyAddsTypeActionAndHead() {
        assertEquals("github:acme/api:42", Dedup.threadId(pr("a", 42, "opened")));
        assertEquals("github:acme/api:42|pull_request|opened|", Dedup.dedupKey(pr("a", 42, "opened")));
        assertEquals("github:acme/api:7", Dedup.threadId(new Event("b", "github", "issues", "acme/api",
                Map.of("issue", Map.of("number", 7)))));
        assertEquals("github:acme/api:9", Dedup.threadId(new Event("c", "github", "x", "acme/api", Map.of("number", 9))));
        assertEquals("github:acme/api", Dedup.threadId(new Event("d", "github", "push", "acme/api", Map.of())));
    }

    private static Event agentStep(String id, String run, String type, String parent, Object attempt) {
        Map<String, Object> metadata = new LinkedHashMap<>();
        metadata.put("step_kind", "retry");
        if (attempt != null) {
            metadata.put("attempt", attempt);
        }
        Event e = new Event(id, "agent", type, "", new LinkedHashMap<>(Map.of("metadata", metadata)));
        e.setTraceId(run);
        e.setParentEventId(parent);
        return e;
    }

    @Test
    void theStepsOfOneAgentRunShareAThreadAndKeyAsInGo() {
        // Found in slice 39d: Java ignored the run, so every agent step of one source shared a thread and merged.
        Event plan = agentStep("x", "run-1", "agent.plan", "", null);
        Event retry = agentStep("y", "run-1", "tool.call", "x", 2);
        Event otherRun = agentStep("z", "run-2", "agent.plan", "", null);
        assertEquals("agent:trace:run-1", Dedup.threadId(plan));
        assertEquals(Dedup.threadId(plan), Dedup.threadId(retry));
        assertEquals("agent:trace:run-2", Dedup.threadId(otherRun));
        // The exact text Go's DedupKey writes (TestAgentKeyTextIsStable), so Redis keys match across services.
        assertEquals("agent:trace:run-1|agent.plan||retry|<nil>", Dedup.dedupKey(plan));
        assertEquals("agent:trace:run-1|tool.call|x|retry|2", Dedup.dedupKey(retry));
        assertFalse(Dedup.dedupKey(retry).equals(Dedup.dedupKey(agentStep("w", "run-1", "tool.call", "x", 3))));
        Event noMetadata = new Event("v", "agent", "agent.plan", "", Map.of());
        noMetadata.setTraceId("run-1");
        assertEquals("agent:trace:run-1|agent.plan|||<nil>", Dedup.dedupKey(noMetadata));
    }

    @Test
    void numbersInKeysAreWrittenAsGoWritesAFloat64() {
        // Expected values printed by Go: fmt.Sprint of each JSON number decoded by encoding/json.
        Map<Object, String> go = new LinkedHashMap<>();
        go.put(3, "3");
        go.put(3.0, "3");
        go.put(2.5, "2.5");
        go.put(0, "0");
        go.put(-0.0, "-0");
        go.put(0.1, "0.1");
        go.put(100, "100");
        go.put(100000, "100000");
        go.put(999999, "999999");
        go.put(1000000, "1e+06");
        go.put(1234567, "1.234567e+06");
        go.put(123456789L, "1.23456789e+08");
        go.put(1234567.5, "1.2345675e+06");
        go.put(12345.678, "12345.678");
        go.put(1e20, "1e+20");
        go.put(1e21, "1e+21");
        go.put(1e300, "1e+300");
        go.put(12345678901234567L, "1.2345678901234568e+16");
        go.put(0.0001, "0.0001");
        go.put(0.00001, "1e-05");
        go.put(1.5e-7, "1.5e-07");
        go.put(4.000000000000001, "4.000000000000001");
        go.forEach((in, want) -> assertEquals(want, Dedup.goSprint(in), "fmt.Sprint(" + in + ")"));
        assertEquals("<nil>", Dedup.goSprint(null));
        assertEquals("x", Dedup.goSprint("x"));
        assertEquals("true", Dedup.goSprint(true));
        assertEquals("-2.5", Dedup.goSprint(-2.5));
        assertEquals("+Inf", Dedup.goSprint(Double.POSITIVE_INFINITY));
        assertEquals("-Inf", Dedup.goSprint(Double.NEGATIVE_INFINITY));
        assertEquals("NaN", Dedup.goSprint(Double.NaN));
    }

    @Test
    void pushesWithDifferentHeadsAreDifferentThings() {
        Event one = new Event("a", "github", "push", "acme/api", Map.of("after", "abc"));
        Event two = new Event("b", "github", "push", "acme/api", Map.of("after", "def"));
        assertFalse(Dedup.dedupKey(one).equals(Dedup.dedupKey(two)));
    }

    @Test
    void theMemoryDeduperMergesInsideTheWindowAndNotAfterIt() {
        Manual clock = new Manual();
        MemoryDeduper deduper = new MemoryDeduper(Duration.ofMinutes(10), clock);

        assertFalse(deduper.check(pr("a", 1, "opened")).duplicate());
        Dedup.Result repeat = deduper.check(pr("b", 1, "opened"));
        assertTrue(repeat.duplicate());
        assertEquals("a", repeat.originalId());

        clock.now = clock.now.plus(Duration.ofMinutes(11));
        assertFalse(deduper.check(pr("c", 1, "opened")).duplicate(), "the window has passed");
    }

    @Test
    void forgetOnlyRemovesTheEntryTheSameEventMade() {
        MemoryDeduper deduper = new MemoryDeduper(null, new Manual());
        Event first = pr("a", 1, "opened");
        deduper.check(first);

        deduper.forget(Dedup.dedupKey(first), "someone-else");
        assertTrue(deduper.check(pr("b", 1, "opened")).duplicate());

        deduper.forget(Dedup.dedupKey(first), "a");
        assertFalse(deduper.check(pr("c", 1, "opened")).duplicate());
    }

    @Test
    void theEventIdEmbedsTheDeliveryIdAndTheRepositoryComesFromThePayload() {
        Event e = GitHubNormalizer.normalize("pull_request", "72d3162e-cc78",
                Map.of("repository", Map.of("full_name", "octo/repo")));
        assertEquals("github-pull_request-72d3162e-cc78", e.getId());
        assertEquals("github", e.getSource());
        assertEquals("pull_request", e.getEventType());
        assertEquals("octo/repo", e.getRepository());
    }

    @Test
    void anUnusableDeliveryIdGetsARandomOneAndANullBodyIsEmpty() {
        Event a = GitHubNormalizer.normalize("push", "bad id!", null);
        Event b = GitHubNormalizer.normalize("push", null, null);
        assertTrue(a.getId().startsWith("github-push-"));
        assertFalse(a.getId().equals(b.getId()));
        assertEquals("", a.getRepository());
        assertTrue(a.getPayload().isEmpty());
    }

    @Test
    void eventNamesAndDeliveryIdsAreValidated() {
        assertTrue(GitHubNormalizer.validEventType("security_advisory"));
        assertFalse(GitHubNormalizer.validEventType(""));
        assertFalse(GitHubNormalizer.validEventType(null));
        assertFalse(GitHubNormalizer.validEventType("Push"));
        assertFalse(GitHubNormalizer.validEventType("a".repeat(65)));
        assertTrue(GitHubNormalizer.validDeliveryId("AbC-123"));
        assertFalse(GitHubNormalizer.validDeliveryId("a b"));
        assertFalse(GitHubNormalizer.validDeliveryId(""));
        assertFalse(GitHubNormalizer.validDeliveryId("a".repeat(65)));
    }
}
