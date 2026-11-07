package com.agentpulse.notification.domain;

/** The outcome of matching an event against the rules. */
public record PolicyDecision(String attentionClass, String reason, String matchedRule, boolean adaptive) {}
