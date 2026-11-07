package com.example.notification.application;

import com.example.notification.domain.Event;
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
