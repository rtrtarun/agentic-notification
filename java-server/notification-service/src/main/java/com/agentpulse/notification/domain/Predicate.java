package com.agentpulse.notification.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * One condition on the contents of an event, such as {@code metadata.attempt gt 3}. All predicates of a rule must
 * hold. {@code op} is eq (the default), ne, gt, gte, lt, lte or exists; {@code value} is a string, number or
 * boolean (optional for exists). Same meaning and JSON as the Go service.
 */
public record Predicate(
        String field,
        @JsonInclude(JsonInclude.Include.NON_EMPTY) String op,
        @JsonInclude(JsonInclude.Include.NON_NULL) Object value) {
    public Predicate {
        field = field == null ? "" : field;
        op = op == null ? "" : op;
    }
}
