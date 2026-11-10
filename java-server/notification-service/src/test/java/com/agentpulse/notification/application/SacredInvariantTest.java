package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Fakes.CountingBudget;
import com.agentpulse.notification.application.Fakes.CountingClassifier;
import com.agentpulse.notification.application.Fakes.CountingOutbox;
import com.agentpulse.notification.application.Fakes.CountingPreferences;
import com.agentpulse.notification.application.Fakes.RecordingProvider;
import com.agentpulse.notification.application.Fakes.RecordingRepository;
import com.agentpulse.notification.application.Ports.Adjustment;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.domain.Status;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * The sacred invariant, one independent test per guarantee. Every test arms every non-critical mechanism so it
 * would change a critical event if the pipeline ever let it: a classifier that demotes everything, preferences
 * that mute everything, a budget that denies everything, a queue, and a deduper. A critical event must be
 * decided by its rule alone and delivered inline.
 */
class SacredInvariantTest {
    private final List<String> order = new ArrayList<>();
    private final RecordingProvider provider = new RecordingProvider(order);
    private final RecordingRepository repository = new RecordingRepository(order);
    private final CountingClassifier classifier = new CountingClassifier();
    private final CountingPreferences preferences = new CountingPreferences();
    private final CountingBudget budget = new CountingBudget();
    private final CountingOutbox outbox = new CountingOutbox();

    /** A pipeline with everything armed against a critical event. */
    private Pipeline armed() {
        preferences.answer = new Adjustment("background", Status.DELAYED, "quiet hours");
        budget.allow = false;
        return new Pipeline(Fakes.engine(), provider, repository)
                .withDeduper(new MemoryDeduper(Duration.ofMinutes(10), Clock.systemUTC()))
                .withClassifier(classifier)
                .withPreferences(preferences)
                .withBudget(budget)
                .withOutbox(outbox);
    }

    @Test
    void criticalEventsNeverReachTheClassifier() {
        Event result = armed().handle(Fakes.event("c1", "security_advisory"));

        assertEquals("critical", result.getAttentionClass());
        assertEquals(0, classifier.calls, "a critical event must never be sent to AI");
        assertEquals("", result.getDecisionState().getModelVersion());
    }

    @Test
    void criticalEventsAreNeverMergedByDedup() {
        Pipeline pipeline = armed();
        Event first = pipeline.handle(Fakes.event("c1", "security_advisory"));
        Event repeat = pipeline.handle(Fakes.event("c2", "security_advisory"));

        assertEquals(Status.SENT, first.getDecisionState().getStatus());
        assertEquals(Status.SENT, repeat.getDecisionState().getStatus(), "an identical critical event is not a duplicate");
        assertEquals("", repeat.getMergedInto());
        assertEquals(2, provider.sent.size(), "both were delivered");
        assertFalse(repeat.getDedupKey().isEmpty(), "it is still tagged and threaded");
    }

    @Test
    void criticalEventsAreNeverMutedOrDelayedByPreferences() {
        Event result = armed().handle(Fakes.event("c1", "security_advisory"));

        assertEquals("critical", result.getAttentionClass(), "quiet hours must not demote it");
        assertEquals(Status.SENT, result.getDecisionState().getStatus());
        assertEquals(0, preferences.calls, "preferences are not even consulted");
    }

    @Test
    void criticalEventsAreNeverCountedOrHeldByTheBudget() {
        Event result = armed().handle(Fakes.event("c1", "security_advisory"));

        assertEquals(Status.SENT, result.getDecisionState().getStatus());
        assertEquals(0, budget.calls, "a critical event does not use up the budget, and a spent budget cannot hold it");
    }

    @Test
    void criticalEventsAreDeliveredInlineAndNeverQueued() {
        Pipeline pipeline = armed();
        Event result = pipeline.handle(Fakes.event("c1", "security_advisory"));

        assertEquals(0, outbox.enqueued, "a critical event never waits in a queue");
        assertEquals(1, outbox.direct, "it is only logged next to the queued ones, after the inline delivery");
        assertEquals(1, provider.sent.size());
        assertEquals(List.of("deliver", "save"), order, "delivered before it is stored, in the same call");
        assertEquals(Status.SENT, result.getDecisionState().getStatus());

        // If delivery fails, the failure is raised and nothing is stored as sent.
        provider.fail = true;
        assertThrows(Pipeline.PipelineException.class, () -> pipeline.handle(Fakes.event("c2", "security_advisory")));
        assertTrue(repository.saved.stream().noneMatch(e -> e.getId().equals("c2")));
    }

    @Test
    void aCriticalRuleCanNeverNarrowWhereACriticalEventGoes() {
        // Even a stored rule that wrongly carries a channel override cannot narrow a critical event.
        Rule bad = new Rule("1", "bad", "", new Rule.Condition("github", "security_advisory", ""),
                new Rule.Outcome("critical", "security", false, List.of("slack")), false, true);
        preferences.answer = new Adjustment("background", Status.DELAYED, "quiet hours");
        budget.allow = false;
        Pipeline pipeline = new Pipeline(new PolicyEngine(List.of(bad)), provider, repository)
                .withClassifier(classifier).withPreferences(preferences).withBudget(budget).withOutbox(outbox);

        Event result = pipeline.handle(Fakes.event("c9", "security_advisory"));

        assertTrue(result.getRouteChannels().isEmpty(), "no override survives on a critical event");
        assertEquals(0, outbox.enqueued);
        assertEquals(1, provider.sent.size(), "delivered inline to every configured channel");
        assertEquals(Status.SENT, result.getDecisionState().getStatus());
    }

    // ---- Milestone 35: the queue, Redis dedup and /notify ----------------------------------------------------------

    /** A Redis stand-in shared by "replicas", with a switch to take it down. */
    private static final class SharedRedis implements com.agentpulse.notification.infrastructure.RedisDeduper.KeyStore {
        final java.util.Map<String, String> data = new java.util.HashMap<>();
        boolean down;

        @Override public boolean setIfAbsent(String key, String value, Duration ttl) {
            check();
            return data.putIfAbsent(key, value) == null;
        }
        @Override public java.util.Optional<String> get(String key) {
            check();
            return java.util.Optional.ofNullable(data.get(key));
        }
        @Override public void deleteIfEquals(String key, String value) {
            check();
            data.remove(key, value);
        }
        private void check() {
            if (down) {
                throw new IllegalStateException("connection refused");
            }
        }
    }

    private Pipeline withRealQueueAndRedis(SharedRedis redis, com.agentpulse.notification.infrastructure.MemoryStores.Deliveries jobs) {
        Targets targets = new Targets(null, List.of("https://hooks.slack.com/services/x"), null);
        return new Pipeline(Fakes.engine(), provider, repository)
                .withDeduper(new com.agentpulse.notification.infrastructure.RedisDeduper(redis, Duration.ofMinutes(10)))
                .withClassifier(classifier)
                .withBudget(budget)
                .withOutbox(new QueueOutbox(jobs, targets, java.util.Map.of("standard", List.of("slack")), Clock.systemUTC()))
                .withCriticalTargets(targets);
    }

    @Test
    void criticalEventsAreNeverMergedByRedisDedupAndNeverEnterTheDurableQueue() {
        SharedRedis redis = new SharedRedis();
        var jobs = new com.agentpulse.notification.infrastructure.MemoryStores.Deliveries();
        Pipeline pipeline = withRealQueueAndRedis(redis, jobs);

        Event first = pipeline.handle(Fakes.event("c1", "security_advisory"));
        Event repeat = pipeline.handle(Fakes.event("c2", "security_advisory"));

        assertEquals(Status.SENT, first.getDecisionState().getStatus());
        assertEquals(Status.SENT, repeat.getDecisionState().getStatus(), "Redis says duplicate; a critical event ignores it");
        assertEquals("", repeat.getMergedInto());
        assertEquals(2, provider.sent.size());
        assertEquals(1, redis.data.size(), "Redis did record the key, as in the Go service");
        assertTrue(jobs.list(10).stream().allMatch(com.agentpulse.notification.application.Delivery.Job::direct),
                "the queue holds only the log of the inline deliveries");
        assertEquals(0, jobs.claim(java.time.Instant.now().plusSeconds(60), Duration.ofMinutes(1), 10).size(),
                "nothing the worker could ever send");
        assertEquals(0, classifier.calls);
        assertEquals(0, budget.calls);
    }

    @Test
    void aStandardEventIsStillQueuedAndMergedThroughTheSameRealParts() {
        classifier.answer = "standard";
        SharedRedis redis = new SharedRedis();
        var jobs = new com.agentpulse.notification.infrastructure.MemoryStores.Deliveries();
        Pipeline pipeline = withRealQueueAndRedis(redis, jobs);

        pipeline.handle(Fakes.pullRequest("e1", 1));
        Event repeat = pipeline.handle(Fakes.pullRequest("e2", 1));

        assertEquals(Status.MERGED, repeat.getDecisionState().getStatus());
        assertEquals(1, jobs.list(10).size(), "queued once; the merged repeat adds nothing");
    }

    @Test
    void whenRedisIsDownEventsFailOpenAndACriticalEventIsStillDeliveredAndThreaded() {
        SharedRedis redis = new SharedRedis();
        redis.down = true;
        var jobs = new com.agentpulse.notification.infrastructure.MemoryStores.Deliveries();
        Pipeline pipeline = withRealQueueAndRedis(redis, jobs);

        Event critical = pipeline.handle(Fakes.event("c1", "security_advisory"));
        assertEquals(Status.SENT, critical.getDecisionState().getStatus());
        assertFalse(critical.getThreadId().isEmpty(), "still threaded without Redis");
        assertFalse(critical.getDedupKey().isEmpty());

        classifier.answer = "standard";
        Event standard = pipeline.handle(Fakes.pullRequest("e1", 1));
        assertEquals(Status.QUEUED, standard.getDecisionState().getStatus(), "treated as unique, not lost");
    }

    @Test
    void aCriticalNotificationSentThroughNotifyIsDeliveredInlineAndTouchesNoQueueDedupOrAi() {
        var jobs = new com.agentpulse.notification.infrastructure.MemoryStores.Deliveries();
        Notify.Service service = new Notify.Service(new Notify.Limits(List.of("hooks.slack.com"), List.of()), provider);

        service.send(new Notify.Request(new Notify.Agent("pager-bot", "", "ops"), "[CRITICAL] database down", "all writes fail",
                List.of("oncall@example.com"), List.of("https://hooks.slack.com/services/x"), null, null));

        assertEquals(1, provider.sent.size(), "delivered synchronously, before send() returned");
        assertEquals(List.of("oncall@example.com"), provider.sent.get(0).targets().email());
        assertTrue(jobs.list(10).isEmpty(), "never in the durable queue");
        assertEquals(0, classifier.calls);
        assertEquals(0, budget.calls);
        assertEquals(0, preferences.calls);
        assertEquals(List.of("deliver"), order, "nothing was stored or deferred; the provider was the only step");
    }
}

