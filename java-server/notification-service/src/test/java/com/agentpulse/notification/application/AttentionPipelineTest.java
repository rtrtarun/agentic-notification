package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Fakes.CountingOutbox;
import com.agentpulse.notification.application.Fakes.RecordingProvider;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Status;
import com.agentpulse.notification.infrastructure.DigestStores;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The real heuristic classifier, the real budget and real digests working inside the real pipeline. */
class AttentionPipelineTest {
    private final List<String> order = new ArrayList<>();
    private final RecordingProvider provider = new RecordingProvider(order);
    private final InMemoryEventRepository repository = new InMemoryEventRepository(Clock.systemUTC());
    private final CountingOutbox outbox = new CountingOutbox();
    private final Heuristic heuristic = new Heuristic();

    private Pipeline pipeline(Ports.Budget budget) {
        Pipeline p = new Pipeline(Fakes.engine(), provider, repository)
                .withDeduper(new MemoryDeduper(Duration.ofMinutes(10), Clock.systemUTC()))
                .withClassifier(GuardedClassifier.of(heuristic))
                .withOutbox(outbox);
        return budget == null ? p : p.withBudget(budget);
    }

    private static Map<String, Object> payload(Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < kv.length; i += 2) {
            m.put((String) kv[i], kv[i + 1]);
        }
        return m;
    }

    @Test
    void theHeuristicDemotesNoisyEventsOfAnAdaptiveRuleAndSaysWhy() {
        Event bot = pipeline(null).handle(Fakes.event("b1", "push", payload("sender", payload("login", "dependabot[bot]"))));

        assertEquals("background", bot.getAttentionClass());
        assertEquals("heuristic-v1", bot.getDecisionState().getModelVersion());
        assertTrue(bot.getDecisionState().getReason().endsWith("; attention scored 25: automated sender"), bot.getDecisionState().getReason());
        assertTrue(bot.getDecisionState().getConfidence() > 0.5);
        assertEquals(Status.QUEUED, bot.getDecisionState().getStatus());
    }

    @Test
    void anEventThatLooksImportantStaysStandardAndAnExplicitRuleIsNeverSecondGuessed() {
        Event review = pipeline(null).handle(Fakes.event("r1", "pull_request", payload("action", "review_requested")));
        assertEquals("standard", review.getAttentionClass());
        assertTrue(review.getDecisionState().getReason().contains("scored 75: someone asked for your review"));

        // The "explicit" rule is not adaptive, so a bot release is left exactly as the rule decided.
        Event release = pipeline(null).handle(Fakes.event("rel", "release", payload("sender", payload("type", "Bot"))));
        assertEquals("standard", release.getAttentionClass());
        assertEquals("", release.getDecisionState().getModelVersion());
    }

    @Test
    void aStarIsBackgroundByRuleAndNeverReachesTheClassifier() {
        Event star = pipeline(null).handle(Fakes.event("s1", "star", payload()));
        assertEquals("background", star.getAttentionClass());
        assertEquals("", star.getDecisionState().getModelVersion());
    }

    @Test
    void onceTheBudgetIsUsedStandardEventsAreHeldForTheDigestNeverDroppedAndCriticalIsUntouched() {
        Budgets.Memory budget = new Budgets.Memory(2, Duration.ofHours(1), Clock.systemUTC());
        Pipeline p = pipeline(budget);

        Event first = p.handle(Fakes.event("q1", "pull_request", payload("action", "review_requested", "number", 1)));
        Event second = p.handle(Fakes.event("q2", "pull_request", payload("action", "review_requested", "number", 2)));
        Event third = p.handle(Fakes.event("q3", "pull_request", payload("action", "review_requested", "number", 3)));
        assertEquals(Status.QUEUED, first.getDecisionState().getStatus());
        assertEquals(Status.QUEUED, second.getDecisionState().getStatus());
        assertEquals(Status.BUDGET_EXCEEDED, third.getDecisionState().getStatus());
        assertEquals("standard", third.getAttentionClass(), "held, not demoted");
        assertTrue(third.getDecisionState().getReason().contains("attention budget used (2 of 2 interruptions this hour), held for the next digest"),
                third.getDecisionState().getReason());
        assertEquals(2, outbox.enqueued, "the held event was not queued");

        Event critical = p.handle(Fakes.event("c1", "security_advisory", payload()));
        assertEquals(Status.SENT, critical.getDecisionState().getStatus(), "critical is delivered whatever has been spent");
        assertEquals(1, provider.sent.size());

        // The held event is in the next digest; the critical one never is.
        Digests.Generator generator = new Digests.Generator(() -> repository.listRecent(100), new DigestStores.Memory(), heuristic, heuristic,
                Clock.systemUTC());
        Digests.Digest digest = generator.generate("test");
        assertNotNull(digest);
        assertEquals(1, digest.count());
        assertEquals(List.of("q3"), digest.groups().get(0).eventIds());
        assertEquals("q3", digest.highlights().get(0).eventId());
    }

    @Test
    void aBudgetThatFailsLetsTheEventThrough() {
        Ports.Budget broken = () -> {
            throw new Ports.PortException("redis down", null);
        };
        Event e = pipeline(broken).handle(Fakes.event("f1", "pull_request", payload("action", "review_requested")));
        assertEquals(Status.QUEUED, e.getDecisionState().getStatus(), "an unavailable store must never suppress a notification");
    }
}
