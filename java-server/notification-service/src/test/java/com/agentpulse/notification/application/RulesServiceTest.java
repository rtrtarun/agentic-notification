package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.MemoryStores;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Managing rules while the service runs. */
class RulesServiceTest {
    private MemoryStores.Rules store;
    private PolicyEngine engine;
    private RulesService service;

    private static Rule seedRule(String name, String eventType, String cls, boolean locked) {
        return new Rule("", name, "", new Rule.Condition("github", eventType, ""),
                new Rule.Outcome(cls, name + " reason", false), false, locked);
    }

    private static Rule input(String name, String eventType, String cls) {
        return seedRule(name, eventType, cls, false);
    }

    @BeforeEach
    void setUp() {
        store = new MemoryStores.Rules();
        engine = new PolicyEngine(List.of());
        AtomicInteger n = new AtomicInteger();
        service = new RulesService(store, engine, List.of(
                seedRule("star", "star", "background", false),
                seedRule("advisory", "security_advisory", "critical", true),
                seedRule("default", "", "standard", false)), () -> "id" + n.incrementAndGet());
    }

    private String decide(String eventType) {
        return engine.evaluate(new Event("e", "github", eventType, "acme/api", null)).map(d -> d.matchedRule()).orElse("none");
    }

    @Test
    void theSeedFillsAnEmptyStoreAssignsIdsAndPutsLockedRulesFirst() {
        List<Rule> rules = service.list();
        assertEquals(List.of("advisory", "star", "default"), rules.stream().map(Rule::name).toList());
        assertTrue(rules.stream().noneMatch(r -> r.id().isEmpty()));
        assertEquals("advisory", decide("security_advisory"), "the engine got the rules at start-up");
    }

    @Test
    void aLockedSeedRuleMissingFromTheStoreIsAddedBack() {
        MemoryStores.Rules other = new MemoryStores.Rules();
        other.replace(List.of(seedRule("mine", "push", "standard", false).withId("m1")));
        PolicyEngine e2 = new PolicyEngine(List.of());
        RulesService s2 = new RulesService(other, e2, List.of(seedRule("advisory", "security_advisory", "critical", true)));
        assertEquals(List.of("advisory", "mine"), s2.list().stream().map(Rule::name).toList());
        assertTrue(s2.list().get(0).locked());
    }

    @Test
    void createAppendsAnUnlockedRuleAndTheEngineUsesItAtOnce() {
        Rule made = service.create(new Rule("ignored", "  quiet pushes ", " d ", new Rule.Condition(" github ", "push", ""),
                new Rule.Outcome("background", "", false), false, true));

        assertFalse(made.locked(), "a caller cannot lock a rule");
        assertEquals("quiet pushes", made.name());
        assertEquals("matched rule quiet pushes", made.then().reason(), "a missing reason gets a default");
        assertEquals("github", made.when().source());
        assertFalse(made.id().isEmpty());
        assertEquals("quiet pushes", service.list().get(service.list().size() - 1).name());
        assertEquals("default", decide("push"), "appended after the broad default rule, so the default still wins");
    }

    @Test
    void reorderChangesWhichRuleWinsAndKeepsLockedRulesFirst() {
        Rule made = service.create(input("quiet pushes", "push", "background"));
        List<String> ids = service.list().stream().map(Rule::id).toList();
        // advisory, star, default, quiet -> advisory, star, quiet, default
        service.reorder(List.of(ids.get(0), ids.get(1), made.id(), ids.get(2)));
        assertEquals("quiet pushes", decide("push"));

        assertEquals("invalid rule: locked rules must stay above all other rules",
                assertThrows(RulesService.InvalidException.class,
                        () -> service.reorder(List.of(ids.get(1), ids.get(0), made.id(), ids.get(2)))).getMessage());
        assertEquals("invalid rule: send every rule id exactly once",
                assertThrows(RulesService.InvalidException.class, () -> service.reorder(List.of(ids.get(0)))).getMessage());
        assertEquals("invalid rule: unknown or repeated rule id",
                assertThrows(RulesService.InvalidException.class,
                        () -> service.reorder(List.of(ids.get(0), ids.get(0), ids.get(1), ids.get(2)))).getMessage());
    }

    @Test
    void updateKeepsTheIdAndPositionAndLetsARuleKeepItsOwnName() {
        Rule made = service.create(input("quiet pushes", "push", "background"));
        Rule changed = service.update(made.id(), input("Quiet Pushes", "push", "standard"));

        assertEquals(made.id(), changed.id());
        assertEquals("Quiet Pushes", changed.name());
        assertEquals("standard", changed.then().attentionClass());
        assertEquals(made.id(), service.list().get(service.list().size() - 1).id());
    }

    @Test
    void lockedRulesCannotBeEditedOrRemovedAndUnknownIdsAreNotFound() {
        String locked = service.list().get(0).id();
        assertEquals("this rule is locked: it protects critical routing",
                assertThrows(RulesService.LockedException.class, () -> service.update(locked, input("x", "y", "standard"))).getMessage());
        assertThrows(RulesService.LockedException.class, () -> service.delete(locked));
        assertEquals("rule not found", assertThrows(RulesService.NotFoundException.class, () -> service.delete("nope")).getMessage());
        assertThrows(RulesService.NotFoundException.class, () -> service.update("nope", input("x", "y", "standard")));
    }

    @Test
    void deleteRemovesAnUnlockedRule() {
        Rule made = service.create(input("quiet pushes", "push", "background"));
        service.delete(made.id());
        assertTrue(service.list().stream().noneMatch(r -> r.name().equals("quiet pushes")));
    }

    @Test
    void aDisabledRuleNeverMatches() {
        Rule off = new Rule("", "no stars", "", new Rule.Condition("github", "star", ""),
                new Rule.Outcome("critical", "x", false), true, false);
        service.create(off);
        assertEquals("star", decide("star"));
    }

    private String invalid(Rule r) {
        return assertThrows(RulesService.InvalidException.class, () -> service.create(r)).getMessage();
    }

    @Test
    void invalidRulesAreRefusedWithTheGoMessages() {
        assertEquals("invalid rule: name is required and at most 80 characters", invalid(input("  ", "x", "standard")));
        assertEquals("invalid rule: name is required and at most 80 characters", invalid(input("n".repeat(81), "x", "standard")));
        assertEquals("invalid rule: a rule named \"STAR\" already exists", invalid(input("STAR", "x", "standard")));
        assertEquals("invalid rule: attentionClass must be critical, standard or background", invalid(input("a", "x", "urgent")));
        assertEquals("invalid rule: attentionClass must be critical, standard or background", invalid(input("a", "x", null)));
        assertEquals("invalid rule: a critical rule cannot be adaptive; critical events never reach Attention Intelligence",
                invalid(new Rule("", "a", "", new Rule.Condition("", "", ""), new Rule.Outcome("critical", "r", true), false, false)));
        assertEquals("invalid rule: reason is at most 200 characters",
                invalid(new Rule("", "a", "", new Rule.Condition("", "", ""), new Rule.Outcome("standard", "r".repeat(201), false), false, false)));
        assertEquals("invalid rule: description is at most 300 characters",
                invalid(new Rule("", "a", "d".repeat(301), new Rule.Condition("", "", ""), new Rule.Outcome("standard", "r", false), false, false)));
        assertEquals("invalid rule: eventType is not a valid pattern",
                invalid(new Rule("", "a", "", new Rule.Condition("", "[a-z]", ""), new Rule.Outcome("standard", "r", false), false, false)));
        assertEquals("invalid rule: repository is at most 100 characters",
                invalid(new Rule("", "a", "", new Rule.Condition("", "", "r".repeat(101)), new Rule.Outcome("standard", "r", false), false, false)));
    }

    @Test
    void thereIsALimitOnTheNumberOfRules() {
        for (int i = service.list().size(); i < RulesService.MAX_RULES; i++) {
            service.create(input("rule " + i, "e" + i, "standard"));
        }
        assertEquals("invalid rule: at most 100 rules", invalid(input("one too many", "x", "standard")));
    }

    @Test
    void aDryRunNamesTheWinnerAndFlagsCriticalAsBypassingAi() {
        RulesService.Result critical = service.test(new RulesService.Sample("github", "security_advisory", "acme/api"));
        assertTrue(critical.matched() && critical.aiBypassed());
        assertEquals("advisory", critical.matchedRule());
        assertEquals(1, critical.steps().stream().filter(RulesService.Step::winner).count());

        RulesService.Result none = service.test(new RulesService.Sample("gitlab", "push", ""));
        assertFalse(none.matched());
        assertEquals("standard", none.attentionClass());
        assertEquals("no matching rule, default standard", none.reason());
        assertFalse(none.aiBypassed());
        assertEquals("", none.matchedRule());
    }

    @Test
    void thePolicySummaryDescribesTheLiveRules() {
        service.create(new Rule("", "off", "", new Rule.Condition("github", "x", ""), new Rule.Outcome("standard", "r", false), true, false));
        var summary = PolicySummary.describe(java.time.Duration.ofMinutes(10), java.time.Duration.ofDays(90), service.list());
        assertEquals(600L, summary.get("dedupWindowSeconds"));
        assertEquals(2160L, summary.get("retentionHours"));
        assertEquals(List.of("advisory"), summary.get("lockedRules"));
        assertEquals(4, summary.get("ruleCount"));
        assertEquals(1, summary.get("disabledRuleCount"));
        assertEquals(4, ((List<?>) summary.get("guardrails")).size());
        assertEquals(3, ((List<?>) summary.get("classes")).size());
    }
}
