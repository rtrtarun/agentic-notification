package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Classification;
import com.agentpulse.notification.domain.DecisionState;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Status;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The built-in Attention Intelligence: the same scores, summaries and explanations as the Go heuristic. */
class HeuristicTest {
    private final Heuristic h = new Heuristic();

    private static Map<String, Object> map(Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < kv.length; i += 2) {
            m.put((String) kv[i], kv[i + 1]);
        }
        return m;
    }

    private static Event event(String type, Map<String, Object> payload) {
        Event e = new Event("e-" + type, "github", type, "acme/api", payload);
        e.setAttentionClass("standard");
        return e;
    }

    private Classification classify(String type, Map<String, Object> payload) {
        return h.classify(event(type, payload));
    }

    // ---- classify ------------------------------------------------------------------------------------------

    @Test
    void anEventNothingIsKnownAboutIsAPlainStandardFifty() {
        Classification c = classify("push", map());
        assertEquals(50, c.score());
        assertEquals("standard", c.attentionClass());
        assertEquals(0.6, c.confidence(), 1e-9);
        assertEquals("scored 50", c.reason());
        assertEquals("heuristic-v1", c.model());
    }

    @Test
    void socialActivityIsBackgroundWithAReason() {
        Classification c = classify("star", map());
        assertEquals(15, c.score());
        assertEquals("background", c.attentionClass());
        assertEquals("scored 15: social activity", c.reason());
        assertEquals(0.75, c.confidence(), 1e-9);
        for (String type : List.of("watch", "fork", "public", "member", "gollum")) {
            assertEquals("background", classify(type, map()).attentionClass(), type);
        }
    }

    @Test
    void routineChangesBotsAndDraftsLowerTheScore() {
        assertEquals("scored 30: routine change (labeled)", classify("pull_request", map("action", "Labeled")).reason());
        assertEquals("scored 25: automated sender", classify("push", map("sender", map("type", "Bot"))).reason());
        assertEquals("scored 25: automated sender", classify("push", map("sender", map("login", "dependabot[bot]"))).reason());
        assertEquals("scored 30: draft pull request", classify("push", map("pull_request", map("draft", true))).reason());
        assertEquals(50, classify("push", map("pull_request", map("draft", false))).score());
        assertEquals(50, classify("push", map("sender", "not-an-object")).score());
    }

    @Test
    void thingsThatAskForAttentionRaiseIt() {
        assertEquals("scored 75: someone asked for your review", classify("pull_request", map("action", "review_requested")).reason());
        assertEquals("scored 70: changes were requested",
                classify("pull_request_review", map("review", map("state", "CHANGES_REQUESTED"))).reason());
        assertEquals("scored 80: a check failed", classify("check_run", map("check_run", map("conclusion", "failure"))).reason());
        assertEquals(80, classify("workflow_run", map("workflow_run", map("conclusion", "timed_out"))).score());
        assertEquals(80, classify("check_suite", map("check_suite", map("conclusion", "cancelled"))).score());
        assertEquals(50, classify("check_run", map("check_run", map("conclusion", "success"))).score());
        assertEquals("scored 60: something new was opened", classify("issues", map("action", "opened")).reason());
        assertEquals("scored 60: a person commented", classify("issue_comment", map()).reason());
        assertEquals(60, classify("pull_request_review_comment", map()).score());
    }

    @Test
    void reasonsAreListedInTheOrderTheyWereWeighedAndTheScoreIsClamped() {
        Classification c = classify("star", map("action", "edited", "sender", map("type", "bot"), "pull_request", map("draft", true)));
        assertEquals(0, c.score(), "15 - 20 - 25 - 20 is clamped at 0");
        assertEquals("scored 0: social activity, routine change (edited), automated sender, draft pull request", c.reason());
        assertEquals(0.9, c.confidence(), 1e-9);
        assertEquals("background", c.attentionClass());
    }

    @Test
    void confidenceGrowsWithTheScoreAndIsCapped() {
        assertEquals(0.85, classify("pull_request", map("action", "review_requested")).confidence(), 1e-9);
        assertEquals(0.9, classify("check_run", map("check_run", map("conclusion", "failure"))).confidence(), 1e-9);
    }

    @Test
    void aCriticalEventIsRefused() {
        Event critical = event("security_advisory", map());
        critical.setAttentionClass("critical");
        assertThrows(IllegalArgumentException.class, () -> h.classify(critical));
    }

    @Test
    void theSameEventAlwaysGetsTheSameAnswer() {
        Event e = event("pull_request", map("action", "opened"));
        assertEquals(h.classify(e), h.classify(e));
    }

    // ---- summarise -----------------------------------------------------------------------------------------

    private static Event held(String id, String source, String type, String repo) {
        Event e = new Event(id, source, type, repo, null);
        e.setAttentionClass("background");
        return e;
    }

    @Test
    void nothingToSummariseSaysSo() {
        Heuristic.Summary s = h.summarize(List.of());
        assertEquals("Nothing held back", s.title());
        assertEquals("Nothing was held back.", s.text());
        assertTrue(s.groups().isEmpty());
    }

    @Test
    void groupsAreBySourceAndRepositoryBusiestFirstThenByKey() {
        Heuristic.Summary s = h.summarize(List.of(held("1", "github", "star", "acme/web"), held("2", "github", "push", "acme/api"),
                held("3", "github", "star", "acme/api"), held("4", "jira", "issue", ""), held("5", "github", "star", "acme/api")));
        assertEquals("5 notifications held back", s.title());
        assertEquals("5 notifications held back, from 4 github, 1 jira. Busiest: acme/api (github) with 3.", s.text());
        assertEquals(List.of("github/acme/api", "github/acme/web", "jira"), s.groups().stream().map(Heuristic.Group::key).toList());
        assertEquals("3 notifications: 2 star, 1 push", s.groups().get(0).summary());
        assertEquals("1 notification: 1 issue", s.groups().get(2).summary());
        assertEquals("jira", s.groups().get(2).title(), "no repository: the source is the title");
        assertEquals(List.of("2", "3", "5"), s.groups().get(0).eventIds());
    }

    @Test
    void aCriticalEventIsNeverSummarisedAndMoreThanThreeKindsAreCut() {
        Event critical = held("c", "github", "security_advisory", "acme/api");
        critical.setAttentionClass("critical");
        List<Event> events = new ArrayList<>(List.of(critical));
        for (String t : List.of("a", "b", "c", "d", "e")) {
            events.add(held(t, "github", t, "acme/api"));
        }
        events.add(held("x", "github", "", "acme/api"));
        Heuristic.Summary s = h.summarize(events);
        assertEquals("6 notifications held back", s.title(), "the critical event is not counted");
        assertEquals("6 notifications: 1 a, 1 b, 1 c, and 3 more", s.groups().get(0).summary());
        assertTrue(Heuristic.counts(Map.of("", 2), 3).startsWith("2 other"), "a missing type reads as other");
    }

    // ---- explain -------------------------------------------------------------------------------------------

    private static Event decided(String cls, String status, String rule, String reason) {
        Event e = new Event("e1", "github", "push", "acme/api", null);
        e.setAttentionClass(cls);
        DecisionState s = new DecisionState(reason, rule);
        s.setStatus(status);
        e.setDecisionState(s);
        return e;
    }

    @Test
    void everyOutcomeIsExplainedInPlainLanguage() {
        assertEquals("No decision has been recorded for this event.", h.explain(new Event("x", "github", "push", "", null)));
        assertEquals("This is critical, so it was sent to every channel at once. No judgement was involved and nothing can mute it. "
                + "The rule \"adv\" applied: security.", h.explain(decided("critical", Status.SENT, "adv", "security.")));
        Event merged = decided("standard", Status.MERGED, "r", "duplicate");
        assertEquals("This repeated an earlier notification, so it was merged into it instead of interrupting you again. "
                + "A rule or a choice decided this; Attention Intelligence was not used.", h.explain(merged));
        merged.setMergedInto("first-1");
        assertTrue(h.explain(merged).contains(" The first one is first-1. "));
        assertTrue(h.explain(decided("background", Status.DIGEST, "", "never notify override for github"))
                .startsWith("It was held for your digest instead of being sent now. never notify override for github."));
        assertTrue(h.explain(decided("standard", Status.BUDGET_EXCEEDED, "r", "x")).contains("It was not dropped."));
        assertTrue(h.explain(decided("standard", Status.DELAYED, "r", "quiet")).startsWith("It was held until quiet hours end. "));
        assertTrue(h.explain(decided("standard", Status.QUEUED, "", "")).contains("No rule matched, so the default applied."));
        assertTrue(h.explain(decided("standard", Status.QUEUED, "default", "default routing"))
                .startsWith("It was kept as a standard notification and queued. The rule \"default\" applied: default routing."));
    }

    @Test
    void aRefinedDecisionNamesTheEngineAndItsConfidence() {
        Event e = decided("background", Status.QUEUED, "default", "default routing; attention scored 15");
        e.getDecisionState().setModelVersion("heuristic-v1");
        e.getDecisionState().setConfidence(0.704);
        String text = h.explain(e);
        assertTrue(text.endsWith("Attention Intelligence (heuristic-v1) refined this decision with 70% confidence."), text);
        assertTrue(!text.contains("was not used"));
    }
}
