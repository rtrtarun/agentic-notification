package com.agentpulse.notification.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * A policy rule: when an event matches {@code when}, it gets {@code then}. Rules run top to bottom and the
 * first match wins. Disabled rules never match; locked rules protect critical routing.
 *
 * <p>The JSON is the Go service's: empty strings and false flags are left out, so a rule written here reads the
 * same there. {@code payloadMatch} and per-rule {@code channels} are not supported yet and are refused.
 */
public record Rule(
        @JsonInclude(JsonInclude.Include.NON_EMPTY) String id,
        String name,
        @JsonInclude(JsonInclude.Include.NON_EMPTY) String description,
        Condition when,
        Outcome then,
        @JsonInclude(JsonInclude.Include.NON_DEFAULT) boolean disabled,
        @JsonInclude(JsonInclude.Include.NON_DEFAULT) boolean locked) {

    public Rule {
        id = id == null ? "" : id;
        description = description == null ? "" : description;
        when = when == null ? new Condition(null, null, null) : when;
    }

    /** Which events a rule applies to. An empty field matches anything; fields may use {@code *}. */
    public record Condition(
            @JsonInclude(JsonInclude.Include.NON_EMPTY) String source,
            @JsonInclude(JsonInclude.Include.NON_EMPTY) String eventType,
            @JsonInclude(JsonInclude.Include.NON_EMPTY) String repository) {
        public Condition {
            source = source == null ? "" : source;
            eventType = eventType == null ? "" : eventType;
            repository = repository == null ? "" : repository;
        }
    }

    /** What a matching rule decides. {@code adaptive} lets Attention Intelligence refine a non-critical outcome. */
    public record Outcome(
            String attentionClass,
            String reason,
            @JsonInclude(JsonInclude.Include.NON_DEFAULT) boolean adaptive) {
        public Outcome {
            reason = reason == null ? "" : reason;
        }
    }

    /** A copy with a different id. */
    public Rule withId(String newId) {
        return new Rule(newId, name, description, when, then, disabled, locked);
    }
}
