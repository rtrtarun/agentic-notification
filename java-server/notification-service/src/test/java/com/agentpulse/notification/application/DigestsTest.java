package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Classification;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.domain.DecisionState;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Status;
import com.agentpulse.notification.infrastructure.DigestStores;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The attention budget, the guard around any classifier, and digests. */
class DigestsTest {
    private static final Instant T0 = Instant.parse("2026-11-10T10:00:00Z");
    private final Heuristic heuristic = new Heuristic();

    // ---- the guard -----------------------------------------------------------------------------------------

    private static Event event(String id, String type, String cls, String status, Instant at) {
        Event e = new Event(id, "github", type, "acme/api", null);
        e.setAttentionClass(cls);
        DecisionState s = new DecisionState("r", "rule");
        s.setStatus(status);
        e.setDecisionState(s);
        e.setCreatedAt(at);
        return e;
    }

    @Test
    void theGuardRefusesCriticalEventsAndAnythingButStandardOrBackground() {
        Ports.Classifier says = GuardedClassifier.of(e -> new Classification("critical", "m", 0.5, "x", 10));
        Event e = event("1", "push", "standard", "", T0);
        assertThrows(IllegalStateException.class, () -> says.classify(e), "a classifier cannot escalate to critical");
        assertThrows(IllegalStateException.class, () -> GuardedClassifier.of(x -> new Classification("urgent", "m", 0.5, "x", 10)).classify(e));
        assertThrows(IllegalStateException.class, () -> GuardedClassifier.of(x -> new Classification("standard", "m", 0.5, "x", 101)).classify(e));
        assertThrows(IllegalStateException.class, () -> GuardedClassifier.of(x -> new Classification("standard", "m", 1.5, "x", 50)).classify(e));
        assertThrows(IllegalStateException.class, () -> GuardedClassifier.of(x -> new Classification("standard", "m", 0.5, "x", -1)).classify(e));

        int[] calls = {0};
        Ports.Classifier counting = GuardedClassifier.of(x -> {
            calls[0]++;
            return new Classification("standard", "m", 0.5, "ok", 50);
        });
        Event critical = event("2", "security_advisory", "critical", "sent", T0);
        assertThrows(IllegalArgumentException.class, () -> counting.classify(critical));
        assertEquals(0, calls[0], "the wrapped classifier never saw the critical event");
        assertEquals("ok", counting.classify(e).reason());
        assertNull(GuardedClassifier.of(null));
    }

    // ---- budgets -------------------------------------------------------------------------------------------

    private static final class MutableClock extends Clock {
        Instant now = T0;

        @Override public ZoneOffset getZone() { return ZoneOffset.UTC; }
        @Override public Clock withZone(java.time.ZoneId zone) { return this; }
        @Override public Instant instant() { return now; }
    }

    @Test
    void theMemoryBudgetAllowsALimitPerRollingWindowThenHoldsAndFreesSlotsAsTimePasses() {
        MutableClock clock = new MutableClock();
        Budgets.Memory b = new Budgets.Memory(2, Duration.ofMinutes(10), clock);
        assertTrue(b.take().allowed());
        clock.now = T0.plusSeconds(60);
        Ports.BudgetResult second = b.take();
        assertTrue(second.allowed());
        assertEquals(2, second.used());
        assertEquals(2, second.limit());
        Ports.BudgetResult third = b.take();
        assertFalse(third.allowed());
        assertEquals(2, third.used());
        clock.now = T0.plus(Duration.ofMinutes(10)).plusSeconds(1);
        assertTrue(b.take().allowed(), "the first interruption has left the window");
        assertFalse(b.take().allowed() && b.take().allowed());
    }

    @Test
    void nonPositiveSettingsFallBackToTheDefaults() {
        Budgets.Memory b = new Budgets.Memory(0, Duration.ZERO, new MutableClock());
        for (int i = 0; i < Budgets.DEFAULT_PER_HOUR; i++) {
            assertTrue(b.take().allowed());
        }
        assertFalse(b.take().allowed());
    }

    @Test
    void theRedisBudgetUsesTheSharedKeyAndFailsAsAPortError() {
        List<String> keys = new ArrayList<>();
        Budgets.Redis ok = new Budgets.Redis((key, now, window, limit, member) -> {
            keys.add(key + "|" + window + "|" + limit + "|" + (member.length() == 16));
            return new Budgets.Slide(true, 4);
        }, 5, Duration.ofHours(1), new MutableClock());
        Ports.BudgetResult r = ok.take();
        assertTrue(r.allowed());
        assertEquals(4, r.used());
        assertEquals(5, r.limit());
        assertEquals(List.of("attention:budget:workspace|3600000|5|true"), keys, "the same key the Go service uses");

        Budgets.Redis down = new Budgets.Redis((k, n, w, l, m) -> { throw new IllegalStateException("redis down"); }, 5, null, new MutableClock());
        assertThrows(PortException.class, down::take);
    }

    // ---- digests -------------------------------------------------------------------------------------------

    private final DigestStores.Memory store = new DigestStores.Memory();
    private final MutableClock clock = new MutableClock();
    private final List<Event> events = new ArrayList<>();

    private Digests.Generator generator(Ports.Classifier classifier) {
        return new Digests.Generator(() -> List.copyOf(events), store, classifier, heuristic, clock);
    }

    @Test
    void aDigestCoversOnlyWhatWasHeldAndNeverCriticalOrMergedEvents() {
        events.add(event("held", "star", "background", Status.DIGEST, T0.plusSeconds(1)));
        events.add(event("delayed", "push", "standard", Status.DELAYED, T0.plusSeconds(2)));
        events.add(event("budget", "push", "standard", Status.BUDGET_EXCEEDED, T0.plusSeconds(3)));
        events.add(event("queued", "push", "standard", Status.QUEUED, T0.plusSeconds(4)));
        events.add(event("critical", "security_advisory", "critical", Status.DIGEST, T0.plusSeconds(5)));
        Event merged = event("merged", "push", "standard", Status.DIGEST, T0.plusSeconds(6));
        merged.setMergedInto("held");
        events.add(merged);
        events.add(new Event("nodecision", "github", "push", "", null));
        clock.now = T0.plusSeconds(60);

        Digests.Digest d = generator(heuristic).generate("sam");

        assertNotNull(d);
        assertEquals(3, d.count());
        assertEquals(List.of("held", "delayed", "budget"), d.groups().get(0).eventIds());
        assertEquals("sam", d.createdBy());
        assertEquals("heuristic-v1", d.model());
        assertEquals("heuristic", d.source());
        assertEquals("", d.userId());
        assertEquals(Digests.BEGINNING, d.from());
        assertEquals(T0.plusSeconds(60), d.to());
        assertEquals(1, store.list(10).size());
    }

    @Test
    void thereIsNothingToReportWhenNothingIsHeldAndTheNextDigestStartsWhereTheLastEnded() {
        assertNull(generator(heuristic).generate("sam"));
        assertTrue(store.list(10).isEmpty(), "nothing is stored when there is nothing to say");

        events.add(event("a", "push", "standard", Status.DIGEST, T0.plusSeconds(1)));
        clock.now = T0.plusSeconds(10);
        Digests.Generator g = generator(heuristic);
        Digests.Digest first = g.generate("sam");
        assertEquals(1, first.count());
        assertNull(g.generate("sam"), "the same event is not reported twice");

        events.add(event("b", "push", "standard", Status.DIGEST, T0.plusSeconds(20)));
        clock.now = T0.plusSeconds(30);
        Digests.Digest second = g.generate("schedule");
        assertEquals(first.to(), second.from());
        assertEquals(List.of("b"), second.groups().get(0).eventIds());
    }

    @Test
    void aDigestIsBoundedAndTheRestWaitForTheNextOne() {
        for (int i = 0; i < Digests.MAX_EVENTS + 20; i++) {
            events.add(event("e" + i, "push", "standard", Status.DIGEST, T0.plusMillis(i + 1)));
        }
        clock.now = T0.plusSeconds(60);
        Digests.Generator g = generator(null);
        Digests.Digest first = g.generate("sam");
        assertEquals(Digests.MAX_EVENTS, first.count());
        assertEquals(T0.plusMillis(Digests.MAX_EVENTS), first.to(), "the window moves up to the last event kept");
        assertEquals(20, g.generate("sam").count());
    }

    @Test
    void highlightsAreTheTopThreeAtOrAboveNeutralAndAFailingClassifierIsSkipped() {
        events.add(withPayload(event("star", "star", "background", Status.DIGEST, T0.plusSeconds(1)), Map.of()));
        events.add(withPayload(event("opened", "pull_request", "standard", Status.DIGEST, T0.plusSeconds(2)), Map.of("action", "opened")));
        events.add(withPayload(event("review", "pull_request", "standard", Status.DIGEST, T0.plusSeconds(3)), Map.of("action", "review_requested")));
        events.add(withPayload(event("failed", "check_run", "standard", Status.DIGEST, T0.plusSeconds(4)), Map.of("check_run", Map.of("conclusion", "failure"))));
        events.add(withPayload(event("comment", "issue_comment", "standard", Status.DIGEST, T0.plusSeconds(5)), Map.of()));
        events.add(withPayload(event("plain", "push", "standard", Status.DIGEST, T0.plusSeconds(6)), Map.of()));
        clock.now = T0.plusSeconds(60);

        Digests.Digest d = generator(heuristic).generate("sam");

        assertEquals(List.of("failed", "review", "opened"), d.highlights().stream().map(Digests.Highlight::eventId).toList(),
                "highest score first, three at most, ties in event order, the social star left out");
        assertEquals(80, d.highlights().get(0).score());
        assertEquals("github check_run in acme/api", d.highlights().get(0).title());
        assertEquals("scored 80: a check failed", d.highlights().get(0).reason());

        events.add(event("late", "push", "standard", Status.DIGEST, T0.plusSeconds(90)));
        clock.now = T0.plusSeconds(120);
        Digests.Digest broken = generator(e -> { throw new IllegalStateException("model down"); }).generate("sam");
        assertEquals(1, broken.count());
        assertTrue(broken.highlights().isEmpty());
    }

    private static Event withPayload(Event e, Map<String, Object> payload) {
        Event copy = new Event(e.getId(), e.getSource(), e.getEventType(), e.getRepository(), new java.util.LinkedHashMap<>(payload));
        copy.setAttentionClass(e.getAttentionClass());
        copy.setDecisionState(e.getDecisionState());
        copy.setCreatedAt(e.getCreatedAt());
        return copy;
    }

    // ---- the stored shape ----------------------------------------------------------------------------------

    @Test
    void theMemoryStoreListsWorkspaceDigestsNewestFirstAndFindsByIdOrSaysNotFound() {
        Digests.Digest a = digest("a", "", T0);
        Digests.Digest b = digest("b", "", T0.plusSeconds(1));
        Digests.Digest personal = digest("p", "sam", T0.plusSeconds(2));
        store.add(a);
        store.add(b);
        store.add(personal);
        assertEquals(List.of("b", "a"), store.list(10).stream().map(Digests.Digest::id).toList(), "personal digests are never listed");
        assertEquals(1, store.list(1).size());
        assertEquals("p", store.get("p").id());
        assertThrows(Digests.NotFoundException.class, () -> store.get("nope"));
    }

    private static Digests.Digest digest(String id, String user, Instant at) {
        return new Digests.Digest(id, at.minusSeconds(5), at, 1, "t", "s", List.of(new Digests.Group("k", "T", "S", 1, List.of("e"))),
                List.of(new Digests.Highlight("e", "title", "why", 60)), "heuristic-v1", user, "heuristic", "sam", at);
    }

    @Test
    void digestJsonMatchesWhatGoWritesAndReadsWhatGoWrote() throws Exception {
        ObjectMapper json = new ObjectMapper();
        Map<String, Object> m = Digests.toMap(digest("a", "", T0));
        assertFalse(m.containsKey("userId"), "a workspace digest has no userId");
        assertEquals(Digests.fromMap(m), digest("a", "", T0));

        Digests.Digest empty = new Digests.Digest("z", T0, T0, 0, "t", "s", List.of(), List.of(), "heuristic-v1", "", "heuristic", "x", T0);
        Map<String, Object> goShape = Digests.toMap(empty);
        assertNull(goShape.get("highlights"), "Go writes null, not [], for no highlights");
        assertNull(goShape.get("groups"));
        assertTrue(Digests.fromMap(goShape).highlights().isEmpty());

        // A digest exactly as the Go service stores it.
        String go = "{\"id\":\"g1\",\"from\":\"0001-01-01T00:00:00Z\",\"to\":\"2026-11-10T06:02:53.2696195Z\",\"count\":3,"
                + "\"title\":\"3 notifications held back\",\"summary\":\"s\",\"groups\":[{\"key\":\"github/acme/api\",\"title\":\"acme/api (github)\","
                + "\"summary\":\"x\",\"count\":2,\"eventIds\":[\"a\",\"b\"]}],\"highlights\":null,\"model\":\"heuristic-v1\",\"source\":\"heuristic\","
                + "\"createdBy\":\"api-key\",\"createdAt\":\"2026-11-10T06:02:53.2696195Z\"}";
        Digests.Digest read = Digests.fromMap(json.readValue(go, new TypeReference<Map<String, Object>>() {}));
        assertEquals(3, read.count());
        assertEquals(Digests.BEGINNING, read.from());
        assertEquals(List.of("a", "b"), read.groups().get(0).eventIds());
        assertTrue(read.highlights().isEmpty());
    }

    // ---- the scheduler -------------------------------------------------------------------------------------

    @Test
    void aZeroIntervalNeverRunsAndAFailedPassNeverStopsTheSchedule() {
        Digests.Scheduler never = new Digests.Scheduler(generator(null), Duration.ZERO);
        never.start();
        never.stop();

        Digests.Generator failing = new Digests.Generator(() -> { throw new IllegalStateException("db down"); }, store, null, heuristic, clock);
        new Digests.Scheduler(failing, Duration.ofHours(1)).runOnce();

        events.add(event("a", "push", "standard", Status.DIGEST, T0.plusSeconds(1)));
        clock.now = T0.plusSeconds(10);
        new Digests.Scheduler(generator(null), Duration.ofHours(1)).runOnce();
        assertEquals("schedule", store.list(1).get(0).createdBy());

        Digests.Scheduler real = new Digests.Scheduler(generator(null), Duration.ofHours(1));
        real.start();
        real.start();
        real.stop();
    }
}
