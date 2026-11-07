package com.example.notification.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

/** The decision taken for an event and why. Empty fields are left out of the JSON, as in the Go service. */
@JsonInclude(JsonInclude.Include.NON_DEFAULT)
public final class DecisionState {
    private String status = "";
    private String reason = "";
    private String modelVersion = "";
    private double confidence;
    private String matchedRule = "";

    public DecisionState() {}

    public DecisionState(String reason, String matchedRule) {
        this.reason = reason == null ? "" : reason;
        this.matchedRule = matchedRule == null ? "" : matchedRule;
    }

    /** A copy that can change without affecting the original. */
    public DecisionState copy() {
        DecisionState c = new DecisionState(reason, matchedRule);
        c.status = status;
        c.modelVersion = modelVersion;
        c.confidence = confidence;
        return c;
    }

    public String getStatus() { return status; }
    public void setStatus(String status) { this.status = status == null ? "" : status; }
    public String getReason() { return reason; }
    public void setReason(String reason) { this.reason = reason == null ? "" : reason; }
    public String getModelVersion() { return modelVersion; }
    public void setModelVersion(String modelVersion) { this.modelVersion = modelVersion == null ? "" : modelVersion; }
    public double getConfidence() { return confidence; }
    public void setConfidence(double confidence) { this.confidence = confidence; }
    public String getMatchedRule() { return matchedRule; }
    public void setMatchedRule(String matchedRule) { this.matchedRule = matchedRule == null ? "" : matchedRule; }
}
