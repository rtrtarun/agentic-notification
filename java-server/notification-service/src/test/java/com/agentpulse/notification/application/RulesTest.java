package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.PolicyDecision;
import com.agentpulse.notification.domain.Rule;
import org.junit.jupiter.api.Test;

import java.nio.file.Path;
import java.util.List;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Glob matching, the policy engine and the rules loader. */
class RulesTest {
    private static final Path GO_RULES = Path.of("../../go-server/notification-service/config/rules.yaml");

    @Test
    void anEmptyPatternMatchesAnythingAndStarDoesNotCrossASlash() {
        assertTrue(Glob.matches("", "whatever"));
        assertTrue(Glob.matches("acme/*", "acme/api"));
        assertFalse(Glob.matches("acme/*", "acme/api/v2"));
        assertTrue(Glob.matches("a?c", "abc"));
        assertFalse(Glob.matches("a?c", "a/c"));
        assertTrue(Glob.matches("github", "github"));
        assertFalse(Glob.matches("github", "gitlab"));
        assertTrue(Glob.matches("a.b", "a.b"));
        assertFalse(Glob.matches("a.b", "axb"), "a dot is a dot, not a wildcard");
    }

    @Test
    void classesAndEscapesAreRejectedAndNeverMatchLoosely() {
        assertNotNull(Glob.validate("[a-z]*"));
        assertNotNull(Glob.validate("a\\*"));
        assertNull(Glob.validate("acme/*"));
        assertTrue(Glob.matches("[ab]", "[ab]"), "an unsupported pattern only matches itself");
        assertFalse(Glob.matches("[ab]", "a"));
    }

    @Test
    void theFirstEnabledMatchingRuleWins() {
        PolicyEngine engine = new PolicyEngine(List.of(
                new Rule("1", "off", "", new Rule.Condition("github", "", ""), new Rule.Outcome("critical", "x", false), true, false),
                new Rule("2", "first", "", new Rule.Condition("github", "push", "acme/*"), new Rule.Outcome("background", "a", false), false, false),
                new Rule("3", "second", "", new Rule.Condition("github", "", ""), new Rule.Outcome("standard", "b", true), false, false)));

        PolicyDecision push = engine.evaluate(new Event("1", "github", "push", "acme/api", null)).orElseThrow();
        assertEquals("first", push.matchedRule());
        assertEquals("background", push.attentionClass());

        PolicyDecision other = engine.evaluate(new Event("2", "github", "push", "other/api", null)).orElseThrow();
        assertEquals("second", other.matchedRule());
        assertTrue(other.adaptive());

        Optional<PolicyDecision> none = engine.evaluate(new Event("3", "gitlab", "push", "acme/api", null));
        assertTrue(none.isEmpty());
    }

    @Test
    void aCriticalRuleIsNeverAdaptive() {
        PolicyEngine engine = new PolicyEngine(List.of(
                new Rule("1", "crit", "", new Rule.Condition("", "", ""), new Rule.Outcome("critical", "x", true), false, true)));
        assertFalse(engine.evaluate(new Event("1", "github", "push", "", null)).orElseThrow().adaptive());
    }

    @Test
    void theRulesTheGoServiceShipsLoadHere() {
        List<Rule> rules = RulesLoader.loadFile(GO_RULES);
        assertEquals(4, rules.size());
        assertEquals("github-security-advisory", rules.get(0).name());
        assertTrue(rules.get(0).locked());
        assertEquals("critical", rules.get(0).then().attentionClass());
        assertTrue(rules.get(3).then().adaptive());
    }

    @Test
    void aMisspeltKeyFailsInsteadOfWideningTheRule() {
        String yaml = """
                rules:
                  - name: typo
                    when:
                      eventTpye: push
                    then:
                      attentionClass: critical
                """;
        RulesLoader.InvalidRulesException e = assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.parse(yaml));
        assertTrue(e.getMessage().contains("eventTpye"), e.getMessage());
    }

    @Test
    void payloadMatchAndChannelsAreReadFromTheRulesFile() {
        String yaml = """
                rules:
                  - name: storm
                    when:
                      source: app
                      payloadMatch:
                        - field: metadata.attempt
                          op: gt
                          value: 3
                        - field: draft
                          op: exists
                          value: false
                    then:
                      attentionClass: standard
                      channels: [slack, email]
                """;
        Rule rule = RulesLoader.parse(yaml).get(0);
        assertEquals(2, rule.when().payloadMatch().size());
        assertEquals("gt", rule.when().payloadMatch().get(0).op());
        assertEquals(3, rule.when().payloadMatch().get(0).value());
        assertEquals(false, rule.when().payloadMatch().get(1).value());
        assertEquals(List.of("slack", "email"), rule.then().channels());
    }

    @Test
    void aBadPredicateOrChannelInTheRulesFileFailsAtLoadAndNamesTheRule() {
        String badOp = "rules: [ {name: a, when: {payloadMatch: [ {field: x, op: between, value: 1} ]}, then: {attentionClass: standard}} ]";
        var e = assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.parse(badOp));
        assertTrue(e.getMessage().contains("rule \"a\"") && e.getMessage().contains("between"), e.getMessage());
        String critical = "rules: [ {name: b, then: {attentionClass: critical, channels: [slack]}} ]";
        var c = assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.parse(critical));
        assertTrue(c.getMessage().contains("critical rule cannot choose channels"), c.getMessage());
        String typo = "rules: [ {name: c, when: {payloadMatch: [ {feild: x, value: 1} ]}, then: {attentionClass: standard}} ]";
        assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.parse(typo));
    }

    @Test
    void invalidRulesAreReportedByName() {
        assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.parse(""));
        assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.parse("rules: []"));
        assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.parse("rules: [ {then: {attentionClass: standard}} ]"));
        assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.parse("rules: [ {name: a} ]"));
        assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.parse("rules: [ {name: a, then: {}} ]"));
        RulesLoader.InvalidRulesException typo = assertThrows(RulesLoader.InvalidRulesException.class,
                () -> RulesLoader.parse("rules: [ {name: a, then: {attentionClass: critcal}} ]"));
        assertTrue(typo.getMessage().contains("critcal"));
        assertThrows(RulesLoader.InvalidRulesException.class,
                () -> RulesLoader.parse("rules: [ {name: a, when: {source: '[a-z]'}, then: {attentionClass: standard}} ]"));
        assertThrows(RulesLoader.InvalidRulesException.class, () -> RulesLoader.loadFile(Path.of("does-not-exist.yaml")));
    }
}
