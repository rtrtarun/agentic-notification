package com.example.notification.domain;

/**
 * A policy rule: when an event matches {@code when}, it gets {@code then}. Rules run top to bottom and the
 * first match wins. Disabled rules never match; locked rules protect critical routing.
 */
public record Rule(String id, String name, String description, Condition when, Outcome then,
                   boolean disabled, boolean locked) {

    public Rule {
        when = when == null ? new Condition(null, null, null) : when;
    }

    /** Which events a rule applies to. An empty field matches anything; fields may use {@code *}. */
    public record Condition(String source, String eventType, String repository) {
        public Condition {
            source = source == null ? "" : source;
            eventType = eventType == null ? "" : eventType;
            repository = repository == null ? "" : repository;
        }
    }

    /** What a matching rule decides. {@code adaptive} lets Attention Intelligence refine a non-critical outcome. */
    public record Outcome(String attentionClass, String reason, boolean adaptive) {
        public Outcome {
            reason = reason == null ? "" : reason;
        }
    }
}
