package com.example.notification.application;

import com.example.notification.domain.Event;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * Deterministic deduplication and threading (Layer 1, never AI). The thread and key formats are identical to the
 * Go service so both services group events the same way.
 */
public final class Dedup {
    private Dedup() {}

    /** What a {@link Deduper} decided for one event. */
    public record Result(String dedupKey, String threadId, boolean duplicate, String originalId) {}

    /**
     * Assigns keys and detects duplicates. Implementations must fail open: when a backing store is unavailable
     * they report "not a duplicate", so no event is ever suppressed by an infrastructure fault.
     */
    public interface Deduper {
        /** Records the event and reports whether it duplicates a recent one. */
        Result check(Event event);

        /** Removes the entry for {@code key} if it was recorded by event {@code id}; used after a failed save. */
        void forget(String key, String id);
    }

    /** Groups related events about one subject, for example one pull request. */
    public static String threadId(Event event) {
        List<String> parts = new ArrayList<>(List.of(event.getSource(), event.getRepository()));
        String subject = subjectOf(event.getPayload());
        if (!subject.isEmpty()) {
            parts.add(subject);
        }
        return String.join(":", parts);
    }

    /**
     * Identifies a repeat of the same thing: same thread, type, action and, for pushes, the same head commit.
     * Without the commit, every push to a repository inside the window would collapse into the first.
     */
    public static String dedupKey(Event event) {
        Map<String, Object> payload = event.getPayload();
        String action = payload.get("action") instanceof String s ? s : "";
        String head = payload.get("after") instanceof String s ? s : "";
        return String.join("|", threadId(event), event.getEventType(), action, head);
    }

    private static String subjectOf(Map<String, Object> payload) {
        if (payload.get("number") instanceof Number n) {
            return Long.toString(n.longValue());
        }
        for (String key : List.of("pull_request", "issue")) {
            if (payload.get(key) instanceof Map<?, ?> obj && obj.get("number") instanceof Number n) {
                return Long.toString(n.longValue());
            }
        }
        return "";
    }
}
