package com.agentpulse.notification.application;

import java.security.SecureRandom;
import java.time.Instant;
import java.util.HexFormat;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** The delivery queue's vocabulary: jobs, statuses, errors and redaction. Same states as the Go service. */
public final class Delivery {
    private static final SecureRandom RANDOM = new SecureRandom();
    private static final Pattern URL = Pattern.compile("https?://[^\\s\"']+");
    private static final int MAX_ERROR_CHARS = 300;

    private Delivery() {}

    /** Where a job is in its life. */
    public static final class Status {
        /** Waits for its time; a job that failed once and will retry is pending too. */
        public static final String PENDING = "pending";
        /** Leased to a worker. A lease that expires makes the job claimable again. */
        public static final String SENDING = "sending";
        public static final String SENT = "sent";
        /** Used all attempts or can never succeed. A person can retry it. */
        public static final String DEAD = "dead";

        private Status() {}
    }

    /**
     * One delivery of one event through one channel. It holds the text but never the target: addresses and webhook
     * URLs come from configuration when the job is sent, so secrets never reach the database or the console.
     */
    public record Job(String id, String eventId, String channel, String attentionClass, String title, String message,
                      String group, String status, int attempts, Instant nextAttemptAt, Instant leaseUntil,
                      String lastError, boolean direct, Instant createdAt, Instant updatedAt) {

        /** A new pending job, due now. */
        public static Job pending(String eventId, String channel, String attentionClass, String title, String message,
                                  String group, Instant now) {
            return new Job(newId(), eventId, channel, attentionClass, title, message, group, Status.PENDING, 0, now,
                    Instant.EPOCH, "", false, now, now);
        }

        public Job with(String status, int attempts, Instant nextAttemptAt, Instant leaseUntil, String lastError,
                        Instant updatedAt) {
            return new Job(id, eventId, channel, attentionClass, title, message, group, status, attempts, nextAttemptAt,
                    leaseUntil, lastError, direct, createdAt, updatedAt);
        }
    }

    /** No job has this id. */
    public static final class NotFoundException extends RuntimeException {
        public NotFoundException() {
            super("delivery not found");
        }
    }

    /** Only a dead job can be retried. */
    public static final class NotRetryableException extends RuntimeException {
        public NotRetryableException() {
            super("only a dead delivery can be retried");
        }
    }

    /** A failure retrying cannot fix (no provider, channel not configured). The job goes straight to dead. */
    public static final class PermanentException extends RuntimeException {
        public PermanentException(String message) {
            super(message);
        }
    }

    /** A random identifier. */
    public static String newId() {
        byte[] b = new byte[12];
        RANDOM.nextBytes(b);
        return HexFormat.of().formatHex(b);
    }

    /**
     * Error text with every URL reduced to scheme and host (a webhook's path is its credential) and a bounded
     * length, safe to store and to show.
     */
    public static String redact(String text) {
        if (text == null) {
            return "";
        }
        Matcher m = URL.matcher(text);
        StringBuilder out = new StringBuilder();
        while (m.find()) {
            String url = m.group();
            int start = url.indexOf("://") + 3;
            int end = start;
            while (end < url.length() && "/?#".indexOf(url.charAt(end)) < 0) {
                end++;
            }
            m.appendReplacement(out, Matcher.quoteReplacement(end < url.length() ? url.substring(0, end) + "/…" : url));
        }
        m.appendTail(out);
        String result = out.toString();
        if (result.codePointCount(0, result.length()) > MAX_ERROR_CHARS) {
            result = result.substring(0, result.offsetByCodePoints(0, MAX_ERROR_CHARS)) + "…";
        }
        return result;
    }
}
