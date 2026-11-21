package com.agentpulse.notification.interfaces;

import java.util.Set;

/**
 * Low-cardinality route labels, ported from the Go service's {@code routeLabel} with the same list, so a request's
 * span has the same name in both services ({@code "<method> <route>"}). Ids in a path never become a label.
 */
final class Routes {
    static final String INBOX_PREFIX = "/api/notifications";
    /** Go's {@code knownRoutes}, including routes only the Go service serves, so labels stay identical. */
    static final Set<String> KNOWN = Set.of("/notify", "/health", "/metrics", "/webhooks/github", "/feedback", "/events",
            INBOX_PREFIX, "/api/analytics", "/api/activity", "/api/rules", "/api/policies", "/api/channels",
            "/api/deliveries", "/api/auth", "/api/users", "/api/audit", "/api/overrides", "/api/digests", "/api/explain",
            "/api/attention", "/api/learning", "/api/events", "/api/mcp", "/api/traces", "/api/me", "/api/integrations",
            "/api/preferences", "/api/stream");

    private Routes() {}

    /** The label for a path: itself when known, its {@code /api/} collection, or {@code other}. */
    static String label(String path) {
        if (path.equals(INBOX_PREFIX) || path.startsWith(INBOX_PREFIX + "/")) {
            return INBOX_PREFIX;
        }
        if (KNOWN.contains(path)) {
            return path;
        }
        if (path.startsWith("/api/")) {
            String rest = path.substring("/api/".length());
            int slash = rest.indexOf('/');
            String collection = "/api/" + (slash < 0 ? rest : rest.substring(0, slash));
            if (KNOWN.contains(collection)) {
                return collection;
            }
        }
        return "other";
    }

    /** The route a request's server span is named after, or null for /health, which is not traced. */
    static String traced(String path) {
        return path.equals("/health") ? null : label(path);
    }
}
