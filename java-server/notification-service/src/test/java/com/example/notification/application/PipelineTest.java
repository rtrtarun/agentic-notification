package com.example.notification.application;

import com.example.notification.application.Fakes.CountingBudget;
import com.example.notification.application.Fakes.CountingClassifier;
import com.example.notification.application.Fakes.CountingOutbox;
import com.example.notification.application.Fakes.CountingPreferences;
import com.example.notification.application.Fakes.RecordingProvider;
import com.example.notification.application.Fakes.RecordingRepository;
import com.example.notification.application.Ports.Adjustment;
import com.example.notification.domain.Event;
import com.example.notification.domain.Rule;
import com.example.notification.domain.Status;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** How the pipeline treats standard and background events, and how it fails. */
class PipelineTest {
    private final List<String> order = new ArrayList<>();
    private final RecordingProvider provider = new RecordingProvider(order);
    private final RecordingRepository repository = new RecordingRepository(order);
    private final CountingClassifier classifier = new CountingClassifier();
    private final CountingPreferences preferences = new CountingPreferences();
    private final CountingBudget budget = new CountingBudget();
    private final CountingOutbox outbox = new CountingOutbox();
    private Pipeline pipeline;

    @BeforeEach
    void setUp() {
        pipeline = new Pipeline(Fakes.engine(), provider, repository)
                .withDeduper(new MemoryDeduper(Duration.ofMinutes(10), Clock.systemUTC()))
                .withClassifier(classifier)
                .withPreferences(preferences)
                .withBudget(budget)
                .withOutbox(outbox);
    }

    @Test
    void aStandardEventIsQueuedAndEnqueued() {
        classifier.answer = "standard";
        Event result = pipeline.handle(Fakes.pullRequest("e1", 1));

        assertEquals("standard", result.getAttentionClass());
        assertEquals(Status.QUEUED, result.getDecisionState().getStatus());
        assertEquals("default", result.getDecisionState().getMatchedRule());
        assertEquals(1, outbox.enqueued);
        assertEquals(1, repository.saved.size());
        assertTrue(provider.sent.isEmpty(), "standard events are not sent inline");
    }

    @Test
    void aRepeatIsMergedIntoTheFirstAndNotEnqueued() {
        classifier.answer = "standard";
        pipeline.handle(Fakes.pullRequest("e1", 1));
        Event repeat = pipeline.handle(Fakes.pullRequest("e2", 1));

        assertEquals(Status.MERGED, repeat.getDecisionState().getStatus());
        assertEquals("e1", repeat.getMergedInto());
        assertEquals("duplicate of e1", repeat.getDecisionState().getReason());
        assertEquals(1, outbox.enqueued);
    }

    @Test
    void aRedeliveredEventIdIsRejected() {
        pipeline.handle(Fakes.pullRequest("e1", 1));
        assertThrows(Pipeline.DuplicateDeliveryException.class, () -> pipeline.handle(Fakes.pullRequest("e1", 1)));
    }

    @Test
    void theClassifierMayOnlyDemoteAStandardEventToBackground() {
        Event demoted = pipeline.handle(Fakes.pullRequest("e1", 1));
        assertEquals("background", demoted.getAttentionClass());
        assertEquals("test-model", demoted.getDecisionState().getModelVersion());
        assertTrue(demoted.getDecisionState().getReason().contains("attention answered background"));

        classifier.answer = "critical";
        Event notEscalated = pipeline.handle(Fakes.pullRequest("e2", 2));
        assertEquals("standard", notEscalated.getAttentionClass(), "AI can never escalate to critical");
    }

    @Test
    void anExplicitRuleIsNeverSecondGuessedAndBackgroundRulesSkipTheClassifier() {
        Event release = pipeline.handle(Fakes.event("e1", "release"));
        Event star = pipeline.handle(Fakes.event("e2", "star"));

        assertEquals("standard", release.getAttentionClass());
        assertEquals("background", star.getAttentionClass());
        assertEquals(0, classifier.calls);
    }

    @Test
    void aClassifierFailureKeepsTheRulesDecision() {
        classifier.fail = true;
        Event result = pipeline.handle(Fakes.pullRequest("e1", 1));

        assertEquals("standard", result.getAttentionClass());
        assertEquals(Status.QUEUED, result.getDecisionState().getStatus());
    }

    @Test
    void anEventNoRuleMatchesIsStandardAndAdaptive() {
        Event other = new Event("e1", "gitlab", "push", "x/y", null);
        Event result = pipeline.handle(other);

        assertEquals("no matching rule, default standard", result.getDecisionState().getReason() .split(";")[0]);
        assertEquals("background", result.getAttentionClass(), "adaptive, so the classifier ran");
    }

    @Test
    void preferencesCanMuteOrDelayANonCriticalEvent() {
        classifier.answer = "standard";
        preferences.answer = new Adjustment("background", Status.DELAYED, "quiet hours until 08:00");
        Event result = pipeline.handle(Fakes.pullRequest("e1", 1));

        assertEquals("background", result.getAttentionClass());
        assertEquals(Status.DELAYED, result.getDecisionState().getStatus());
        assertEquals("quiet hours until 08:00", result.getDecisionState().getReason());
        assertEquals(0, outbox.enqueued);
        assertEquals(0, budget.calls, "a held event does not use the budget");
    }

    @Test
    void whenTheBudgetIsUsedUpAStandardEventIsHeldForTheDigest() {
        classifier.answer = "standard";
        budget.allow = false;
        Event result = pipeline.handle(Fakes.pullRequest("e1", 1));

        assertEquals(Status.BUDGET_EXCEEDED, result.getDecisionState().getStatus());
        assertTrue(result.getDecisionState().getReason().contains("10 of 10"));
        assertEquals(0, outbox.enqueued);
    }

    @Test
    void theBudgetIsOnlyUsedWhenTheEventWouldReallyBeDelivered() {
        classifier.answer = "standard";
        outbox.routedStandard = false;
        pipeline.handle(Fakes.pullRequest("e1", 1));
        assertEquals(0, budget.calls);

        outbox.routedStandard = true;
        budget.fail = true;
        Event result = pipeline.handle(Fakes.pullRequest("e2", 2));
        assertEquals(Status.QUEUED, result.getDecisionState().getStatus(), "a failing budget store lets the event through");
    }

    @Test
    void backgroundEventsAreDigestedWhenNoChannelIsRoutedAndQueuedWhenOneIs() {
        outbox.routedBackground = false;
        assertEquals(Status.DIGEST, pipeline.handle(Fakes.event("e1", "star")).getDecisionState().getStatus());

        outbox.routedBackground = true;
        // A different action, so dedup does not merge it into the first star.
        Event another = Fakes.event("e2", "star", new java.util.LinkedHashMap<>(java.util.Map.of("action", "deleted")));
        assertEquals(Status.QUEUED, pipeline.handle(another).getDecisionState().getStatus());
    }

    @Test
    void withoutAnOutboxBackgroundIsDigestedAndStandardIsStillQueued() {
        Pipeline bare = new Pipeline(Fakes.engine(), provider, repository);
        assertEquals(Status.DIGEST, bare.handle(Fakes.event("e1", "star")).getDecisionState().getStatus());
        assertEquals(Status.QUEUED, bare.handle(Fakes.event("e2", "release")).getDecisionState().getStatus());
    }

    @Test
    void aQueueFailureNeverFailsTheEvent() {
        classifier.answer = "standard";
        outbox.fail = true;
        Event result = pipeline.handle(Fakes.pullRequest("e1", 1));

        assertEquals(Status.QUEUED, result.getDecisionState().getStatus());
        assertEquals(1, repository.saved.size());
    }

    @Test
    void aFailedSaveReleasesTheDedupKeySoTheRetryIsNotLost() {
        classifier.answer = "standard";
        repository.failSave = true;
        assertThrows(Pipeline.PipelineException.class, () -> pipeline.handle(Fakes.pullRequest("e1", 1)));

        repository.failSave = false;
        Event retry = pipeline.handle(Fakes.pullRequest("e1b", 1));
        assertEquals(Status.QUEUED, retry.getDecisionState().getStatus(), "not merged into the event that was never stored");
    }

    @Test
    void aFailedCriticalDeliveryReleasesTheDedupKeyToo() {
        provider.fail = true;
        assertThrows(Pipeline.PipelineException.class, () -> pipeline.handle(Fakes.event("c1", "security_advisory")));
        provider.fail = false;
        Event retry = pipeline.handle(Fakes.event("c2", "security_advisory"));
        assertEquals(Status.SENT, retry.getDecisionState().getStatus());
    }

    @Test
    void aBrokenDeduperFailsOpen() {
        Dedup.Deduper broken = new Dedup.Deduper() {
            @Override public Dedup.Result check(Event event) { throw new IllegalStateException("redis down"); }
            @Override public void forget(String key, String id) { throw new IllegalStateException("redis down"); }
        };
        Pipeline p = new Pipeline(Fakes.engine(), provider, repository).withDeduper(broken);
        Event result = p.handle(Fakes.event("e1", "release"));
        assertEquals(Status.QUEUED, result.getDecisionState().getStatus());

        repository.failSave = true;
        assertThrows(Pipeline.PipelineException.class, () -> p.handle(Fakes.event("e2", "release")));
    }

    @Test
    void rulesCanBeReplacedWhileRunning() {
        PolicyEngine engine = Fakes.engine();
        Pipeline p = new Pipeline(engine, provider, repository);
        assertEquals("standard", p.handle(Fakes.event("e1", "release")).getAttentionClass());

        engine.setRules(List.of(new Rule("9", "all-critical", "", new Rule.Condition("", "", ""),
                new Rule.Outcome("critical", "everything matters", false), false, true)));
        assertEquals("critical", p.handle(Fakes.event("e2", "release")).getAttentionClass());
    }
}
