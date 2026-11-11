package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.DecisionState;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.FeedbackStores;
import com.agentpulse.notification.infrastructure.LearningStores;
import com.agentpulse.notification.infrastructure.MemoryStores;
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
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The learning engine: the same suggestions, evidence rules and guardrails as the Go service's {@code pkg/learning}. */
class LearningTest {
    private static final Instant T0 = Instant.parse("2026-11-10T10:00:00Z");

    private static final class MutableClock extends Clock {
        Instant now = T0;

        @Override public ZoneOffset getZone() { return ZoneOffset.UTC; }
        @Override public Clock withZone(java.time.ZoneId zone) { return this; }
        @Override public Instant instant() { return now; }
    }

    private final MutableClock clock = new MutableClock();
    private final LearningStores.Memory repo = new LearningStores.Memory();
    private final FeedbackStores.MemoryFeedback feedback = new FeedbackStores.MemoryFeedback();
    private final Overrides.Service overrides = new Overrides.Service(new FeedbackStores.MemoryOverrides(), clock);
    private final List<Event> events = new ArrayList<>();
    private final MemoryStores.Rules ruleStore = new MemoryStores.Rules();
    private RulesService rules;
    private Learning.Service learning;
    private int serial;

    private static Rule rule(String name, String cls, boolean adaptive, boolean locked, boolean disabled, String source, String type) {
        return new Rule("", name, "", new Rule.Condition(source, type, ""), new Rule.Outcome(cls, "r", adaptive), disabled, locked);
    }

    private void setUp(Rule... seed) {
        PolicyEngine engine = new PolicyEngine(List.of());
        rules = new RulesService(ruleStore, engine, List.of(seed));
        learning = new Learning.Service(repo, feedback::list, () -> List.copyOf(events), Learning.ruleEditor(rules), overrides::upsert,
                new Learning.Config(5, 2, Duration.ofDays(30)), clock);
    }

    /** An event handled by a rule, with one piece of feedback about it. */
    private Event signal(String action, String source, String type, String repo, String cls, String ruleName) {
        String id = "e" + (++serial);
        Event e = new Event(id, source, type, repo, null);
        e.setAttentionClass(cls);
        e.setDecisionState(new DecisionState("r", ruleName));
        events.add(e);
        feedback.add(new Feedback.Entry("f" + serial, id, "sam", action, "", "", "", source, type, repo, clock.instant()));
        return e;
    }

    private void signals(int n, String action, String source, String type, String repo, String cls, String ruleName) {
        for (int i = 0; i < n; i++) {
            clock.now = clock.now.plusSeconds(1);
            signal(action, source, type, repo, cls, ruleName);
        }
    }

    // ---- when a suggestion is made ---------------------------------------------------------------------------

    @Test
    void nothingIsSuggestedWithoutFeedbackOrBelowTheThreshold() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        assertTrue(learning.review().isEmpty());
        signals(4, "dismissed", "github", "pull_request", "acme/api", "standard", "default");
        assertTrue(learning.review().isEmpty(), "4 is below 5");
        assertEquals(5, learning.minSignals());
    }

    @Test
    void enoughDismissalsOnAStandardRuleSuggestLettingAttentionRefineIt() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        signals(3, "dismissed", "github", "pull_request", "acme/api", "standard", "default");
        signals(2, "never", "github", "pull_request", "acme/api", "standard", "default");

        Learning.Suggestion s = learning.review().get(0);

        assertEquals("adaptive", s.kind());
        assertEquals("pending", s.status());
        assertEquals("default", s.ruleName());
        assertEquals("acme/api", s.repository(), "one repository is named");
        assertEquals(new Learning.Evidence(3, 2, 0, 0, 0), s.evidence());
        assertEquals("People dismissed or muted github pull_request notifications 5 times and never opened or replied to one.", s.summary());
        assertEquals("Let Attention refine the rule \"default\", so quiet events like this can move to the digest.", s.proposed());
    }

    @Test
    void anAlreadyAdaptiveStandardRuleIsSuggestedForDemotion() {
        setUp(rule("default", "standard", true, false, false, "github", ""));
        signals(5, "dismissed", "github", "pull_request", "", "standard", "default");
        Learning.Suggestion s = learning.review().get(0);
        assertEquals("demote", s.kind());
        assertEquals("Change the rule \"default\" from standard to background.", s.proposed());
    }

    @Test
    void withNoRuleADismissedGroupGetsANeverOverrideSuggestion() {
        setUp(rule("other", "standard", false, false, false, "jira", ""));
        signals(5, "dismissed", "github", "push", "acme/a", "standard", "");
        signals(1, "dismissed", "github", "push", "acme/b", "standard", "");
        Learning.Suggestion s = learning.review().get(0);
        assertEquals("override", s.kind());
        assertEquals("", s.repository(), "two repositories, so none is named");
        assertEquals("Add a \"never notify\" override for github push notifications.", s.proposed());
    }

    @Test
    void aPositiveSignalMeansNoSuggestionToQuieten() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        signals(8, "dismissed", "github", "pull_request", "acme/api", "standard", "default");
        signals(1, "opened", "github", "pull_request", "acme/api", "standard", "default");
        assertTrue(learning.review().isEmpty());
    }

    @Test
    void repeatedAlwaysOnABackgroundRuleSuggestsPromotingIt() {
        setUp(rule("stars", "background", false, false, false, "github", "star"));
        signals(2, "always", "github", "star", "acme/a", "background", "stars");
        signals(1, "always", "github", "star", "acme/b", "background", "stars");
        Learning.Suggestion s = learning.review().get(0);
        assertEquals("promote", s.kind());
        assertEquals("People asked to always be told about github star notifications 3 times.", s.summary());
        assertEquals("Change the rule \"stars\" from background to standard.", s.proposed());
    }

    @Test
    void alwaysOnBackgroundEventsNoRuleDecidedSuggestsAnAlwaysOverrideButNotWithANegativeSignal() {
        setUp(rule("other", "standard", false, false, false, "jira", ""));
        signals(2, "always", "github", "star", "acme/a", "background", "");
        Learning.Suggestion s = learning.review().get(0);
        assertEquals("override", s.kind());
        assertEquals("Add an \"always notify\" override for github star notifications in acme/a.", s.proposed());

        signals(1, "dismissed", "github", "star", "acme/a", "background", "");
        List<Learning.Suggestion> again = learning.review();
        assertEquals(1, again.size(), "the pending one stays; a negative signal only stops a new one being made");
    }

    // ---- the guardrails --------------------------------------------------------------------------------------

    @Test
    void feedbackAboutCriticalEventsIsNeverLearnedFrom() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        signals(9, "dismissed", "github", "security_advisory", "acme/api", "critical", "advisory");
        assertTrue(learning.review().isEmpty());
    }

    @Test
    void aLockedCriticalOrDisabledRuleIsNeverTheSubjectOfASuggestion() {
        setUp(rule("locked", "standard", false, true, false, "github", "a"),
                rule("critical", "critical", false, false, false, "github", "b"),
                rule("disabled", "standard", false, false, true, "github", "c"));
        signals(6, "dismissed", "github", "a", "", "standard", "locked");
        signals(6, "dismissed", "github", "b", "", "standard", "critical");
        signals(6, "dismissed", "github", "c", "", "standard", "disabled");
        assertTrue(learning.review().isEmpty());
    }

    @Test
    void applyingRechecksTheGuardrailAgainstTheRuleAsItIsNow() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        signals(5, "dismissed", "github", "pull_request", "", "standard", "default");
        Learning.Suggestion s = learning.review().get(0);

        // Someone made the rule critical after the suggestion was made. It is critical now, so it is refused.
        Rule current = rules.list().stream().filter(r -> r.name().equals("default")).findFirst().orElseThrow();
        rules.update(current.id(), new Rule(current.id(), "default", "", current.when(), new Rule.Outcome("critical", "r", false), false, false));
        assertThrows(Learning.GuardrailException.class, () -> learning.apply(s.id(), "admin"));
        assertEquals("critical", rules.list().get(0).then().attentionClass(), "refused, so nothing was changed");
        assertEquals("pending", repo.get(s.id()).status(), "and the suggestion is still pending");
    }

    @Test
    void aStoredSuggestionForALockedRuleCannotBeApplied() {
        setUp(rule("advisory", "critical", false, true, false, "github", "security_advisory"));
        Rule locked = rules.list().get(0);
        repo.save(new Learning.Suggestion("crafted", "demote", "pending", "github", "security_advisory", "", locked.id(), locked.name(), "s", "p",
                new Learning.Evidence(9, 0, 0, 0, 0), T0, T0, "", null));
        assertThrows(Learning.GuardrailException.class, () -> learning.apply("crafted", "admin"));
        assertEquals("critical", rules.list().get(0).then().attentionClass());
        assertTrue(rules.list().get(0).locked());
    }

    @Test
    void applyingASuggestionAboutARuleThatWasDeletedSaysSo() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        signals(5, "dismissed", "github", "pull_request", "", "standard", "default");
        Learning.Suggestion s = learning.review().get(0);
        rules.delete(rules.list().get(0).id());
        assertThrows(Learning.RuleGoneException.class, () -> learning.apply(s.id(), "admin"));
    }

    // ---- applying, dismissing, not making it again ------------------------------------------------------------

    @Test
    void applyingChangesTheRuleRecordsWhoAndWhenAndIsFinal() {
        setUp(rule("default", "standard", true, false, false, "github", ""));
        signals(5, "dismissed", "github", "pull_request", "", "standard", "default");
        Learning.Suggestion s = learning.review().get(0);
        clock.now = clock.now.plusSeconds(100);

        Learning.Suggestion done = learning.apply(s.id(), "admin@corp.com");

        assertEquals("applied", done.status());
        assertEquals("admin@corp.com", done.resolvedBy());
        assertEquals(clock.now, done.resolvedAt());
        assertEquals("background", rules.list().get(0).then().attentionClass());
        assertTrue(rules.list().get(0).then().adaptive(), "the rest of the rule is kept");
        assertThrows(Learning.NotPendingException.class, () -> learning.apply(s.id(), "admin"));
        assertThrows(Learning.NotPendingException.class, () -> learning.dismiss(s.id(), "admin"));
    }

    @Test
    void applyingAdaptiveAndPromoteChangeOnlyWhatTheySay() {
        setUp(rule("a", "standard", false, false, false, "github", "x"), rule("b", "background", false, false, false, "github", "y"));
        signals(5, "dismissed", "github", "x", "", "standard", "a");
        signals(2, "always", "github", "y", "", "background", "b");
        List<Learning.Suggestion> pending = learning.review();
        assertEquals(2, pending.size());
        for (Learning.Suggestion s : pending) {
            learning.apply(s.id(), "admin");
        }
        assertTrue(rules.list().get(0).then().adaptive());
        assertEquals("standard", rules.list().get(0).then().attentionClass());
        assertEquals("standard", rules.list().get(1).then().attentionClass(), "promoted from background");
    }

    @Test
    void applyingAnOverrideSuggestionCreatesTheRightOverrideWithANote() {
        setUp(rule("other", "standard", false, false, false, "jira", ""));
        signals(5, "dismissed", "github", "push", "acme/a", "standard", "");
        signals(2, "always", "github", "star", "acme/a", "background", "");
        for (Learning.Suggestion s : learning.review()) {
            learning.apply(s.id(), "admin");
        }
        List<Overrides.Override> all = overrides.list();
        assertEquals(2, all.size());
        Overrides.Override never = all.stream().filter(o -> o.eventType().equals("push")).findFirst().orElseThrow();
        assertEquals("never", never.mode());
        assertEquals("acme/a", never.repository());
        assertEquals("admin", never.createdBy());
        assertEquals("from a learning suggestion", never.note());
        assertEquals("always", all.stream().filter(o -> o.eventType().equals("star")).findFirst().orElseThrow().mode());
    }

    @Test
    void aDismissedSuggestionIsNotMadeAgainAndAnUnknownIdIsNotFound() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        signals(5, "dismissed", "github", "pull_request", "", "standard", "default");
        Learning.Suggestion s = learning.review().get(0);
        assertEquals("dismissed", learning.dismiss(s.id(), "admin").status());
        signals(3, "dismissed", "github", "pull_request", "", "standard", "default");
        assertTrue(learning.review().isEmpty());
        assertEquals("standard", rules.list().get(0).then().attentionClass(), "dismissing changes nothing");
        assertThrows(Learning.NotFoundException.class, () -> learning.apply("nope", "admin"));
        assertThrows(Learning.NotFoundException.class, () -> learning.dismiss("nope", "admin"));
    }

    @Test
    void feedbackFromBeforeAnAppliedSuggestionDoesNotCountButNewFeedbackDoes() {
        setUp(rule("default", "standard", true, false, false, "github", "pull_request"));
        signals(5, "dismissed", "github", "pull_request", "", "standard", "default");
        Learning.Suggestion first = learning.review().get(0);
        clock.now = clock.now.plusSeconds(10);
        learning.apply(first.id(), "admin");

        assertTrue(learning.review().isEmpty(), "the old feedback described the old behaviour");

        // The rule is now background; people then ask to always hear about it. That is new evidence.
        signals(2, "always", "github", "pull_request", "", "background", "default");
        List<Learning.Suggestion> next = learning.review();
        assertEquals(1, next.size());
        assertEquals("promote", next.get(0).kind());
    }

    @Test
    void aPendingSuggestionRefreshesItsEvidenceAndKeepsItsCreationTime() {
        setUp(rule("default", "standard", true, false, false, "github", ""));
        signals(5, "dismissed", "github", "pull_request", "", "standard", "default");
        Learning.Suggestion first = learning.review().get(0);
        clock.now = clock.now.plusSeconds(500);
        signals(2, "dismissed", "github", "pull_request", "", "standard", "default");
        Learning.Suggestion second = learning.review().get(0);
        assertEquals(first.id(), second.id());
        assertEquals(7, second.evidence().dismissed());
        assertEquals(first.createdAt(), second.createdAt());
        assertTrue(second.updatedAt().isAfter(first.updatedAt()));
        assertEquals(1, repo.list("").size());
    }

    @Test
    void feedbackOlderThanTheLookbackAndFeedbackForUnknownEventsIsIgnored() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        signals(5, "dismissed", "github", "pull_request", "", "standard", "default");
        clock.now = T0.plus(Duration.ofDays(40));
        assertTrue(learning.review().isEmpty(), "30 days is the lookback");

        clock.now = T0.plusSeconds(100);
        events.clear();
        assertTrue(learning.review().isEmpty(), "the events are gone from the recent window");
    }

    @Test
    void unusualActionsAndEventClassesAreIgnored() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        signals(6, "reply", "github", "pull_request", "", "standard", "default");
        signals(6, "undo", "github", "pull_request", "", "standard", "default");
        signal("dismissed", "github", "pull_request", "", "urgent", "default");
        assertTrue(learning.review().isEmpty());
    }

    // ---- the id and the JSON are the Go service's ------------------------------------------------------------

    @Test
    void theSuggestionIdIsTheSameHashGoComputesSoTwoServicesNeverMakeTheSameSuggestionTwice() {
        assertEquals("4720c2796c6d55bc", Learning.suggestionId("demote", "github", "pull_request", "acme/learn", "default-github"));
        assertEquals("4e54006b8bfbc086", Learning.suggestionId("override", "github", "star", "", ""));
    }

    @Test
    void theJsonOmitsEmptyFieldsAndReadsWhatGoWrote() throws Exception {
        Learning.Suggestion s = new Learning.Suggestion("id1", "override", "pending", "github", "star", "", "", "", "sum", "prop",
                new Learning.Evidence(0, 0, 0, 0, 2), T0, T0, "", null);
        Map<String, Object> m = Learning.toMap(s);
        for (String absent : List.of("repository", "ruleId", "ruleName", "resolvedBy", "resolvedAt")) {
            assertFalse(m.containsKey(absent), absent);
        }
        assertEquals(s, Learning.fromMap(m));

        Learning.Suggestion resolved = new Learning.Suggestion("id2", "demote", "applied", "github", "pull_request", "acme/learn", "rid", "default-github",
                "sum", "prop", new Learning.Evidence(5, 0, 0, 0, 0), T0, T0.plusSeconds(9), "api-key", T0.plusSeconds(9));
        assertEquals(resolved, Learning.fromMap(Learning.toMap(resolved)));

        String go = "{\"id\":\"4720c2796c6d55bc\",\"kind\":\"demote\",\"status\":\"pending\",\"source\":\"github\",\"eventType\":\"pull_request\","
                + "\"repository\":\"acme/learn\",\"ruleId\":\"r1\",\"ruleName\":\"default-github\",\"summary\":\"s\",\"proposed\":\"p\","
                + "\"evidence\":{\"dismissed\":5,\"never\":0,\"opened\":0,\"reply\":0,\"always\":0},\"createdAt\":\"2026-11-10T06:02:53.2696195Z\","
                + "\"updatedAt\":\"2026-11-10T06:02:53.2696195Z\"}";
        Learning.Suggestion read = Learning.fromMap(new com.fasterxml.jackson.databind.ObjectMapper().readValue(go,
                new com.fasterxml.jackson.core.type.TypeReference<Map<String, Object>>() {}));
        assertEquals(5, read.evidence().dismissed());
        assertEquals("", read.resolvedBy());
        assertNull(read.resolvedAt());
    }

    @Test
    void theMemoryStoreListsNewestUpdateFirstThenById() {
        for (String[] row : new String[][] {{"b", "0"}, {"a", "0"}, {"c", "5"}}) {
            repo.save(new Learning.Suggestion(row[0], "override", "pending", "s", "t", "", "", "", "", "", new Learning.Evidence(0, 0, 0, 0, 0),
                    T0, T0.plusSeconds(Long.parseLong(row[1])), "", null));
        }
        assertEquals(List.of("c", "a", "b"), repo.list("").stream().map(Learning.Suggestion::id).toList());
        assertEquals(3, repo.list("pending").size());
        assertTrue(repo.list("applied").isEmpty());
    }

    @Test
    void theRuleEditorTurnsALockedOrMissingRuleIntoTheGuardrailAndRuleGoneErrors() {
        setUp(rule("advisory", "critical", false, true, false, "github", "security_advisory"));
        Rule locked = rules.list().get(0);
        Learning.RuleEditor editor = Learning.ruleEditor(rules);
        assertEquals(1, editor.list().size());
        assertThrows(Learning.GuardrailException.class, () -> editor.update(locked.id(), locked));
        assertThrows(Learning.RuleGoneException.class, () -> editor.update("nope", locked));
    }

    @Test
    void aSuggestionOfAnUnknownKindIsRefusedRatherThanGuessed() {
        setUp(rule("default", "standard", false, false, false, "github", ""));
        repo.save(new Learning.Suggestion("odd", "teleport", "pending", "github", "push", "", "", "", "s", "p", new Learning.Evidence(9, 0, 0, 0, 0),
                T0, T0, "", null));
        assertThrows(IllegalStateException.class, () -> learning.apply("odd", "admin"));
        assertEquals("pending", repo.get("odd").status());
    }

    @Test
    void theConfigFallsBackToTheDefaults() {
        Learning.Config c = new Learning.Config(0, 0, null);
        assertEquals(5, c.minSignals());
        assertEquals(2, c.minAlways());
        assertEquals(Duration.ofDays(30), c.lookback());
    }
}
