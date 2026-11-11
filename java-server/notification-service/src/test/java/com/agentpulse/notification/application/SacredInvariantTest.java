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
import com.agentpulse.notification.infrastructure.MemoryStores;
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
    void anOverrideNeverTouchesACriticalEventAndFeedbackCannotMuteOne() {
        var overrides = new Overrides.Service(new com.agentpulse.notification.infrastructure.FeedbackStores.MemoryOverrides(), Clock.systemUTC());
        overrides.upsert(new Overrides.Override("", "github", "", "", "never", "sam", "", null));
        overrides.upsert(new Overrides.Override("", "github", "security_advisory", "", "never", "sam", "", null));
        preferences.answer = new Adjustment("background", Status.DELAYED, "quiet hours");
        budget.allow = false;
        Pipeline pipeline = armed().withOverrides(overrides);

        Event result = pipeline.handle(Fakes.event("c8", "security_advisory"));

        assertEquals("critical", result.getAttentionClass(), "a never override cannot demote a critical event");
        assertEquals("security vulnerability detected", result.getDecisionState().getReason(), "decided by its rule alone");
        assertEquals(Status.SENT, result.getDecisionState().getStatus());
        assertEquals(1, provider.sent.size(), "delivered inline");
        assertEquals(0, outbox.enqueued);

        // And the feedback path refuses to create such an override in the first place.
        var feedback = new Feedback.Service(new com.agentpulse.notification.infrastructure.FeedbackStores.MemoryFeedback(),
                id -> java.util.Optional.of(result), overrides, Clock.systemUTC());
        assertThrows(Feedback.InvalidException.class,
                () -> feedback.record("sam", new Feedback.Input(result.getId(), "never", "", ""), true));
    }

    @Test
    void aCriticalEventIsNeverClassifiedByTheRealHeuristicAndNeverAppearsInADigest() {
        Heuristic heuristic = new Heuristic();
        com.agentpulse.notification.infrastructure.InMemoryEventRepository real = new com.agentpulse.notification.infrastructure.InMemoryEventRepository(Clock.systemUTC());
        Pipeline pipeline = new Pipeline(Fakes.engine(), provider, real)
                .withClassifier(GuardedClassifier.of(heuristic)).withBudget(budget).withOutbox(outbox);
        budget.allow = false;

        Event critical = pipeline.handle(Fakes.event("c7", "security_advisory",
                new java.util.LinkedHashMap<>(java.util.Map.of("sender", java.util.Map.of("type", "Bot")))));

        assertEquals("critical", critical.getAttentionClass(), "a bot-sent, otherwise noisy advisory is still critical");
        assertEquals("", critical.getDecisionState().getModelVersion(), "the heuristic never judged it");
        assertEquals(0, budget.calls, "and it was never counted against the budget");
        assertEquals(Status.SENT, critical.getDecisionState().getStatus());
        assertThrows(IllegalArgumentException.class, () -> heuristic.classify(critical));

        // Even a stored critical event whose status says "digest" cannot be put in a digest.
        critical.getDecisionState().setStatus(Status.DIGEST);
        Digests.Generator generator = new Digests.Generator(() -> java.util.List.of(critical),
                new com.agentpulse.notification.infrastructure.DigestStores.Memory(), heuristic, heuristic, Clock.systemUTC());
        assertEquals(null, generator.generate("test"));
        assertTrue(heuristic.summarize(java.util.List.of(critical)).groups().isEmpty());
    }

    @Test
    void preferencesAndOverridesRefuseACriticalEventEvenIfCalledDirectly() {
        var overrides = new Overrides.Service(new com.agentpulse.notification.infrastructure.FeedbackStores.MemoryOverrides(), Clock.systemUTC());
        overrides.upsert(new Overrides.Override("", "github", "", "", "never", "sam", "", null));
        preferences.answer = new Adjustment("background", Status.DELAYED, "quiet hours");
        Pipeline pipeline = armed().withOverrides(overrides);
        Event critical = Fakes.event("c6", "security_advisory");
        critical.setAttentionClass("critical");
        critical.setDecisionState(new com.agentpulse.notification.domain.DecisionState("security", "advisory"));

        assertFalse(pipeline.applyPreferences(critical), "no override or preference decided a critical event");

        assertEquals("critical", critical.getAttentionClass());
        assertEquals("", critical.getDecisionState().getStatus(), "not delayed, not sent to the digest");
        assertEquals("security", critical.getDecisionState().getReason());
        assertEquals(0, preferences.calls, "quiet hours were never even consulted");
    }

    @Test
    void learningNeverLearnsFromCriticalEventsNorChangesACriticalOrLockedRule() {
        Clock clock = Clock.systemUTC();
        MemoryStores.Rules store = new MemoryStores.Rules();
        RulesService rules = new RulesService(store, new PolicyEngine(List.of()), List.of(
                new Rule("", "advisory", "", new Rule.Condition("github", "security_advisory", ""),
                        new Rule.Outcome("critical", "security", false), false, true),
                new Rule("", "default", "", new Rule.Condition("github", ""  , ""), new Rule.Outcome("standard", "r", false), false, false)));
        com.agentpulse.notification.infrastructure.FeedbackStores.MemoryFeedback fb = new com.agentpulse.notification.infrastructure.FeedbackStores.MemoryFeedback();
        com.agentpulse.notification.infrastructure.LearningStores.Memory repo = new com.agentpulse.notification.infrastructure.LearningStores.Memory();
        java.util.List<Event> events = new java.util.ArrayList<>();
        Learning.Service learning = new Learning.Service(repo, fb::list, () -> java.util.List.copyOf(events), Learning.ruleEditor(rules),
                o -> o, new Learning.Config(5, 2, null), clock);

        // Twenty people dismiss critical advisories. Nothing may be learned from that.
        for (int i = 0; i < 20; i++) {
            Event critical = new Event("c" + i, "github", "security_advisory", "acme/api", null);
            critical.setAttentionClass("critical");
            critical.setDecisionState(new com.agentpulse.notification.domain.DecisionState("security", "advisory"));
            events.add(critical);
            fb.add(new Feedback.Entry("f" + i, "c" + i, "sam", "dismissed", "", "", "", "github", "security_advisory", "acme/api", clock.instant()));
        }
        assertTrue(learning.review().isEmpty(), "feedback about critical events is never learned from");

        // A suggestion crafted against the locked critical rule is refused, and the rule is unchanged.
        Rule locked = rules.list().stream().filter(Rule::locked).findFirst().orElseThrow();
        repo.save(new Learning.Suggestion("crafted", "demote", "pending", "github", "security_advisory", "", locked.id(), locked.name(), "s", "p",
                new Learning.Evidence(20, 0, 0, 0, 0), clock.instant(), clock.instant(), "", null));
        assertThrows(Learning.GuardrailException.class, () -> learning.apply("crafted", "admin"));
        Rule after = rules.list().stream().filter(Rule::locked).findFirst().orElseThrow();
        assertEquals("critical", after.then().attentionClass());
        assertTrue(after.locked());
        assertEquals("pending", repo.get("crafted").status(), "refused, so it is still pending, not applied");
    }

    @Test
    void scoreTuningNeverLearnsFromCriticalEventsNorAdjustsACriticalOrLockedScope() {
        Clock clock = Clock.systemUTC();
        MemoryStores.Rules store = new MemoryStores.Rules();
        RulesService rules = new RulesService(store, new PolicyEngine(List.of()), List.of(
                new Rule("", "advisory", "", new Rule.Condition("github", "security_advisory", ""),
                        new Rule.Outcome("critical", "security", false), false, true),
                new Rule("", "default", "", new Rule.Condition("github", "", ""), new Rule.Outcome("standard", "r", true), false, false)));
        com.agentpulse.notification.infrastructure.FeedbackStores.MemoryFeedback fb = new com.agentpulse.notification.infrastructure.FeedbackStores.MemoryFeedback();
        com.agentpulse.notification.infrastructure.TuningStores.MemoryAdjustments adjustments = new com.agentpulse.notification.infrastructure.TuningStores.MemoryAdjustments();
        java.util.List<Event> events = new java.util.ArrayList<>();
        Tuning.Snapshot snapshot = new Tuning.Snapshot(java.time.Duration.ofHours(1), clock);
        Tuning.Tuner tuner = new Tuning.Tuner(adjustments, new com.agentpulse.notification.infrastructure.TuningStores.MemoryRuns(), fb::list,
                () -> java.util.List.copyOf(events), rules::list, snapshot, new Tuning.Config(5, 1, 20, null, null), clock);

        // Many people dismiss critical events. Nothing may be learned from that.
        for (int i = 0; i < 30; i++) {
            Event critical = new Event("c" + i, "github", "security_advisory", "acme/api", null);
            critical.setAttentionClass("critical");
            com.agentpulse.notification.domain.DecisionState state = new com.agentpulse.notification.domain.DecisionState("security", "advisory");
            state.setStatus("sent");
            critical.setDecisionState(state);
            events.add(critical);
            fb.add(new Feedback.Entry("f" + i, "c" + i, i % 2 == 0 ? "sam" : "alex", "dismissed", "", "", "", "github", "security_advisory", "acme/api", clock.instant()));
        }
        assertEquals(0, tuner.runNow().proposalsMade(), "feedback about critical events is never learned from");
        assertTrue(tuner.list("").isEmpty());

        // A proposal crafted against the locked critical rule's scope is refused, stays proposed, and nothing becomes active.
        adjustments.save(new Tuning.Adjustment("crafted", "github", "security_advisory", "", -20, "proposed", Tuning.Evidence.NONE,
                new Tuning.Impact(0, 0), 0, clock.instant(), clock.instant(), "", null));
        assertThrows(Tuning.GuardrailException.class, () -> tuner.approve("crafted", "admin"));
        assertEquals("proposed", adjustments.get("crafted").status());
        assertEquals(0, snapshot.size());

        // Even an active row for that scope is left out of the snapshot, and the heuristic refuses a critical event outright.
        adjustments.save(new Tuning.Adjustment("sneaky", "github", "security_advisory", "", -20, "active", Tuning.Evidence.NONE,
                new Tuning.Impact(0, 0), 1, clock.instant(), clock.instant(), "", null));
        tuner.refreshSnapshot();
        assertEquals(0, snapshot.size(), "a scope a critical or locked rule covers is never applied");
        snapshot.set(1, java.util.List.of(new Tuning.Applied("sneaky", "github", "security_advisory", "", -20, "s")));
        Event critical = new Event("x", "github", "security_advisory", "", null);
        critical.setAttentionClass("critical");
        assertThrows(IllegalArgumentException.class, () -> new Heuristic().withTuning(snapshot).classify(critical));
        assertThrows(IllegalArgumentException.class, () -> GuardedClassifier.of(new Heuristic().withTuning(snapshot)).classify(critical));
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

