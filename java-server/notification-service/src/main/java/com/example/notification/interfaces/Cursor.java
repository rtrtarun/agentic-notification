package com.example.notification.interfaces;

import com.example.notification.domain.Event;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Base64;

/**
 * An opaque position in a newest-first list: the creation time (nanoseconds) and id of the last item a client
 * received. The encoding is the Go service's, so a cursor from one service works on the other.
 */
record Cursor(Instant at, String id) {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final int MAX_ENCODED = 400;

    static Cursor of(Event e) {
        return new Cursor(e.getCreatedAt(), e.getId());
    }

    String encode() {
        long nanos = Math.addExact(Math.multiplyExact(at.getEpochSecond(), 1_000_000_000L), at.getNano());
        String raw = "{\"t\":" + nanos + ",\"i\":" + JSON.valueToTree(id) + "}";
        return Base64.getUrlEncoder().withoutPadding().encodeToString(raw.getBytes(StandardCharsets.UTF_8));
    }

    /** Reads a cursor made by {@link #encode}; returns null when it is malformed. */
    static Cursor decode(String s) {
        if (s == null || s.isEmpty() || s.length() > MAX_ENCODED) {
            return null;
        }
        try {
            JsonNode node = JSON.readTree(Base64.getUrlDecoder().decode(s));
            if (node == null || !node.path("t").isIntegralNumber() || !node.path("i").isTextual()
                    || node.path("i").asText().isEmpty()) {
                return null;
            }
            long nanos = node.get("t").asLong();
            return new Cursor(Instant.ofEpochSecond(Math.floorDiv(nanos, 1_000_000_000L), Math.floorMod(nanos, 1_000_000_000L)),
                    node.get("i").asText());
        } catch (IllegalArgumentException | java.io.IOException e) {
            return null;
        }
    }

    /** True when the event comes after the cursor in newest-first order: strictly older, id breaking ties. */
    boolean isAfter(Event e) {
        int byTime = e.getCreatedAt().compareTo(at);
        return byTime != 0 ? byTime < 0 : e.getId().compareTo(id) < 0;
    }
}
