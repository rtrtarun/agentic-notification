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
import com.agentpulse.notification.infrastructure.FeedbackStores;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Overrides, feedback, and how the pipeline treats an override: the Go service's {@code pkg/overrides} and {@code pkg/feedback}. */
class FeedbackTest {
    private final Clock clock = Clock.fixed(Instant.parse("2026-11-10T10:00:00Z"), ZoneOffset.UTC);
    private final Overrides.Service overrides = new Overrides.Service(new FeedbackStores.MemoryOverrides(), clock);
    private final FeedbackStores.MemoryFeedback store = new FeedbackStores.MemoryFeedback();
    private final Map<String, Event> events = new HashMap<>();
    private final Feedback.Service feedback = new Feedback.Service(store, id -> Optional.ofNullable(events.get(id)), overrides, clock);

    private Event event(String id, String source, String type, String repo, String attentionClass) {
        Event e = new Event(id, source, type, repo, null);
        e.setAttentionClass(attentionClass);
        events.put(id, e);
        return e;
    }

    private static Overrides.Override override(String mode, String source, String type, String repo) {
        return new Overrides.Override("", source, type, repo, mode, "sam", "", null);
    }

    private static Feedback.Input input(String eventId, String action) {
        return new Feedback.Input(eventId, action, "", "");
    }

    // ---- overrides -----------------------------------------------------------------------------------------

    @Test
    void anOverrideMatchesBySourceTypeAndRepositoryPatternIgnoringCase() {
        overrides.upsert(override("never", "GitHub", "", "Acme/*"));
        assertTrue(overrides.decide(event("1", "github", "push", "acme/api", "standard")).isPresent());
        assertTrue(overrides.decide(event("2", "github", "push", "other/api", "standard")).isEmpty());
        assertTrue(overrides.decide(event("3", "gitlab", "push", "acme/api", "standard")).isEmpty());
        assertTrue(overrides.decide(event("4", "github", "push", "acme/api/deep", "standard")).isEmpty(), "* does not cross a slash");
    }

    @Test
    void theMostSpecificOverrideWinsAndAlwaysWinsATie() {
        overrides.upsert(override("never", "github", "", ""));
        overrides.upsert(override("always", "github", "push", ""));
        Event push = event("1", "github", "push", "acme/api", "standard");
        assertEquals("always", overrides.decide(push).orElseThrow().mode(), "type is more specific than source");
        assertEquals("always notify override for github / push", overrides.decide(push).orElseThrow().reason());

        overrides.upsert(override("never", "", "", "acme/api"));
        overrides.upsert(override("always", "", "push", ""));
        Event tie = event("2", "other", "push", "acme/api", "standard");
        assertEquals("always", overrides.decide(tie).orElseThrow().mode(), "equally specific: always wins");
    }

    @Test
    void anOverrideNeverAnswersForACriticalEvent() {
        overrides.upsert(override("never", "github", "", ""));
        assertTrue(overrides.decide(event("c", "github", "security_advisory", "acme/api", "critical")).isEmpty());
    }

    @Test
    void theSameScopeReplacesTheOldChoiceAndKeepsItsIdAndTime() {
        Overrides.Override first = overrides.upsert(override("never", "github", "push", ""));
        Overrides.Override again = overrides.upsert(override("always", "GITHUB", "Push", ""));
        assertEquals(1, overrides.list().size());
        assertEquals("always", overrides.list().get(0).mode());
        assertEquals(first.id(), overrides.list().get(0).id());
        assertNotNull(again);
    }

    @Test
    void overridesAreValidatedWithTheGoMessages() {
        assertEquals("invalid override: mode must be always or never",
                assertThrows(Overrides.InvalidException.class, () -> overrides.upsert(override("sometimes", "github", "", ""))).getMessage());
        assertEquals("invalid override: choose a source, an event type or a repository",
                assertThrows(Overrides.InvalidException.class, () -> overrides.upsert(override("never", " ", "", ""))).getMessage());
        assertEquals("invalid override: values must be at most 200 characters",
                assertThrows(Overrides.InvalidException.class, () -> overrides.upsert(override("never", "x".repeat(201), "", ""))).getMessage());
        assertEquals("invalid override: the repository pattern is malformed",
                assertThrows(Overrides.InvalidException.class, () -> overrides.upsert(override("never", "", "", "acme/["))).getMessage());
    }

    @Test
    void deleteRemovesAnOverrideAndAnUnknownIdIsNotFound() {
        Overrides.Override o = overrides.upsert(override("never", "github", "", ""));
        overrides.delete(o.id());
        assertTrue(overrides.list().isEmpty());
        assertThrows(Overrides.NotFoundException.class, () -> overrides.delete(o.id()));
    }

    @Test
    void aServiceLoadsWhatTheStoreAlreadyHolds() {
        FeedbackStores.MemoryOverrides shared = new FeedbackStores.MemoryOverrides();
        new Overrides.Service(shared, clock).upsert(override("never", "github", "", ""));
        assertEquals(1, new Overrides.Service(shared, clock).list().size(), "another replica sees the stored choice on start");
    }

    // ---- feedback ------------------------------------------------------------------------------------------

    @Test
    void neverOnAStandardEventCreatesAnOverrideScopedAsAsked() {
        event("e", "github", "pull_request", "acme/api", "standard");
        Feedback.Result type = feedback.record("sam", input("e", "never"), true);
        assertEquals("type", type.entry().scope());
        assertEquals("github", type.override().source());
        assertEquals("pull_request", type.override().eventType());
        assertEquals(type.override().id(), type.entry().overrideId());

        Feedback.Result repo = feedback.record("sam", new Feedback.Input("e", "never", "repository", ""), true);
        assertEquals("acme/api", repo.override().repository());
        assertEquals("", repo.override().source());

        Feedback.Result source = feedback.record("sam", new Feedback.Input("e", "always", "source", ""), true);
        assertEquals("github", source.override().source());
        assertEquals("", source.override().eventType());
        assertEquals(3, overrides.list().size());
    }

    @Test
    void neverOnACriticalEventIsRefusedButAlwaysAndOtherSignalsAreFine() {
        event("c", "github", "security_advisory", "acme/api", "critical");
        assertEquals("invalid feedback: critical events cannot be muted",
                assertThrows(Feedback.InvalidException.class, () -> feedback.record("sam", input("c", "never"), true)).getMessage());
        assertTrue(overrides.list().isEmpty());
        assertNotNull(feedback.record("sam", input("c", "dismissed"), true).entry());
        assertNotNull(feedback.record("sam", input("c", "always"), true).override());
    }

    @Test
    void aCallerWhoMayNotOverrideStillLeavesASignal() {
        event("e", "github", "push", "acme/api", "standard");
        Feedback.Result r = feedback.record("sam", input("e", "never"), false);
        assertTrue(r.overrideSkipped());
        assertNull(r.override());
        assertTrue(overrides.list().isEmpty());
        assertEquals(1, store.list(new Feedback.Filter("e", "sam", Instant.EPOCH, 10)).size(), "the learning layer still sees it");
    }

    @Test
    void feedbackWithoutAnOverridesServiceIsOnlyRecorded() {
        event("e", "github", "push", "acme/api", "standard");
        Feedback.Service plain = new Feedback.Service(store, id -> Optional.ofNullable(events.get(id)), null, clock);
        Feedback.Result r = plain.record("sam", input("e", "always"), true);
        assertTrue(r.overrideSkipped());
        assertFalse(plain.record("sam", input("e", "undo"), true).reverted());
    }

    @Test
    void undoRevertsOnlyThisPersonsLatestOverrideAndOnlyOnce() {
        event("e", "github", "push", "acme/api", "standard");
        feedback.record("sam", input("e", "never"), true);
        assertFalse(feedback.record("kim", input("e", "undo"), true).reverted(), "another person's choice is not theirs to undo");
        assertEquals(1, overrides.list().size());
        assertTrue(feedback.record("sam", input("e", "undo"), true).reverted());
        assertTrue(overrides.list().isEmpty());
        assertFalse(feedback.record("sam", input("e", "undo"), true).reverted(), "already undone");
    }

    @Test
    void undoWithoutPermissionOnlyRecordsTheSignal() {
        event("e", "github", "push", "acme/api", "standard");
        feedback.record("sam", input("e", "never"), true);
        assertFalse(feedback.record("sam", input("e", "undo"), false).reverted());
        assertEquals(1, overrides.list().size());
    }

    @Test
    void undoToleratesAnOverrideAlreadyRemovedByHand() {
        event("e", "github", "push", "acme/api", "standard");
        Feedback.Result r = feedback.record("sam", input("e", "never"), true);
        overrides.delete(r.override().id());
        assertTrue(feedback.record("sam", input("e", "undo"), true).reverted());
    }

    @Test
    void openedIsRecordedOncePerPersonAndEvent() {
        event("e", "github", "push", "acme/api", "standard");
        feedback.recordOpened("sam", "e");
        feedback.recordOpened("sam", "e");
        feedback.recordOpened("kim", "e");
        assertEquals(1, store.list(new Feedback.Filter("e", "sam", Instant.EPOCH, 10)).size());
        assertEquals(2, store.list(new Feedback.Filter("e", "", Instant.EPOCH, 10)).size());
    }

    @Test
    void theSignalCarriesTheEventsContextAndATrimmedReason() {
        event("e", "github", "pull_request", "acme/api", "standard");
        Feedback.Entry r = feedback.record("sam", new Feedback.Input("e", "reply", "", "  too noisy  "), true).entry();
        assertEquals("too noisy", r.reason());
        assertEquals("github", r.source());
        assertEquals("pull_request", r.eventType());
        assertEquals("acme/api", r.repository());
        assertEquals("sam", r.userId());
        assertEquals(clock.instant(), r.createdAt());
    }

    @Test
    void invalidAndUnknownRequestsAreRefused() {
        event("e", "github", "push", "acme/api", "standard");
        assertThrows(Feedback.InvalidException.class, () -> feedback.record("sam", input("e", "love"), true));
        assertThrows(Feedback.InvalidException.class, () -> feedback.record("sam", input("", "opened"), true));
        assertThrows(Feedback.InvalidException.class, () -> feedback.record("sam", new Feedback.Input("e", "never", "planet", ""), true));
        assertThrows(Feedback.InvalidException.class, () -> feedback.record("sam", new Feedback.Input("e", "opened", "source", ""), true));
        assertThrows(Feedback.InvalidException.class, () -> feedback.record("sam", new Feedback.Input("e", "reply", "", "  "), true));
        assertThrows(Feedback.InvalidException.class, () -> feedback.record("sam", new Feedback.Input("e", "reply", "", "x".repeat(1001)), true));
        assertThrows(Feedback.EventNotFoundException.class, () -> feedback.record("sam", input("nope", "opened"), true));
    }

    @Test
    void theMemoryStoreIsBoundedAndFiltersByEventPersonAndTime() {
        for (int i = 0; i < FeedbackStores.MAX_IN_MEMORY + 5; i++) {
            store.add(new Feedback.Entry("i" + i, "e" + (i % 2), "sam", "opened", "", "", "", "", "", "", Instant.EPOCH.plusSeconds(i)));
        }
        assertEquals(FeedbackStores.MAX_IN_MEMORY, store.list(new Feedback.Filter("", "", Instant.EPOCH, 0)).size());
        assertEquals("i" + (FeedbackStores.MAX_IN_MEMORY + 4), store.list(new Feedback.Filter("", "", Instant.EPOCH, 1)).get(0).id(), "newest first");
        assertTrue(store.list(new Feedback.Filter("", "kim", Instant.EPOCH, 5)).isEmpty());
        assertTrue(store.list(new Feedback.Filter("", "", Instant.EPOCH.plusSeconds(100_000), 5)).isEmpty());
    }

    // ---- the pipeline --------------------------------------------------------------------------------------

    private final List<String> order = new ArrayList<>();
    private final RecordingProvider provider = new RecordingProvider(order);
    private final RecordingRepository repository = new RecordingRepository(order);
    private final CountingClassifier classifier = new CountingClassifier();
    private final CountingPreferences preferences = new CountingPreferences();
    private final CountingBudget budget = new CountingBudget();
    private final CountingOutbox outbox = new CountingOutbox();

    private Pipeline pipeline() {
        return new Pipeline(Fakes.engine(), provider, repository)
                .withDeduper(new MemoryDeduper(Duration.ofMinutes(10), Clock.systemUTC()))
                .withClassifier(classifier).withPreferences(preferences).withBudget(budget).withOutbox(outbox)
                .withOverrides(overrides);
    }

    @Test
    void aNeverOverrideSendsTheEventToTheDigestAndSkipsJudgementAndTheBudget() {
        overrides.upsert(override("never", "github", "", ""));
        classifier.answer = "background";
        Event result = pipeline().handle(Fakes.pullRequest("p1", 1));

        assertEquals("background", result.getAttentionClass());
        assertEquals(Status.DIGEST, result.getDecisionState().getStatus());
        assertEquals("never notify override for github", result.getDecisionState().getReason());
        assertEquals(0, classifier.calls, "an explicit choice outranks judgement");
        assertEquals(0, budget.calls, "and is never counted against the budget");
        assertEquals(0, outbox.enqueued);
    }

    @Test
    void anAlwaysOverrideKeepsTheEventQueuedWhateverQuietHoursSay() {
        overrides.upsert(override("always", "github", "", ""));
        preferences.answer = new Adjustment("background", Status.DELAYED, "quiet hours");
        budget.allow = false;
        Event result = pipeline().handle(Fakes.pullRequest("p2", 2));

        assertEquals("standard", result.getAttentionClass());
        assertEquals(Status.QUEUED, result.getDecisionState().getStatus());
        assertEquals("always notify override for github", result.getDecisionState().getReason());
        assertEquals(1, outbox.enqueued);
        assertEquals(0, budget.calls);
    }

    @Test
    void withoutAnOverrideTheOtherMechanismsStillApply() {
        preferences.answer = new Adjustment("background", Status.DELAYED, "quiet hours");
        Event result = pipeline().handle(Fakes.pullRequest("p3", 3));
        assertEquals(Status.DELAYED, result.getDecisionState().getStatus());
    }
}
