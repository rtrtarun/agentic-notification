package com.agentpulse.notification.domain;

/** What happened to an event: the decision status stored with it. Same values as the Go service. */
public final class Status {
    public static final String SENT = "sent";
    public static final String QUEUED = "queued";
    public static final String MERGED = "merged";
    public static final String DIGEST = "digest";
    public static final String DELAYED = "delayed";
    public static final String BUDGET_EXCEEDED = "budget_exceeded";

    private Status() {}
}
