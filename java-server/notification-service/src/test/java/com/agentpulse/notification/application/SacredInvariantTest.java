package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Fakes.CountingBudget;
import com.agentpulse.notification.application.Fakes.CountingClassifier;
import com.agentpulse.notification.application.Fakes.CountingOutbox;
import com.agentpulse.notification.application.Fakes.CountingPreferences;
import com.agentpulse.notification.application.Fakes.RecordingProvider;
import com.agentpulse.notification.application.Fakes.RecordingRepository;
import com.agentpulse.notification.application.Ports.Adjustment;
import com.agentpulse.notification.domain.Event;
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
        assertEquals(1, provider.sent.size());
        assertEquals(List.of("deliver", "save"), order, "delivered before it is stored, in the same call");
        assertEquals(Status.SENT, result.getDecisionState().getStatus());

        // If delivery fails, the failure is raised and nothing is stored as sent.
        provider.fail = true;
        assertThrows(Pipeline.PipelineException.class, () -> pipeline.handle(Fakes.event("c2", "security_advisory")));
        assertTrue(repository.saved.stream().noneMatch(e -> e.getId().equals("c2")));
    }
}
