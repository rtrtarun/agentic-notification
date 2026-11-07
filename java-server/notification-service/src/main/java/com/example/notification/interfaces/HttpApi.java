package com.example.notification.interfaces;

import com.example.notification.application.GitHubNormalizer;
import com.example.notification.application.Pipeline;
import com.example.notification.application.Ports.EventRepository;
import com.example.notification.domain.Event;
import com.example.notification.infrastructure.Config;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpServer;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executors;

/**
 * The HTTP interface: it decodes a request, calls one pipeline or repository method and encodes the answer.
 * No business decision is made here. Paths, status codes and bodies match the Go service, which the shared
 * contract tests in {@code docs/contract} check.
 */
public final class HttpApi {
    private static final Logger LOG = LoggerFactory.getLogger(HttpApi.class);
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final TypeReference<Map<String, Object>> MAP = new TypeReference<>() {};

    static final int DEFAULT_EVENTS_LIMIT = 100;
    static final int MAX_EVENTS_LIMIT = 500;
    private static final int LIST_WINDOW = 1000;

    private final Config config;
    private final Pipeline pipeline;
    private final EventRepository events;

    public HttpApi(Config config, Pipeline pipeline, EventRepository events) {
        this.config = config;
        this.pipeline = pipeline;
        this.events = events;
    }

    /** Starts serving on the given port (0 picks a free one) and returns the running server. */
    public HttpServer start(int port) throws IOException {
        HttpServer server = HttpServer.create(new InetSocketAddress(port), 0);
        server.createContext("/", logged(this::route));
        server.setExecutor(Executors.newFixedThreadPool(16));
        server.start();
        return server;
    }

    private void route(HttpExchange x) throws IOException {
        switch (x.getRequestURI().getPath()) {
            case "/health" -> reply(x, 200, Map.of("status", "ok", "service", "attention-orchestration"));
            case "/webhooks/github" -> webhook(x);
            case "/events" -> listEvents(x);
            default -> error(x, 404, "not found");
        }
    }

    // ---- webhook -------------------------------------------------------------------------------------------

    private void webhook(HttpExchange x) throws IOException {
        String secret = config.githubWebhookSecret();
        if (secret.isEmpty() && !config.allowUnsignedWebhooks()) {
            error(x, 503, "webhook secret not configured");
            return;
        }
        byte[] body = readBody(x);
        if (body == null) {
            return;
        }
        if (!secret.isEmpty() && !Signature.valid(secret, body, x.getRequestHeaders().getFirst("X-Hub-Signature-256"))) {
            LOG.warn("rejected webhook with invalid signature remote={}", x.getRemoteAddress());
            error(x, 401, "invalid signature");
            return;
        }
        if (!"POST".equals(x.getRequestMethod())) {
            error(x, 405, "method not allowed");
            return;
        }
        String eventType = x.getRequestHeaders().getFirst("X-GitHub-Event");
        if (!GitHubNormalizer.validEventType(eventType)) {
            error(x, 400, "missing or invalid github event header");
            return;
        }
        Map<String, Object> payload;
        try {
            payload = JSON.readValue(body, MAP);
        } catch (IOException e) {
            error(x, 400, "invalid request payload");
            return;
        }

        Event normalized = GitHubNormalizer.normalize(eventType, x.getRequestHeaders().getFirst("X-GitHub-Delivery"), payload);
        Event result;
        try {
            result = pipeline.handle(normalized);
        } catch (Pipeline.DuplicateDeliveryException e) {
            LOG.info("ignored redelivered event event_id={}", normalized.getId());
            reply(x, 200, Map.of("status", "duplicate", "eventId", normalized.getId()));
            return;
        } catch (RuntimeException e) {
            LOG.error("pipeline failed event_id={} event_type={} error={}", normalized.getId(), eventType, e.getMessage());
            error(x, 500, "internal error");
            return;
        }
        LOG.info("event processed event_id={} thread_id={} attention_class={} decision_status={}",
                result.getId(), result.getThreadId(), result.getAttentionClass(), result.getDecisionState().getStatus());
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("status", "accepted");
        out.put("eventId", result.getId());
        out.put("attentionClass", result.getAttentionClass());
        out.put("decisionState", result.getDecisionState());
        reply(x, 202, out);
    }

    /** Reads the body up to the configured limit; answers 413 or 400 itself and returns null when it cannot. */
    private byte[] readBody(HttpExchange x) throws IOException {
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

    // ---- events --------------------------------------------------------------------------------------------

    private void listEvents(HttpExchange x) throws IOException {
        if (!authorised(x)) {
            return;
        }
        if (!"GET".equals(x.getRequestMethod())) {
            error(x, 405, "method not allowed");
            return;
        }
        Map<String, String> query = Query.parse(x.getRequestURI().getRawQuery());

        int limit = DEFAULT_EVENTS_LIMIT;
        if (query.containsKey("limit")) {
            try {
                limit = Integer.parseInt(query.get("limit"));
            } catch (NumberFormatException e) {
                limit = 0;
            }
            if (limit < 1 || limit > MAX_EVENTS_LIMIT) {
                error(x, 400, "limit must be a whole number from 1 to " + MAX_EVENTS_LIMIT);
                return;
            }
        }
        Cursor cursor = null;
        if (query.containsKey("cursor")) {
            cursor = Cursor.decode(query.get("cursor"));
            if (cursor == null) {
                error(x, 400, "cursor is not valid; use the nextCursor from a previous page");
                return;
            }
        }

        List<Event> stored;
        try {
            stored = new ArrayList<>(events.listRecent(LIST_WINDOW));
        } catch (RuntimeException e) {
            LOG.error("list events failed error={}", e.getMessage());
            error(x, 500, "internal error");
            return;
        }
        // Newest first, ties broken by id, so a cursor names one position exactly.
        stored.sort(Comparator.comparing(Event::getCreatedAt).thenComparing(Event::getId).reversed());

        List<Map<String, Object>> page = new ArrayList<>();
        boolean more = false;
        Event last = null;
        for (Event e : stored) {
            if (cursor != null && !cursor.isAfter(e)) {
                continue;
            }
            if (page.size() == limit) {
                more = true;
                break;
            }
            page.add(summary(e));
            last = e;
        }
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("events", page);
        if (more) {
            out.put("nextCursor", Cursor.of(last).encode());
        }
        reply(x, 200, out);
    }

    /** The read model for the list: the payload is left out because it can carry names and emails. */
    private static Map<String, Object> summary(Event e) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", e.getId());
        m.put("source", e.getSource());
        m.put("eventType", e.getEventType());
        putIfNotEmpty(m, "repository", e.getRepository());
        m.put("attentionClass", e.getAttentionClass());
        if (e.getDecisionState() != null) {
            m.put("decisionState", e.getDecisionState());
        }
        putIfNotEmpty(m, "threadId", e.getThreadId());
        putIfNotEmpty(m, "mergedInto", e.getMergedInto());
        m.put("createdAt", DateTimeFormatter.ISO_INSTANT.format(e.getCreatedAt()));
        return m;
    }

    private static void putIfNotEmpty(Map<String, Object> m, String key, String value) {
        if (!value.isEmpty()) {
            m.put(key, value);
        }
    }

    // ---- shared --------------------------------------------------------------------------------------------

    /** The API key check. Answers 401 or 503 itself and returns false when the request may not continue. */
    private boolean authorised(HttpExchange x) throws IOException {
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
    private static boolean sameSecret(String want, String got) {
        try {
            MessageDigest sha = MessageDigest.getInstance("SHA-256");
            return MessageDigest.isEqual(sha.digest(want.getBytes(StandardCharsets.UTF_8)),
                    sha.digest(got.getBytes(StandardCharsets.UTF_8)));
        } catch (java.security.NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    private HttpHandler logged(HttpHandler next) {
        return x -> {
            long started = System.nanoTime();
            try {
                next.handle(x);
            } catch (RuntimeException e) {
                LOG.error("unhandled error path={} error={}", x.getRequestURI().getPath(), e.toString());
                error(x, 500, "internal error");
            } finally {
                LOG.info("request method={} path={} status={} ms={}", x.getRequestMethod(), x.getRequestURI().getPath(),
                        x.getResponseCode(), (System.nanoTime() - started) / 1_000_000);
                x.close();
            }
        };
    }

    private static void error(HttpExchange x, int status, String message) throws IOException {
        reply(x, status, Map.of("error", message));
    }

    private static void reply(HttpExchange x, int status, Object body) throws IOException {
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
}
