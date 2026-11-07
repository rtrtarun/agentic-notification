package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.infrastructure.Config;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpExchange;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.Map;
import java.util.Optional;

/** What every handler shares: the API-key check, size-limited body reading and JSON replies. */
final class ApiSupport {
    static final ObjectMapper JSON = new ObjectMapper().configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);

    static final String SESSION_COOKIE = "attn_session";
    static final String CSRF_HEADER = "X-CSRF-Token";

    /** Who made a request: a service holding the API key, or a signed-in person with a session. */
    record Principal(boolean service, Accounts.User user, Accounts.Session session) {
        boolean admin() {
            return service || (user != null && Accounts.ROLE_ADMIN.equals(user.role()));
        }

        String actor() {
            return service ? "api-key" : user.email();
        }
    }

    private final Config config;
    private volatile Accounts.Service accounts;

    ApiSupport(Config config) {
        this.config = config;
    }

    /** Lets signed-in people use the protected endpoints. Without it only the API key works. */
    ApiSupport withAccounts(Accounts.Service accounts) {
        this.accounts = accounts;
        return this;
    }

    /** SameSite for the session cookie, as written in the header. */
    String sameSite() {
        return switch (config.authCookieSameSite()) {
            case "strict" -> "Strict";
            case "none" -> "None";
            default -> "Lax";
        };
    }

    /** Whether the session cookie is marked Secure. SameSite=None always needs it. */
    boolean secureCookie(HttpExchange x) {
        if ("none".equals(config.authCookieSameSite())) {
            return true;
        }
        return switch (config.authCookieSecure()) {
            case "true" -> true;
            case "false" -> false;
            default -> "https".equalsIgnoreCase(x.getRequestHeaders().getFirst("X-Forwarded-Proto"));
        };
    }

    /** The session cookie's value, or empty. */
    String sessionToken(HttpExchange x) {
        java.util.List<String> headers = x.getRequestHeaders().get("Cookie");
        if (headers != null) {
            for (String header : headers) {
                for (String part : header.split(";")) {
                    String p = part.trim();
                    if (p.startsWith(SESSION_COOKIE + "=")) {
                        return p.substring(SESSION_COOKIE.length() + 1);
                    }
                }
            }
        }
        return "";
    }

    private static boolean unsafeMethod(String method) {
        return !method.equals("GET") && !method.equals("HEAD") && !method.equals("OPTIONS");
    }

    /**
     * Works out who is calling. The API key works as before; otherwise a valid session cookie works, and a
     * cookie that changes anything must also carry the session's CSRF token. Answers 401, 403 or 503 itself and
     * returns null when the request may not continue.
     */
    Principal authenticate(HttpExchange x) throws IOException {
        String key = x.getRequestHeaders().getFirst("X-API-Key");
        boolean hasKey = key != null && !key.isEmpty();
        if (!hasKey && accounts != null) {
            String token = sessionToken(x);
            if (!token.isEmpty()) {
                Optional<Accounts.Authenticated> who = accounts.authenticate(token);
                if (who.isEmpty()) {
                    error(x, 401, "missing or invalid api key");
                    return null;
                }
                String csrf = x.getRequestHeaders().getFirst(CSRF_HEADER);
                if (unsafeMethod(x.getRequestMethod()) && (csrf == null || !sameSecret(who.get().session().csrf(), csrf))) {
                    error(x, 403, "missing or invalid csrf token");
                    return null;
                }
                return new Principal(false, who.get().user(), who.get().session());
            }
        }
        return authorisedByKey(x) ? new Principal(true, null, null) : null;
    }

    /** Like {@link #authorised} but also requires an admin: the API key, or a signed-in admin. */
    boolean authorisedAdmin(HttpExchange x) throws IOException {
        Principal p = authenticate(x);
        if (p == null) {
            return false;
        }
        if (!p.admin()) {
            error(x, 403, "admin access required");
            return false;
        }
        return true;
    }

    Config config() {
        return config;
    }

    /** Whether the caller may use a protected endpoint: the API key or a signed-in person. Answers itself when not. */
    boolean authorised(HttpExchange x) throws IOException {
        return authenticate(x) != null;
    }

    /** The API key check. Answers 401 or 503 itself and returns false when the request may not continue. */
    private boolean authorisedByKey(HttpExchange x) throws IOException {
        String key = x.getRequestHeaders().getFirst("X-API-Key");
        String want = config.apiKey();
        if (key != null && !key.isEmpty() && !want.isEmpty()) {
            if (sameSecret(want, key)) {
                return true;
            }
            error(x, 401, "missing or invalid api key");
            return false;
        }
        if (want.isEmpty()) {
            if (config.allowUnauthenticated()) {
                return true;
            }
            error(x, 503, "api key not configured");
            return false;
        }
        error(x, 401, "missing or invalid api key");
        return false;
    }

    /** Compares in constant time. Hashing first gives both sides one length, so the length does not leak. */
    static boolean sameSecret(String want, String got) {
        try {
            MessageDigest sha = MessageDigest.getInstance("SHA-256");
            return MessageDigest.isEqual(sha.digest(want.getBytes(StandardCharsets.UTF_8)),
                    sha.digest(got.getBytes(StandardCharsets.UTF_8)));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    /** Reads the body up to the configured limit; answers 413 or 400 itself and returns null when it cannot. */
    byte[] readBody(HttpExchange x) throws IOException {
        long limit = config.maxBodyBytes() > 0 ? config.maxBodyBytes() : Config.DEFAULT_MAX_BODY_BYTES;
        try (InputStream in = x.getRequestBody()) {
            byte[] body = in.readNBytes((int) limit + 1);
            if (body.length > limit) {
                error(x, 413, "request body too large");
                return null;
            }
            return body;
        } catch (IOException e) {
            error(x, 400, "unreadable request body");
            return null;
        }
    }

    /** Reads a size-limited JSON body. Answers 413 or 400 itself and returns empty when it cannot. */
    Optional<JsonNode> readJson(HttpExchange x) throws IOException {
        byte[] body = readBody(x);
        if (body == null) {
            return Optional.empty();
        }
        try {
            JsonNode node = JSON.readTree(body);
            if (node == null || node.isMissingNode()) {
                error(x, 400, "invalid request payload");
                return Optional.empty();
            }
            return Optional.of(node);
        } catch (IOException e) {
            error(x, 400, "invalid request payload");
            return Optional.empty();
        }
    }

    /** Answers 405 with an Allow header. */
    void methodNotAllowed(HttpExchange x, String allow) throws IOException {
        x.getResponseHeaders().set("Allow", allow);
        error(x, 405, "method not allowed");
    }

    void error(HttpExchange x, int status, String message) throws IOException {
        reply(x, status, Map.of("error", message));
    }

    void reply(HttpExchange x, int status, Object body) throws IOException {
        byte[] bytes;
        try {
            bytes = JSON.writeValueAsBytes(body);
        } catch (JsonProcessingException e) {
            bytes = "{\"error\":\"internal error\"}".getBytes(StandardCharsets.UTF_8);
            status = 500;
        }
        x.getResponseHeaders().set("Content-Type", "application/json");
        x.sendResponseHeaders(status, bytes.length);
        try (OutputStream out = x.getResponseBody()) {
            out.write(bytes);
        }
    }

    /** An empty reply, such as 204. */
    void replyEmpty(HttpExchange x, int status) throws IOException {
        x.sendResponseHeaders(status, -1);
    }
}
