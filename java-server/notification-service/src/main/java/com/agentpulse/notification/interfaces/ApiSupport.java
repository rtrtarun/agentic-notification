package com.agentpulse.notification.interfaces;

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

    private final Config config;

    ApiSupport(Config config) {
        this.config = config;
    }

    Config config() {
        return config;
    }

    /** The API key check. Answers 401 or 503 itself and returns false when the request may not continue. */
    boolean authorised(HttpExchange x) throws IOException {
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
