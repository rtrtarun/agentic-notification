package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Rule;

import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * The effective policy as plain data, for {@code GET /api/policies}. The guardrails and class behaviour are facts of
 * the pipeline, not settings; the window, retention and rules are live values. Text is the Go service's.
 */
public final class PolicySummary {
    private PolicySummary() {}

    public static Map<String, Object> describe(Duration dedupWindow, Duration retention, List<Rule> rules) {
        List<String> locked = new ArrayList<>();
        int disabled = 0;
        for (Rule r : rules) {
            if (r.locked()) {
                locked.add(r.name());
            }
            if (r.disabled()) {
                disabled++;
            }
        }
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("guardrails", List.of(
                guardrail("never-suppress-critical", "Critical events are never suppressed",
                        "Critical events skip deduplication, budgets and digests. They are delivered to every configured channel."),
                guardrail("no-ai-in-layer-1", "No AI in the policy layer",
                        "Classification by rules, deduplication and threading are deterministic. Critical events never reach the AI layer."),
                guardrail("locked-rules-first", "Locked rules run first",
                        "Rules that protect critical routing cannot be edited, disabled, removed or placed below other rules."),
                guardrail("critical-priority-fixed", "Critical priority cannot be lowered",
                        "A person can raise or lower standard and background notifications, never a critical one.")));
        out.put("classes", List.of(
                policy("critical", "critical", "immediate, every configured channel", "never merged", false,
                        "Security alerts, incidents, payment failures and outages."),
                policy("standard", "high or normal", "held and shown in the inbox", "repeats merged inside the window", true,
                        "Normal work that does not need an interruption."),
                policy("background", "low", "recorded quietly", "repeats merged inside the window", true,
                        "Low-value signals such as stars and watches.")));
        out.put("dedupWindowSeconds", dedupWindow.toSeconds());
        out.put("retentionHours", retention.toHours());
        out.put("lockedRules", locked);
        out.put("ruleCount", rules.size());
        out.put("disabledRuleCount", disabled);
        return out;
    }

    private static Map<String, Object> guardrail(String id, String title, String description) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", id);
        m.put("title", title);
        m.put("description", description);
        return m;
    }

    private static Map<String, Object> policy(String cls, String level, String delivery, String dedup, boolean ai,
                                              String description) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("class", cls);
        m.put("level", level);
        m.put("delivery", delivery);
        m.put("dedup", dedup);
        m.put("aiEligible", ai);
        m.put("description", description);
        return m;
    }
}
