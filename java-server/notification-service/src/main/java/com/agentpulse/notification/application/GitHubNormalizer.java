package com.agentpulse.notification.application;

import com.agentpulse.notification.domain.Event;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.UUID;

/** Maps a GitHub webhook to the internal event. */
public final class GitHubNormalizer {
    static final int MAX_ID_LENGTH = 64;

    private GitHubNormalizer() {}

    /**
     * The event id embeds GitHub's delivery id so a redelivery maps to the same id and is recognised. Without a
     * usable delivery id a random one is generated.
     */
    public static Event normalize(String eventType, String deliveryId, Map<String, Object> body) {
        Map<String, Object> payload = body == null ? new LinkedHashMap<>() : body;
        String repository = "";
        if (payload.get("repository") instanceof Map<?, ?> repo && repo.get("full_name") instanceof String name) {
            repository = name;
        }
        String delivery = validDeliveryId(deliveryId) ? deliveryId : UUID.randomUUID().toString().replace("-", "");
        return new Event("github-" + eventType + "-" + delivery, "github", eventType, repository, payload);
    }

    /** GitHub event names are short, lowercase letters and underscores. */
    public static boolean validEventType(String type) {
        if (type == null || type.isEmpty() || type.length() > MAX_ID_LENGTH) {
            return false;
        }
        for (char c : type.toCharArray()) {
            if ((c < 'a' || c > 'z') && c != '_') {
                return false;
            }
        }
        return true;
    }

    /** Short ids of letters, digits and dashes (GitHub sends a GUID). */
    public static boolean validDeliveryId(String id) {
        if (id == null || id.isEmpty() || id.length() > MAX_ID_LENGTH) {
            return false;
        }
        for (char c : id.toCharArray()) {
            boolean ok = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-';
            if (!ok) {
                return false;
            }
        }
        return true;
    }
}
