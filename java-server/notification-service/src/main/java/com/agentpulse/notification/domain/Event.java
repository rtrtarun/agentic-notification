package com.agentpulse.notification.domain;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonInclude;

import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.Map;

/** A normalised event. The pipeline fills in the attention class, decision, dedup key and thread. */
@JsonInclude(JsonInclude.Include.NON_EMPTY)
public final class Event {
    private final String id;
    private final String source;
    private final String eventType;
    private final String repository;
    private final Map<String, Object> payload;
    private String attentionClass = Attention.STANDARD;
    private DecisionState decisionState;
    private String dedupKey = "";
    private String threadId = "";
    private String mergedInto = "";
    private Instant createdAt;

    public Event(String id, String source, String eventType, String repository, Map<String, Object> payload) {
        this.id = id;
        this.source = source;
        this.eventType = eventType;
        this.repository = repository == null ? "" : repository;
        this.payload = payload == null ? new LinkedHashMap<>() : payload;
    }

    /** A copy that can change without affecting the original. The payload is shared and never edited. */
    public Event copy() {
        Event c = new Event(id, source, eventType, repository, payload);
        c.attentionClass = attentionClass;
        c.decisionState = decisionState == null ? null : decisionState.copy();
        c.dedupKey = dedupKey;
        c.threadId = threadId;
        c.mergedInto = mergedInto;
        c.createdAt = createdAt;
        return c;
    }

    public String getId() { return id; }
    public String getSource() { return source; }
    public String getEventType() { return eventType; }
    public String getRepository() { return repository; }
    public Map<String, Object> getPayload() { return payload; }
    public String getAttentionClass() { return attentionClass; }
    public void setAttentionClass(String attentionClass) { this.attentionClass = attentionClass; }
    public DecisionState getDecisionState() { return decisionState; }
    public void setDecisionState(DecisionState decisionState) { this.decisionState = decisionState; }
    public String getDedupKey() { return dedupKey; }
    public void setDedupKey(String dedupKey) { this.dedupKey = dedupKey == null ? "" : dedupKey; }
    public String getThreadId() { return threadId; }
    public void setThreadId(String threadId) { this.threadId = threadId == null ? "" : threadId; }
    public String getMergedInto() { return mergedInto; }
    public void setMergedInto(String mergedInto) { this.mergedInto = mergedInto == null ? "" : mergedInto; }
    public Instant getCreatedAt() { return createdAt; }
    public void setCreatedAt(Instant createdAt) { this.createdAt = createdAt; }

    /** True for an event the rules classed as critical. */
    @JsonIgnore
    public boolean isCritical() { return Attention.CRITICAL.equals(attentionClass); }
}
