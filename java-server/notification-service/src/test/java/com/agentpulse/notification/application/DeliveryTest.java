package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.infrastructure.MemoryStores;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The durable queue: enqueueing, claiming, retry backoff, dead letters and manual retry. */
class DeliveryTest {
    private static final class Manual extends Clock {
        Instant now = Instant.parse("2026-01-01T00:00:00Z");

        @Override public ZoneId getZone() { return ZoneId.of("UTC"); }
        @Override public Clock withZone(ZoneId zone) { return this; }
        @Override public Instant instant() { return now; }

        void advance(Duration d) { now = now.plus(d); }
    }

    private static final Targets TARGETS = new Targets(List.of("ops@x.com"), List.of("https://hooks.slack.com/services/SECRET"), null);

    private final Manual clock = new Manual();
    private final MemoryStores.Deliveries store = new MemoryStores.Deliveries();
    private final List<Notification> sent = new ArrayList<>();
    private boolean slackFails;
    private boolean slackPermanent;

    private Dispatcher dispatcher(Map<String, NotificationProvider> providers, int maxAttempts) {
        return new Dispatcher(store, TARGETS, providers, clock, maxAttempts, Duration.ofSeconds(30));
    }

    private Map<String, NotificationProvider> providers() {
        Map<String, NotificationProvider> m = new HashMap<>();
        m.put("slack", new NotificationProvider() {
            @Override public void send(Notification n) {
                if (slackPermanent) {
                    throw new Delivery.PermanentException("gone");
                }
                if (slackFails) {
                    throw new PortException("post webhook https://hooks.slack.com/services/SECRET failed", null);
                }
                sent.add(n);
            }
            @Override public String name() { return "slack"; }
        });
        m.put("email", new NotificationProvider() {
            @Override public void send(Notification n) { sent.add(n); }
            @Override public String name() { return "email"; }
        });
        return m;
    }

    private QueueOutbox outbox(Map<String, List<String>> routes) {
        return new QueueOutbox(store, TARGETS, routes, clock);
    }

    private static Event standard(String id) {
        Event e = new Event(id, "github", "pull_request", "acme/api", null);
        e.setAttentionClass("standard");
        e.setDecisionState(new com.agentpulse.notification.domain.DecisionState("default routing", "default"));
        return e;
    }

    @BeforeEach
    void setUp() {
        slackFails = false;
        slackPermanent = false;
    }

    @Test
    void anEventIsQueuedOncePerRoutedConfiguredChannelAndNeverTwice() {
        QueueOutbox outbox = outbox(Map.of("standard", List.of("slack", "email", "teams")));
        assertTrue(outbox.routed("standard"));
        assertFalse(outbox.routed("background"));

        outbox.enqueue(standard("e1"));
        outbox.enqueue(standard("e1"));

        List<Delivery.Job> jobs = store.list(10);
        assertEquals(2, jobs.size(), "teams has no target so it is skipped, and a repeat adds nothing");
        assertTrue(jobs.stream().allMatch(j -> j.status().equals("pending") && j.message().contains("Source: github")));
        assertTrue(jobs.stream().allMatch(j -> j.title().equals("[STANDARD] github/pull_request")));
    }

    @Test
    void aRoutedClassWithNoConfiguredChannelIsNotRouted() {
        assertFalse(new QueueOutbox(store, Targets.none(), Map.of("standard", List.of("slack")), clock).routed("standard"));
    }

    @Test
    void criticalEventsAreNeverQueuedOnlyLogged() {
        QueueOutbox outbox = outbox(Map.of("standard", List.of("slack"), "critical", List.of("slack")));
        Event critical = standard("c1");
        critical.setAttentionClass("critical");

        outbox.enqueue(critical);
        assertTrue(store.list(10).isEmpty(), "even a route table naming critical cannot queue it");

        outbox.recordDirect(critical, "webhook+email", null);
        outbox.recordDirect(critical, "webhook+email", new PortException("boom https://x.example/secret/path", null));
        List<Delivery.Job> log = store.list(10);
        assertEquals(1, log.size(), "one log entry per event and channel set; the second replaced the first");
        assertTrue(log.get(0).direct());
        assertEquals("dead", log.get(0).status());
        assertFalse(log.get(0).lastError().contains("secret"));
        assertEquals(0, dispatcher(providers(), 5).runOnce(), "a logged delivery is never sent by the worker");
    }

    @Test
    void aDueJobIsSentOnceWithOnlyItsChannelsTargetsAndMarkedSent() {
        outbox(Map.of("standard", List.of("slack"))).enqueue(standard("e1"));
        assertEquals(1, dispatcher(providers(), 5).runOnce());

        assertEquals(1, sent.size());
        assertEquals(List.of("https://hooks.slack.com/services/SECRET"), sent.get(0).targets().slack());
        assertTrue(sent.get(0).targets().email().isEmpty());
        assertEquals("sent", store.list(10).get(0).status());
        assertEquals(0, dispatcher(providers(), 5).runOnce(), "nothing is due any more");
    }

    @Test
    void aFailedJobIsRetriedWithDoublingBackoffThenDeadLettered() {
        outbox(Map.of("standard", List.of("slack"))).enqueue(standard("e1"));
        slackFails = true;
        Dispatcher d = dispatcher(providers(), 3);

        assertEquals(1, d.runOnce());
        Delivery.Job first = store.list(1).get(0);
        assertEquals("pending", first.status());
        assertEquals(1, first.attempts());
        assertEquals(clock.now.plusSeconds(30), first.nextAttemptAt(), "first backoff is the base");
        assertFalse(first.lastError().contains("SECRET"), "the webhook path is its credential and is never stored");

        assertEquals(0, d.runOnce(), "not due yet");
        clock.advance(Duration.ofSeconds(31));
        assertEquals(1, d.runOnce());
        assertEquals(clock.now.plusSeconds(60), store.list(1).get(0).nextAttemptAt(), "then it doubles");

        clock.advance(Duration.ofSeconds(61));
        assertEquals(1, d.runOnce());
        Delivery.Job dead = store.list(1).get(0);
        assertEquals("dead", dead.status(), "the third attempt used them all");
        assertEquals(3, dead.attempts());

        clock.advance(Duration.ofHours(5));
        assertEquals(0, d.runOnce(), "a dead job is not retried by itself");
    }

    @Test
    void aPermanentFailureIsDeadLetteredAtOnceAndAMissingProviderOrTargetToo() {
        outbox(Map.of("standard", List.of("slack"))).enqueue(standard("e1"));
        slackPermanent = true;
        dispatcher(providers(), 5).runOnce();
        assertEquals("dead", store.list(1).get(0).status());
        assertEquals(1, store.list(1).get(0).attempts());

        MemoryStores.Deliveries other = new MemoryStores.Deliveries();
        new QueueOutbox(other, TARGETS, Map.of("standard", List.of("email")), clock).enqueue(standard("e2"));
        new Dispatcher(other, TARGETS, Map.of(), clock, 5, null).runOnce();
        assertEquals("dead", other.list(1).get(0).status());
        assertTrue(other.list(1).get(0).lastError().contains("no provider"));

        MemoryStores.Deliveries third = new MemoryStores.Deliveries();
        new QueueOutbox(third, TARGETS, Map.of("standard", List.of("email")), clock).enqueue(standard("e3"));
        new Dispatcher(third, Targets.none(), providers(), clock, 5, null).runOnce();
        assertTrue(third.list(1).get(0).lastError().contains("not configured"));
    }

    @Test
    void aDeadJobCanBeRetriedByAPersonOnlyWhenItIsDead() {
        outbox(Map.of("standard", List.of("slack"))).enqueue(standard("e1"));
        String id = store.list(1).get(0).id();
        assertThrows(Delivery.NotRetryableException.class, () -> store.requeue(id, clock.now), "still pending");
        assertThrows(Delivery.NotFoundException.class, () -> store.requeue("nope", clock.now));

        slackPermanent = true;
        dispatcher(providers(), 5).runOnce();
        slackPermanent = false;
        store.requeue(id, clock.now);
        Delivery.Job again = store.list(1).get(0);
        assertEquals("pending", again.status());
        assertEquals(0, again.attempts(), "attempts are reset");

        dispatcher(providers(), 5).runOnce();
        assertEquals("sent", store.list(1).get(0).status());
        assertEquals(Map.of("sent", 1), store.counts());
    }

    @Test
    void aLeaseThatExpiresMakesTheJobClaimableAgain() {
        outbox(Map.of("standard", List.of("slack"))).enqueue(standard("e1"));
        List<Delivery.Job> first = store.claim(clock.now, Duration.ofMinutes(2), 10);
        assertEquals(1, first.size());
        assertTrue(store.claim(clock.now, Duration.ofMinutes(2), 10).isEmpty(), "leased to the first worker");

        clock.advance(Duration.ofMinutes(3));
        List<Delivery.Job> second = store.claim(clock.now, Duration.ofMinutes(2), 10);
        assertEquals(1, second.size());
        assertEquals(2, second.get(0).attempts(), "the crashed worker's attempt counts");
    }

    @Test
    void errorsAreRedactedToSchemeAndHostAndBounded() {
        assertEquals("post https://hooks.slack.com/… failed", Delivery.redact("post https://hooks.slack.com/services/T/B/SECRET failed"));
        assertEquals("see http://a.example/…", Delivery.redact("see http://a.example/p?q=1"));
        assertEquals("https://h.example", Delivery.redact("https://h.example"));
        assertEquals("", Delivery.redact(null));
        assertEquals(301, Delivery.redact("x".repeat(500)).length());
    }

    @Test
    void backoffDoublesUpToAnHour() {
        Dispatcher d = dispatcher(providers(), 5);
        assertEquals(Duration.ofSeconds(30), d.backoff(1));
        assertEquals(Duration.ofSeconds(120), d.backoff(3));
        assertEquals(Duration.ofHours(1), d.backoff(20));
    }

    @Test
    void theBackgroundDispatcherStartsStopsAndSendsDueJobs() throws Exception {
        outbox(Map.of("standard", List.of("slack"))).enqueue(standard("e1"));
        Dispatcher d = dispatcher(providers(), 5);
        d.start();
        d.start();
        long deadline = System.nanoTime() + Duration.ofSeconds(5).toNanos();
        while (!"sent".equals(store.list(1).get(0).status()) && System.nanoTime() < deadline) {
            Thread.sleep(20);
        }
        d.stop();
        d.stop();
        assertEquals("sent", store.list(1).get(0).status());
    }
}
