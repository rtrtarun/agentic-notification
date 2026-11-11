package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Classification;
import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.DecisionState;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.FeedbackStores;
import com.agentpulse.notification.infrastructure.MemoryStores;
import com.agentpulse.notification.infrastructure.TuningStores;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Offline score tuning: the same rule, proposals, approval, guardrails and heuristic effect as the Go service's
 * {@code pkg/learning/tuning.go} and {@code pkg/attention/tuning.go}. The numbers here are the ones Go's tests use.
 */
class TuningTest {
    private static final Instant T0 = Instant.parse("2026-11-10T10:00:00Z");
    private static final List<String> TWO = List.of("alice", "bob");

    private static final class MutableClock extends Clock {
        Instant now = T0;

        @Override public ZoneOffset getZone() { return ZoneOffset.UTC; }
        @Override public Clock withZone(java.time.ZoneId zone) { return this; }
        @Override public Instant instant() { return now; }
    }

    private final MutableClock clock = new MutableClock();
    private final TuningStores.MemoryAdjustments adjustments = new TuningStores.MemoryAdjustments();
    private final TuningStores.MemoryRuns runs = new TuningStores.MemoryRuns();
    private final FeedbackStores.MemoryFeedback feedback = new FeedbackStores.MemoryFeedback();
    private final List<Event> events = new ArrayList<>();
    private final Tuning.Snapshot snapshot = new Tuning.Snapshot(Duration.ofHours(1), clock);
    private List<Rule> ruleList = defaultRules();
    private Tuning.Tuner tuner = tuner(new Tuning.Config(0, 0, 0, null, null));
    private int serial;

    private static List<Rule> defaultRules() {
        return new ArrayList<>(List.of(
                new Rule("r-adv", "github-security-advisory", "", new Rule.Condition("github", "security_advisory", ""),
                        new Rule.Outcome("critical", "x", false), false, true),
                new Rule("r-def", "default-github", "", new Rule.Condition("github", "", ""), new Rule.Outcome("standard", "x", true), false, false)));
    }

    private Tuning.Tuner tuner(Tuning.Config cfg) {
        return new Tuning.Tuner(adjustments, runs, feedback::list, () -> List.copyOf(events), () -> List.copyOf(ruleList), snapshot, cfg, clock);
    }

    private Event signal(String user, String action, String type, String cls, String status, Duration age) {
        String id = "evt-" + (++serial);
        Event e = new Event(id, "github", type, "acme/api", Map.of());
        e.setAttentionClass(cls);
        DecisionState state = new DecisionState("r", "default-github");
        state.setStatus(status);
        e.setDecisionState(state);
        events.add(e);
        feedback.add(new Feedback.Entry("fb-" + id, id, user, action, "", "", "", "github", type, "acme/api", clock.now.minus(age)));
        return e;
    }

    private void signals(int n, List<String> users, String action, String type) {
        for (int i = 0; i < n; i++) {
            signal(users.get(i % users.size()), action, type, Attention.STANDARD, "queued", Duration.ofMinutes(1));
        }
    }

    private Tuning.Run run() {
        Tuning.Run r = tuner.runNow();
        assertEquals("done", r.status(), r.toString());
        return r;
    }

    private static Event pullRequest() {
        return new Event("p", "github", "pull_request", "", Map.of("action", "opened"));
    }

    private static Event star() {
        Event e = new Event("s", "github", "star", "acme/web", Map.of());
        e.setAttentionClass(Attention.STANDARD);
        return e;
    }

    // ---- the rule --------------------------------------------------------------------------------------------

    @Test
    void theRuleIsABoundedSmoothedOffset() {
        double[][] cases = {{0, 5, -14}, {5, 0, 14}, {0, 3, -12}, {3, 0, 12}, {4, 4, 0}, {2, 1, 4}, {0, 1000, -20}, {1000, 0, 20}, {0, 0, 0}};
        for (double[] c : cases) {
            assertEquals((int) c[2], Tuning.offset(c[0], c[1], 20), "offset(" + c[0] + ", " + c[1] + ")");
        }
        assertEquals(-5, Tuning.offset(0, 100, 5), "a smaller maximum bounds the offset");
    }

    @Test
    void halvesAreRoundedAwayFromZeroLikeGo() {
        assertEquals(-1.0, Tuning.roundHalfAway(-0.5));
        assertEquals(1.0, Tuning.roundHalfAway(0.5));
        assertEquals(-2.0, Tuning.roundHalfAway(-1.5));
        assertEquals(0.0, Tuning.roundHalfAway(0.4999999));
    }

    @Test
    void signalsLoseHalfTheirWeightEveryHalfLife() {
        Duration half = Duration.ofDays(14);
        assertEquals(1.0, Tuning.weight(Duration.ZERO, half));
        assertEquals(1.0, Tuning.weight(Duration.ofHours(-1), half), "a future-dated signal is not heavier");
        assertEquals(0.5, Tuning.weight(half, half));
        assertEquals(0.25, Tuning.weight(Duration.ofDays(28), half));
        assertEquals(0.997939, Tuning.weight(Duration.ofHours(1), half), "six decimals, the same as Go");
    }

    // ---- proposals -------------------------------------------------------------------------------------------

    @Test
    void enoughSignalsFromEnoughPeopleProposeABoundedNegativeAdjustment() {
        signals(5, TWO, "dismissed", "pull_request");
        Tuning.Run run = run();
        assertEquals(5, run.feedbackRead());
        assertEquals(5, run.eventsRead());
        assertEquals(1, run.proposalsMade());
        assertNotNull(run.finishedAt());

        List<Tuning.Adjustment> got = tuner.list("proposed");
        assertEquals(1, got.size());
        Tuning.Adjustment a = got.get(0);
        assertEquals("github", a.source());
        assertEquals("pull_request", a.eventType());
        assertEquals("", a.repository());
        assertEquals("proposed", a.status());
        assertEquals(0, a.version());
        assertEquals(Tuning.adjustmentId("github", "pull_request", ""), a.id());
        assertEquals(-14, a.delta());
        assertEquals(new Tuning.Evidence(0, 5, 2, 5, 30, "5 of 5 signals from 2 people were negative"), a.evidence());
    }

    @Test
    void opensAndRepliesProposeAPositiveAdjustmentThatProtects() {
        signals(3, TWO, "opened", "push");
        signals(3, TWO, "reply", "push");
        run();
        Tuning.Adjustment a = tuner.list("").get(0);
        assertTrue(a.delta() > 0);
        assertEquals(6, a.evidence().positive());
        assertTrue(a.evidence().summary().endsWith("were positive"));
    }

    @Test
    void onePersonCannotSteerItAndTooFewSignalsProposeNothing() {
        signals(40, List.of("alice"), "dismissed", "pull_request");
        run();
        assertTrue(tuner.list("").isEmpty(), "one person alone must not produce a proposal");
        events.clear();
        signals(4, TWO, "dismissed", "push");
        run();
        assertTrue(tuner.list("").isEmpty(), "four signals are below the default of five");
    }

    @Test
    void aPersonsInfluenceIsCapped() {
        signals(20, List.of("alice"), "dismissed", "pull_request");
        signals(1, List.of("bob"), "dismissed", "pull_request");
        run();
        // N = 3 (alice, capped) + about 1 (bob) = about 4: round(20 * -4 / 6) = -13, not the -19 that 21 raw signals would give.
        assertEquals(-13, tuner.list("").get(0).delta());
    }

    @Test
    void olderSignalsCountForLess() {
        for (int i = 0; i < 5; i++) {
            signal(TWO.get(i % 2), "dismissed", "pull_request", Attention.STANDARD, "queued", Duration.ofDays(28));
        }
        run();
        // Five signals, each weighing a quarter: N = 1.25, so round(20 * -1.25 / 3.25) = -8.
        assertEquals(-8, tuner.list("").get(0).delta());
    }

    @Test
    void onlyDeliveredEventsAreLearnedFrom() {
        for (String status : List.of("digest", "merged", "delayed", "budget_exceeded")) {
            for (int i = 0; i < 6; i++) {
                signal(TWO.get(i % 2), "dismissed", "pull_request", Attention.STANDARD, status, Duration.ofMinutes(1));
            }
        }
        run();
        assertTrue(tuner.list("").isEmpty(), "events held back or merged are not learned from");
        signals(5, TWO, "dismissed", "pull_request");
        signal("alice", "dismissed", "pull_request", Attention.BACKGROUND, "sent", Duration.ofMinutes(1));
        run();
        assertEquals(6, tuner.list("").get(0).evidence().signals(), "queued and sent events are");
    }

    @Test
    void feedbackAboutCriticalEventsIsNeverLearnedFrom() {
        for (int i = 0; i < 30; i++) {
            signal(TWO.get(i % 2), "dismissed", "pull_request", Attention.CRITICAL, "sent", Duration.ofMinutes(1));
        }
        assertEquals(0, run().proposalsMade());
        assertTrue(tuner.list("").isEmpty());
    }

    @Test
    void aScopeCoveredByALockedOrCriticalRuleIsNeverProposed() {
        signals(10, TWO, "dismissed", "security_advisory");
        run();
        assertTrue(tuner.list("").isEmpty(), "a locked critical rule's scope is never tuned");

        // An event decided by a locked rule is dropped even when its type is not covered.
        ruleList.add(new Rule("r-lock", "locked-push", "", new Rule.Condition("gitlab", "", ""), new Rule.Outcome("standard", "x", false), false, true));
        for (int i = 0; i < 8; i++) {
            Event e = signal(TWO.get(i % 2), "dismissed", "push", Attention.STANDARD, "queued", Duration.ofMinutes(1));
            e.getDecisionState().setMatchedRule("locked-push");
        }
        run();
        assertTrue(tuner.list("").isEmpty(), "events decided by a locked rule are not learned from");
    }

    @Test
    void undoAndUnknownEventsAreIgnored() {
        signals(10, TWO, "undo", "pull_request");
        feedback.add(new Feedback.Entry("orphan", "gone", "alice", "dismissed", "", "", "", "github", "pull_request", "", T0));
        run();
        assertTrue(tuner.list("").isEmpty());
    }

    @Test
    void runningAgainRefreshesTheProposalInsteadOfAddingAnother() {
        signals(5, TWO, "dismissed", "pull_request");
        run();
        Tuning.Adjustment first = tuner.list("").get(0);
        clock.now = clock.now.plus(Duration.ofHours(1));
        signals(4, TWO, "dismissed", "pull_request");
        run();
        List<Tuning.Adjustment> got = tuner.list("");
        assertEquals(1, got.size(), "a second run must update the proposal, not add one");
        assertEquals(first.id(), got.get(0).id());
        assertEquals(first.createdAt(), got.get(0).createdAt());
        assertTrue(got.get(0).updatedAt().isAfter(first.updatedAt()));
        assertEquals(9, got.get(0).evidence().signals());
        assertTrue(got.get(0).delta() < first.delta());
    }

    @Test
    void theSameFeedbackAlwaysGivesTheSameProposals() {
        signals(7, TWO, "dismissed", "pull_request");
        signals(6, List.of("alice", "bob", "carol"), "opened", "push");
        run();
        List<Tuning.Adjustment> a = tuner.list("");
        run();
        List<Tuning.Adjustment> b = tuner.list("");
        assertEquals(2, a.size());
        for (int i = 0; i < a.size(); i++) {
            assertEquals(a.get(i).id(), b.get(i).id());
            assertEquals(a.get(i).delta(), b.get(i).delta());
            assertEquals(a.get(i).evidence(), b.get(i).evidence());
        }
    }

    // ---- approval --------------------------------------------------------------------------------------------

    @Test
    void nothingIsActiveUntilAnAdminApproves() {
        signals(9, TWO, "dismissed", "pull_request");
        run();
        run();
        assertTrue(tuner.list("active").isEmpty());
        assertEquals(0, snapshot.size());
        assertEquals(0, tuner.version());
        assertEquals(Heuristic.MODEL, new Heuristic().withTuning(snapshot).classify(pullRequestEvent()).model(), "scoring must be unchanged");
    }

    private static Event pullRequestEvent() {
        Event e = pullRequest();
        e.setAttentionClass(Attention.STANDARD);
        return e;
    }

    @Test
    void approvingActivatesItAndTheHeuristicUsesItUntilItIsRetired() {
        signals(5, TWO, "dismissed", "pull_request");
        run();
        String id = tuner.list("").get(0).id();
        Heuristic h = new Heuristic().withTuning(snapshot);
        assertEquals(60, h.classify(pullRequestEvent()).score());

        Tuning.Adjustment a = tuner.approve(id, "admin");
        assertEquals("active", a.status());
        assertEquals(1, a.version());
        assertEquals("admin", a.resolvedBy());
        assertNotNull(a.resolvedAt());
        Classification got = h.classify(pullRequestEvent());
        assertEquals(46, got.score());
        assertEquals("heuristic-v1+tuned-1", got.model());
        assertTrue(got.reason().contains("learned adjustment -14: 5 of 5 signals from 2 people were negative"), got.reason());
        assertThrows(Tuning.NotProposedException.class, () -> tuner.approve(id, "admin"));
        assertThrows(Tuning.NotProposedException.class, () -> tuner.reject(id, "admin"));

        clock.now = clock.now.plusSeconds(60);
        Tuning.Adjustment retired = tuner.retire(id, "admin2");
        assertEquals("retired", retired.status());
        assertEquals(2, retired.version());
        assertEquals("admin2", retired.resolvedBy());
        Classification restored = h.classify(pullRequestEvent());
        assertEquals(60, restored.score(), "retiring must restore today's score");
        assertEquals(Heuristic.MODEL, restored.model());
        assertThrows(Tuning.NotActiveException.class, () -> tuner.retire(id, "admin"));
        assertThrows(Tuning.NotProposedException.class, () -> tuner.approve(id, "admin"));
        assertThrows(Tuning.NotFoundException.class, () -> tuner.approve("nope", "admin"));
    }

    @Test
    void rejectedAndActiveAdjustmentsAreNotProposedAgainAndRetiredOnesNeedFreshEvidence() {
        signals(5, TWO, "dismissed", "pull_request");
        signals(5, TWO, "dismissed", "push");
        signals(5, TWO, "dismissed", "issues");
        run();
        Map<String, String> byType = new java.util.HashMap<>();
        tuner.list("").forEach(a -> byType.put(a.eventType(), a.id()));
        tuner.reject(byType.get("pull_request"), "admin");
        tuner.approve(byType.get("push"), "admin");
        tuner.approve(byType.get("issues"), "admin");
        clock.now = clock.now.plus(Duration.ofHours(1));
        tuner.retire(byType.get("issues"), "admin");

        clock.now = clock.now.plus(Duration.ofHours(1));
        assertEquals(0, run().proposalsMade(), "a rejected, active or freshly retired scope has no new evidence");
        Map<String, String> statuses = new java.util.HashMap<>();
        tuner.list("").forEach(a -> statuses.put(a.eventType(), a.status()));
        assertEquals("rejected", statuses.get("pull_request"));
        assertEquals("active", statuses.get("push"));
        assertEquals("retired", statuses.get("issues"));

        // New evidence after the retirement brings a retired scope back as a fresh proposal.
        clock.now = clock.now.plus(Duration.ofHours(1));
        signals(5, TWO, "dismissed", "issues");
        assertEquals(1, run().proposalsMade());
        Tuning.Adjustment issues = adjustments.get(byType.get("issues"));
        assertEquals("proposed", issues.status());
        assertNull(issues.resolvedAt());
        assertEquals("", issues.resolvedBy());
        assertEquals(5, issues.evidence().signals());
    }

    @Test
    void approvingIsRefusedForACriticalOrLockedScopeAndForAnOutOfBoundsOffset() {
        for (Object[] c : new Object[][] {{"sec", "security_advisory", -10}, {"big", "push", 51}, {"zero", "push2", 0}}) {
            adjustments.save(new Tuning.Adjustment((String) c[0], "github", (String) c[1], "", (Integer) c[2], "proposed", Tuning.Evidence.NONE,
                    new Tuning.Impact(0, 0), 0, T0, T0, "", null));
        }
        for (String id : List.of("sec", "big", "zero")) {
            assertThrows(Tuning.GuardrailException.class, () -> tuner.approve(id, "admin"), id);
            assertEquals("proposed", adjustments.get(id).status(), "a refused approval changes nothing");
            assertEquals(0, snapshot.size());
        }
    }

    @Test
    void anActiveAdjustmentThatALockedRuleNowCoversIsLeftOutOfTheSnapshot() {
        adjustments.save(new Tuning.Adjustment("a", "github", "push", "", -10, "active", Tuning.Evidence.NONE, new Tuning.Impact(0, 0), 1, T0, T0, "", null));
        adjustments.save(new Tuning.Adjustment("b", "github", "security_advisory", "", -10, "active", Tuning.Evidence.NONE, new Tuning.Impact(0, 0), 2, T0, T0, "", null));
        tuner.refreshSnapshot();
        assertEquals(1, snapshot.size(), "only the uncovered adjustment may be applied");
        assertEquals(2, snapshot.version());
    }

    @Test
    void theImpactEstimateReplaysRecentEventsWithAndWithoutTheAdjustment() {
        signals(9, List.of("alice", "bob", "carol"), "dismissed", "push"); // N = 9 -> round(20 * -9 / 11) = -16: a push (50) drops to 34
        for (int i = 0; i < 4; i++) {
            Event e = new Event("p" + i, "github", "push", "", Map.of());
            e.setAttentionClass(Attention.STANDARD);
            events.add(e);
        }
        Event bg = new Event("bg", "github", "push", "", Map.of());
        bg.setAttentionClass(Attention.BACKGROUND);
        events.add(bg);
        Event other = new Event("other", "github", "issues", "", Map.of());
        other.setAttentionClass(Attention.STANDARD);
        events.add(other);
        run();
        Tuning.Adjustment a = tuner.list("").get(0);
        assertEquals(-16, a.delta());
        assertEquals(new Tuning.Impact(13, 13), a.impact());
        assertEquals(0, snapshot.size(), "computing the estimate changes nothing");
    }

    // ---- runs ------------------------------------------------------------------------------------------------

    @Test
    void aRunReturnsAtOnceRecordsWhatItDidAndRefusesASecondWhileRunning() throws Exception {
        signals(5, TWO, "dismissed", "pull_request");
        CountDownLatch gate = new CountDownLatch(1);
        AtomicBoolean entered = new AtomicBoolean();
        tuner = new Tuning.Tuner(adjustments, runs, f -> {
            entered.set(true);
            try {
                gate.await(10, TimeUnit.SECONDS);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            return feedback.list(f);
        }, () -> List.copyOf(events), () -> List.copyOf(ruleList), snapshot, new Tuning.Config(0, 0, 0, null, null), clock);

        Tuning.Run started = tuner.start();
        assertEquals("running", started.status());
        assertNull(started.finishedAt());
        assertThrows(Tuning.RunInProgressException.class, () -> tuner.start());
        assertThrows(Tuning.RunInProgressException.class, () -> tuner.runNow());
        gate.countDown();
        tuner.await();

        List<Tuning.Run> list = tuner.runs(10);
        assertEquals(1, list.size());
        assertEquals(started.id(), list.get(0).id());
        assertEquals("done", list.get(0).status());
        assertEquals(1, list.get(0).proposalsMade());
        assertEquals(5, list.get(0).feedbackRead());
        assertNotNull(list.get(0).finishedAt());
        assertTrue(entered.get());
        assertNotNull(tuner.runNow(), "a run can be made again once the last finished");
    }

    @Test
    void aFailedRunIsRecordedWithARedactedErrorAndChangesNothing() {
        tuner = new Tuning.Tuner(adjustments, runs, f -> {
            throw new IllegalStateException("connection refused at postgres://user:secret@db/notif");
        }, () -> List.copyOf(events), () -> List.copyOf(ruleList), snapshot, new Tuning.Config(0, 0, 0, null, null), clock);
        Tuning.Run run = tuner.runNow();
        assertEquals("failed", run.status());
        assertEquals("connection refused at", run.error());
        assertTrue(tuner.list("").isEmpty());
        assertNotNull(tuner.runNow(), "a failed run does not block the next");
    }

    @Test
    void aRunThatNeverFinishedIsAbandonedAfterTheStaleLimit() {
        runs.start(new Tuning.Run("old", "running", T0.minus(Tuning.STALE_RUN_AFTER).minusSeconds(60), null, 0, 0, 0, ""),
                T0.minus(Tuning.STALE_RUN_AFTER).minus(Duration.ofHours(1)));
        tuner.runNow();
        Tuning.Run old = tuner.runs(10).stream().filter(r -> r.id().equals("old")).findFirst().orElseThrow();
        assertEquals("failed", old.status());
        assertEquals(Tuning.ABANDONED_RUN_TEXT, old.error());
        assertNotNull(old.finishedAt());
    }

    @Test
    void theConfigFallsBackToTheDefaultsAndIsBounded() {
        Tuning.Config cfg = new Tuning.Config(0, 0, 0, null, null);
        assertEquals(5, cfg.minSignals());
        assertEquals(2, cfg.minPeople());
        assertEquals(20, cfg.maxAdjustment());
        assertEquals(Duration.ofDays(30), cfg.lookback());
        assertEquals(Duration.ofDays(14), cfg.halfLife());
        assertEquals(Tuning.MAX_DELTA, new Tuning.Config(1, 1, 500, null, null).maxAdjustment());
    }

    @Test
    void theAdjustmentIdMatchesTheOneGoComputes() {
        assertEquals("68abe35a59d2dd14", Tuning.adjustmentId("github", "pull_request", ""));
        assertEquals("10ce5c7ae7840ae4", Tuning.adjustmentId("github", "issues", ""));
        assertEquals(Tuning.adjustmentId("github", "pull_request", ""), Tuning.adjustmentId("GitHub", "Pull_Request", ""));
        assertFalse(Tuning.adjustmentId("github", "pull_request", "acme/api").equals(Tuning.adjustmentId("github", "pull_request", "")));
    }

    @Test
    void theJsonHasGosShape() {
        signals(5, TWO, "dismissed", "pull_request");
        run();
        Map<String, Object> m = Tuning.toMap(tuner.list("").get(0));
        assertEquals(List.of("id", "source", "eventType", "delta", "status", "evidence", "impact", "version", "createdAt", "updatedAt"), List.copyOf(m.keySet()),
                "an empty repository and resolution are left out");
        assertEquals(List.of("id", "status", "startedAt", "finishedAt", "feedbackRead", "eventsRead", "proposalsMade"),
                List.copyOf(Tuning.toMap(tuner.runs(1).get(0)).keySet()));
    }

    // ---- the heuristic and its snapshot ------------------------------------------------------------------------

    private Tuning.Snapshot snapshotWith(int version, Tuning.Applied... applied) {
        Tuning.Snapshot s = new Tuning.Snapshot(Duration.ofMinutes(1), clock);
        s.set(version, List.of(applied));
        return s;
    }

    @Test
    void withoutAdjustmentsTheHeuristicScoresExactlyAsBefore() {
        Heuristic plain = new Heuristic();
        List<Heuristic> variants = List.of(new Heuristic().withTuning(null), new Heuristic().withTuning(snapshotWith(7)),
                new Heuristic().withTuning(snapshotWith(7, new Tuning.Applied("x", "gitlab", "star", "", -20, "x"))));
        for (Event e : List.of(star(), pullRequestEvent(), new Event("x", "github", "push", "", Map.of()))) {
            for (Heuristic h : variants) {
                assertEquals(plain.classify(e), h.classify(e));
            }
        }
    }

    @Test
    void aNegativeAdjustmentCanMoveStandardToBackgroundAndAPositiveOneProtects() {
        Heuristic down = new Heuristic().withTuning(snapshotWith(1, new Tuning.Applied("a", "github", "star", "", -20, "s")));
        assertEquals("background", down.classify(star()).attentionClass());
        // A star scores 15 on its own; +20 makes 35, which is standard: protected from demotion.
        Classification up = new Heuristic().withTuning(snapshotWith(1, new Tuning.Applied("a", "github", "star", "", 20, "s"))).classify(star());
        assertEquals(35, up.score());
        assertEquals("standard", up.attentionClass());
    }

    @Test
    void aCriticalEventIsNeverClassifiedNorAdjusted() {
        Heuristic h = new Heuristic().withTuning(snapshotWith(1, new Tuning.Applied("a", "github", "security_advisory", "", -20, "s")));
        Event critical = new Event("c", "github", "security_advisory", "", Map.of());
        critical.setAttentionClass(Attention.CRITICAL);
        assertThrows(IllegalArgumentException.class, () -> h.classify(critical));
        assertThrows(IllegalArgumentException.class, () -> GuardedClassifier.of(h).classify(critical));
    }

    @Test
    void anAdjustmentForARepositoryBeatsOneForTheWholeType() {
        Heuristic h = new Heuristic().withTuning(snapshotWith(2, new Tuning.Applied("t", "github", "pull_request", "", -5, "type"),
                new Tuning.Applied("r", "github", "pull_request", "ACME/api", 7, "repo")));
        Event inRepo = new Event("p", "github", "pull_request", "acme/api", Map.of("action", "opened"));
        Event elsewhere = new Event("q", "github", "pull_request", "acme/other", Map.of("action", "opened"));
        assertEquals(67, h.classify(inRepo).score());
        assertEquals(55, h.classify(elsewhere).score());
    }

    @Test
    void theScoreStaysBetweenZeroAndAHundredAndOffsetsAreClamped() {
        Classification high = new Heuristic().withTuning(snapshotWith(1, new Tuning.Applied("a", "github", "pull_request", "", 500, "huge")))
                .classify(pullRequestEvent());
        assertEquals(100, high.score());
        assertTrue(high.reason().contains("learned adjustment +50:"), high.reason());
        assertEquals(0, new Heuristic().withTuning(snapshotWith(1, new Tuning.Applied("a", "github", "star", "", -500, "huge"))).classify(star()).score());
    }

    @Test
    void aSnapshotThatIsNeverLoadedOrHasGoneStaleIsNotUsed() {
        MutableClock c = new MutableClock();
        Tuning.Snapshot snap = new Tuning.Snapshot(Duration.ofMinutes(1), c);
        Heuristic h = new Heuristic().withTuning(snap);
        assertEquals(60, h.classify(pullRequestEvent()).score());
        assertEquals(0, snap.version());
        assertEquals(0, snap.size());
        snap.set(4, List.of(new Tuning.Applied("a", "github", "pull_request", "", -10, "s")));
        assertEquals(50, h.classify(pullRequestEvent()).score());
        assertEquals(4, snap.version());
        assertEquals(1, snap.size());
        c.now = c.now.plus(Duration.ofMinutes(2));
        Classification stale = h.classify(pullRequestEvent());
        assertEquals(60, stale.score(), "a stale snapshot must be ignored and scores return to normal");
        assertEquals(Heuristic.MODEL, stale.model());
        assertEquals(0, snap.version());
        snap.set(5, List.of());
        assertEquals(60, h.classify(pullRequestEvent()).score(), "an empty reload is fresh and changes nothing");
    }

    @Test
    void aZeroOffsetIsNotKept() {
        assertEquals(0, snapshotWith(1, new Tuning.Applied("a", "github", "star", "", 0, "s")).size());
    }
}
