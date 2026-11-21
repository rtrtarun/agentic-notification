package com.agentpulse.notification.domain;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonInclude;

import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
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
    private String traceId = "";
    private String parentEventId = "";
    private String agentName = "";
    private Instant createdAt;
    private List<String> routeChannels = List.of();

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
        c.traceId = traceId;
        c.parentEventId = parentEventId;
        c.agentName = agentName;
        c.createdAt = createdAt;
        c.routeChannels = routeChannels;
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
    /** The agent run this step belongs to; empty for events that are not part of one (webhooks). */
    public String getTraceId() { return traceId; }
    public void setTraceId(String traceId) { this.traceId = traceId == null ? "" : traceId; }
    /** The step of the same run that led to this one; empty for a first step. */
    public String getParentEventId() { return parentEventId; }
    public void setParentEventId(String parentEventId) { this.parentEventId = parentEventId == null ? "" : parentEventId; }
    /** Which agent reported this step; empty when none was named. */
    public String getAgentName() { return agentName; }
    public void setAgentName(String agentName) { this.agentName = agentName == null ? "" : agentName; }
    public Instant getCreatedAt() { return createdAt; }
    public void setCreatedAt(Instant createdAt) { this.createdAt = createdAt; }

    /** The matched rule's channel override, set while the pipeline runs; never stored or serialised. Empty routes by class. */
    @JsonIgnore
    public List<String> getRouteChannels() { return routeChannels; }

    @JsonIgnore
    public void setRouteChannels(List<String> channels) { this.routeChannels = channels == null ? List.of() : List.copyOf(channels); }

    /** True for an event the rules classed as critical. */
    @JsonIgnore
    public boolean isCritical() { return Attention.CRITICAL.equals(attentionClass); }
}
