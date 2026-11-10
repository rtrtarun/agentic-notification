package com.agentpulse.notification.domain;

import java.util.List;

/**
 * The outcome of matching an event against the rules. {@code channels} is the matched rule's channel override;
 * empty means route by class. A critical decision never has one.
 */
public record PolicyDecision(String attentionClass, String reason, String matchedRule, boolean adaptive,
                             List<String> channels) {
    public PolicyDecision {
        channels = channels == null ? List.of() : List.copyOf(channels);
    }

    public PolicyDecision(String attentionClass, String reason, String matchedRule, boolean adaptive) {
        this(attentionClass, reason, matchedRule, adaptive, List.of());
    }
}
