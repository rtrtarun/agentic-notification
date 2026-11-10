package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Predicate;
import com.agentpulse.notification.domain.Rule;
import org.junit.jupiter.api.Test;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Payload predicates: the same meaning as the Go service's {@code pkg/policy/predicate.go}. */
class PredicatesTest {
    private static Event event(Map<String, Object> payload) {
        return new Event("e", "app", "job.failed", "acme/api", payload);
    }

    private static Map<String, Object> map(Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < kv.length; i += 2) {
            m.put((String) kv[i], kv[i + 1]);
        }
        return m;
    }

    private static boolean match(Predicate p, Map<String, Object> payload) {
        return Predicates.matchAll(List.of(p), event(payload));
    }

    @Test
    void noPredicatesMatchEverything() {
        assertTrue(Predicates.matchAll(List.of(), event(map())));
    }

    @Test
    void everyPredicateMustHold() {
        List<Predicate> both = List.of(new Predicate("a", "eq", 1), new Predicate("b", "eq", 2));
        assertTrue(Predicates.matchAll(both, event(map("a", 1, "b", 2))));
        assertFalse(Predicates.matchAll(both, event(map("a", 1, "b", 3))));
    }

    @Test
    void numbersCompareAsNumbersWhateverTheirTypeOrSpelling() {
        assertTrue(match(new Predicate("n", "eq", 3), map("n", 3.0)));
        assertTrue(match(new Predicate("n", "eq", 3L), map("n", 3)));
        assertTrue(match(new Predicate("n", "eq", "3"), map("n", " 3 ")));
        assertTrue(match(new Predicate("n", "gt", 2), map("n", "2.5")));
        assertFalse(match(new Predicate("n", "gt", 3), map("n", 3)));
        assertTrue(match(new Predicate("n", "gte", 3), map("n", 3)));
        assertTrue(match(new Predicate("n", "lt", 4), map("n", 3)));
        assertTrue(match(new Predicate("n", "lte", 3), map("n", 3)));
        assertTrue(match(new Predicate("n", "gt", 1), map("n", "1e3")));
    }

    @Test
    void textIsNotANumberUnlessItLooksLikeOne() {
        assertFalse(match(new Predicate("n", "gt", 1), map("n", "many")));
        assertFalse(match(new Predicate("n", "gt", 1), map("n", "NaN")));
        assertFalse(match(new Predicate("n", "gt", 1), map("n", "Infinity")));
        assertFalse(match(new Predicate("n", "gt", 1), map("n", "1d")));
        assertFalse(match(new Predicate("n", "gt", 1), map("n", true)));
        assertNull(Predicates.number(Double.NaN));
        assertNull(Predicates.number(null));
    }

    @Test
    void textComparisonIgnoresCaseAndNeverEqualsAnotherType() {
        assertTrue(match(new Predicate("s", "", "HIGH"), map("s", "high")));
        assertTrue(match(new Predicate("s", "ne", "low"), map("s", "high")));
        assertFalse(match(new Predicate("s", "eq", "low"), map("s", "high")));
        assertFalse(match(new Predicate("s", "eq", "abc"), map("s", 5)));
        assertFalse(match(new Predicate("s", "eq", true), map("s", "true")));
    }

    @Test
    void booleansMatchOnlyBooleans() {
        assertTrue(match(new Predicate("d", "eq", false), map("d", false)));
        assertFalse(match(new Predicate("d", "eq", false), map("d", "false")));
        assertFalse(match(new Predicate("d", "eq", true), map("d", false)));
        assertTrue(match(new Predicate("d", "ne", true), map("d", false)));
    }

    @Test
    void aMissingFieldNeverMatchesExceptUnderExistsFalse() {
        assertFalse(match(new Predicate("x", "eq", 1), map()));
        assertFalse(match(new Predicate("x", "ne", 1), map()), "ne needs the field too");
        assertFalse(match(new Predicate("x", "gt", 1), map()));
        assertFalse(match(new Predicate("x", "exists", null), map()));
        assertTrue(match(new Predicate("x", "exists", false), map()));
        assertTrue(match(new Predicate("x", "exists", null), map("x", 1)));
        assertTrue(match(new Predicate("x", "exists", true), map("x", 1)));
        assertFalse(match(new Predicate("x", "exists", false), map("x", 1)));
    }

    @Test
    void aFieldPresentWithNullExistsButMatchesNothingElse() {
        Map<String, Object> payload = map("x", null);
        assertTrue(match(new Predicate("x", "exists", true), payload));
        assertFalse(match(new Predicate("x", "eq", "a"), payload));
    }

    @Test
    void dottedPathsWalkNestedObjectsOnly() {
        assertTrue(match(new Predicate("metadata.attempt", "gt", 3), map("metadata", map("attempt", 5))));
        assertFalse(match(new Predicate("metadata.attempt", "gt", 3), map("metadata", "text")));
        assertFalse(match(new Predicate("a.b.c", "exists", true), map("a", map("b", 1))));
        assertFalse(match(new Predicate("a..b", "exists", true), map("a", map("b", 1))));
    }

    @Test
    void branchLooksInThreePlacesAndDropsTheRefsPrefix() {
        assertTrue(match(new Predicate("branch", "eq", "main"), map("branch", "main")));
        assertTrue(match(new Predicate("branch", "eq", "main"), map("ref", "refs/heads/main")));
        assertTrue(match(new Predicate("branch", "eq", "feature"), map("pull_request", map("head", map("ref", "feature")))));
        assertFalse(match(new Predicate("branch", "eq", "main"), map("branch", 5)), "a non-text branch is not found");
        assertFalse(match(new Predicate("branch", "exists", true), map()));
    }

    @Test
    void authorAndSeverityLookInSeveralPlaces() {
        assertTrue(match(new Predicate("author", "eq", "sam"), map("sender", map("login", "sam"))));
        assertTrue(match(new Predicate("author", "eq", "sam"), map("pusher", map("name", "sam"))));
        assertTrue(match(new Predicate("author", "eq", "sam"), map("metadata", map("author", "sam"))));
        assertTrue(match(new Predicate("severity", "eq", "high"), map("security_advisory", map("severity", "high"))));
        assertTrue(match(new Predicate("severity", "eq", "high"), map("alert", map("severity", "high"))));
        assertFalse(match(new Predicate("severity", "eq", "high"), map()));
    }

    // ---- validation ---------------------------------------------------------------------------------------

    @Test
    void goodPredicatesPassValidation() {
        assertNull(Predicates.validate(List.of()));
        assertNull(Predicates.validate(List.of(new Predicate("metadata.attempt", "gt", 3), new Predicate("severity", "", "high"),
                new Predicate("x", "exists", null), new Predicate("y", "exists", false), new Predicate("z", "ne", true))));
    }

    @Test
    void badPredicatesAreRefusedWithTheGoMessages() {
        assertEquals("a rule can have at most 5 payload predicates",
                Predicates.validate(List.of(p("a"), p("b"), p("c"), p("d"), p("e"), p("f"))));
        String field = "payload predicate 1: field must be a dotted name such as metadata.attempt";
        assertEquals(field, Predicates.validate(List.of(new Predicate("", "eq", 1))));
        assertEquals(field, Predicates.validate(List.of(new Predicate("a b", "eq", 1))));
        assertEquals(field, Predicates.validate(List.of(new Predicate(".a", "eq", 1))));
        assertEquals(field, Predicates.validate(List.of(new Predicate("a\n", "eq", 1))), "a trailing newline is not a name");
        assertEquals(field, Predicates.validate(List.of(new Predicate("a".repeat(101), "eq", 1))));
        assertEquals("payload predicate 1: eq needs a string, number or true/false value",
                Predicates.validate(List.of(new Predicate("a", "", null))));
        assertEquals("payload predicate 2: ne needs a string, number or true/false value",
                Predicates.validate(List.of(p("a"), new Predicate("a", "ne", List.of(1)))));
        assertEquals("payload predicate 1: gt needs a number", Predicates.validate(List.of(new Predicate("a", "gt", "many"))));
        assertEquals("payload predicate 1: lte needs a number", Predicates.validate(List.of(new Predicate("a", "lte", null))));
        assertEquals("payload predicate 1: exists takes true or false", Predicates.validate(List.of(new Predicate("a", "exists", "yes"))));
        assertEquals("payload predicate 1: unknown operator \"between\" (use eq, ne, gt, gte, lt, lte or exists)",
                Predicates.validate(List.of(new Predicate("a", "between", 1))));
        assertEquals("payload predicate 1: value is at most 200 characters",
                Predicates.validate(List.of(new Predicate("a", "eq", "x".repeat(201)))));
    }

    private static Predicate p(String field) {
        return new Predicate(field, "eq", 1);
    }

    @Test
    void channelOverridesAreCheckedAndNeverAllowedOnACriticalRule() {
        assertNull(Predicates.validateChannels("standard", List.of()));
        assertNull(Predicates.validateChannels("critical", List.of()));
        assertNull(Predicates.validateChannels("background", List.of("email", "slack", "teams", "sms")));
        assertEquals("unknown channel \"pager\" (use email, slack, teams or sms)",
                Predicates.validateChannels("standard", List.of("pager")));
        assertEquals("channel \"slack\" is listed twice", Predicates.validateChannels("standard", List.of("slack", "slack")));
        assertEquals("a critical rule cannot choose channels; critical events always go to every configured channel",
                Predicates.validateChannels("critical", List.of("slack")));
    }

    // ---- the engine ---------------------------------------------------------------------------------------

    @Test
    void theEngineHonoursPredicatesAndReturnsTheRulesChannels() {
        Rule storm = new Rule("1", "storm", "", new Rule.Condition("app", "", "", List.of(new Predicate("attempt", "gt", 3))),
                new Rule.Outcome("standard", "retries", false, List.of("slack")), false, false);
        Rule other = new Rule("2", "other", "", new Rule.Condition("app", "", ""),
                new Rule.Outcome("background", "rest", false), false, false);
        PolicyEngine engine = new PolicyEngine(List.of(storm, other));

        var hit = engine.evaluate(event(map("attempt", 5))).orElseThrow();
        assertEquals("storm", hit.matchedRule());
        assertEquals(List.of("slack"), hit.channels());
        var miss = engine.evaluate(event(map("attempt", 2))).orElseThrow();
        assertEquals("other", miss.matchedRule());
        assertTrue(miss.channels().isEmpty());
    }

    @Test
    void aCriticalRuleNeverYieldsChannelsEvenIfOneWasStored() {
        Rule critical = new Rule("1", "bad", "", new Rule.Condition("app", "", ""),
                new Rule.Outcome("critical", "sev", false, List.of("slack")), false, true);
        var decision = new PolicyEngine(List.of(critical)).evaluate(event(map())).orElseThrow();
        assertTrue(decision.channels().isEmpty(), "critical events always go to every configured channel");
    }
}
