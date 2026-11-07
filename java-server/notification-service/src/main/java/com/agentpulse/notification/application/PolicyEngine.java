package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.PolicyDecision;
import com.agentpulse.notification.domain.Rule;

import java.util.List;
import java.util.Optional;

/** Evaluates rules in order; the first enabled match wins. Deterministic: no model is ever involved. */
public final class PolicyEngine {
    private volatile List<Rule> rules;

    public PolicyEngine(List<Rule> rules) {
        this.rules = List.copyOf(rules);
    }

    /** Replaces the rules. Evaluations already running keep the old list. */
    public void setRules(List<Rule> rules) {
        this.rules = List.copyOf(rules);
    }

    /** The decision of the first matching rule, or empty when no rule matches. */
    public Optional<PolicyDecision> evaluate(Event event) {
        for (Rule rule : rules) {
            if (rule.disabled() || !matches(rule.when(), event)) {
                continue;
            }
            Rule.Outcome then = rule.then();
            boolean adaptive = then.adaptive() && !Attention.CRITICAL.equals(then.attentionClass());
            return Optional.of(new PolicyDecision(then.attentionClass(), then.reason(), rule.name(), adaptive));
        }
        return Optional.empty();
    }

    /** True when the event satisfies every field the condition sets. */
    public static boolean matches(Rule.Condition c, Event event) {
        return Glob.matches(c.source(), event.getSource())
                && Glob.matches(c.eventType(), event.getEventType())
                && Glob.matches(c.repository(), event.getRepository());
    }
}
