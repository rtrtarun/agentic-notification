package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Digests;
import com.agentpulse.notification.application.Heuristic;
import com.agentpulse.notification.domain.Event;
import com.sun.net.httpserver.HttpExchange;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.function.Function;

/**
 * Attention Intelligence over HTTP: {@code /api/digests}, {@code /api/explain/{eventId}} and
 * {@code /api/attention/status}. It decodes, calls the digest generator, store or heuristic and encodes the answer.
 * Same paths, statuses and bodies as the Go service.
 */
final class AttentionApi {
    static final String DIGESTS_PREFIX = "/api/digests";
    static final String EXPLAIN_PREFIX = "/api/explain";
    static final String STATUS_PATH = "/api/attention/status";
    private static final Logger LOG = LoggerFactory.getLogger(AttentionApi.class);
    private static final int DEFAULT_LIMIT = 30;
    private static final int MAX_LIMIT = 100;

    private final ApiSupport api;
    private final Digests.Generator generator;
    private final Digests.Store store;
    private final Heuristic heuristic;
    private final Function<String, Optional<Event>> lookup;
    private final Duration every;
    private final Accounts.Service accounts;

    AttentionApi(ApiSupport api, Digests.Generator generator, Digests.Store store, Heuristic heuristic,
                 Function<String, Optional<Event>> lookup, Duration every, Accounts.Service accounts) {
        this.api = api;
        this.generator = generator;
        this.store = store;
        this.heuristic = heuristic;
        this.lookup = lookup;
        this.every = every;
        this.accounts = accounts;
    }

    // ---- /api/attention/status -----------------------------------------------------------------------------

    void status(HttpExchange x) throws IOException {
        ApiSupport.Principal who = api.authenticate(x);
        if (who == null) {
            return;
        }
        if (!"GET".equals(x.getRequestMethod())) {
            api.error(x, 405, "method not allowed");
            return;
        }
        if (!who.admin()) {
            api.error(x, 403, "admin access required");
            return;
        }
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("provider", "heuristic");
        out.put("model", Heuristic.MODEL);
        out.put("breaker", "closed");
        out.put("calls", 0);
        out.put("fallbacks", 0);
        api.reply(x, 200, out);
    }

    // ---- /api/digests --------------------------------------------------------------------------------------

    void digests(HttpExchange x) throws IOException {
        ApiSupport.Principal who = api.authenticate(x);
        if (who == null) {
            return;
        }
        if (generator == null || store == null) {
            api.error(x, 503, "digests are not available");
            return;
        }
        String rest = x.getRequestURI().getPath().substring(DIGESTS_PREFIX.length()).replaceAll("^/+|/+$", "");
        Map<String, String> query = Query.parse(x.getRequestURI().getRawQuery());
        String method = x.getRequestMethod();
        if (rest.isEmpty() && "GET".equals(method)) {
            list(x, query);
        } else if (rest.equals("generate") && "POST".equals(method)) {
            generate(x, who, query);
        } else if (!rest.isEmpty() && !rest.equals("generate") && !rest.contains("/") && "GET".equals(method)) {
            get(x, rest);
        } else {
            api.error(x, 405, "method not allowed");
        }
    }

    private void list(HttpExchange x, Map<String, String> query) throws IOException {
        int limit = DEFAULT_LIMIT;
        if (query.containsKey("limit")) {
            try {
                limit = Integer.parseInt(query.get("limit"));
            } catch (NumberFormatException e) {
                limit = 0;
            }
            if (limit < 1 || limit > MAX_LIMIT) {
                api.error(x, 400, "limit must be between 1 and " + MAX_LIMIT);
                return;
            }
        }
        if (personal(query)) {
            api.error(x, 400, "personal digests are not available in this service yet");
            return;
        }
        List<Digests.Digest> found;
        try {
            found = store.list(limit);
        } catch (RuntimeException e) {
            fail(x, e);
            return;
        }
        List<Map<String, Object>> out = new ArrayList<>();
        found.forEach(d -> out.add(Digests.toMap(d)));
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("digests", out);
        body.put("every", goDuration(every));
        api.reply(x, 200, body);
    }

    private void generate(HttpExchange x, ApiSupport.Principal who, Map<String, String> query) throws IOException {
        if (personal(query)) {
            api.error(x, 400, "personal digests are not available in this service yet");
            return;
        }
        if (!who.admin()) {
            api.error(x, 403, "admin access required");
            return;
        }
        Digests.Digest d;
        try {
            d = generator.generate(who.actor());
        } catch (RuntimeException e) {
            fail(x, e);
            return;
        }
        Map<String, Object> body = new LinkedHashMap<>();
        if (d == null) {
            body.put("generated", false);
            body.put("digest", null);
            api.reply(x, 200, body);
            return;
        }
        if (accounts != null) {
            InetSocketAddress a = x.getRemoteAddress();
            String ip = a == null || a.getAddress() == null ? "unknown" : a.getAddress().getHostAddress();
            accounts.auditEvent(who.actor(), "digest.generated", d.id(), ip, "");
        }
        body.put("generated", true);
        body.put("digest", Digests.toMap(d));
        api.reply(x, 201, body);
    }

    private void get(HttpExchange x, String id) throws IOException {
        try {
            api.reply(x, 200, Digests.toMap(store.get(id)));
        } catch (Digests.NotFoundException e) {
            api.error(x, 404, e.getMessage());
        } catch (RuntimeException e) {
            fail(x, e);
        }
    }

    private static boolean personal(Map<String, String> query) {
        return "me".equals(query.get("scope"));
    }

    // ---- /api/explain/{eventId} ----------------------------------------------------------------------------

    void explain(HttpExchange x) throws IOException {
        ApiSupport.Principal who = api.authenticate(x);
        if (who == null) {
            return;
        }
        if (!"GET".equals(x.getRequestMethod())) {
            api.error(x, 405, "method not allowed");
            return;
        }
        String id = x.getRequestURI().getPath().substring(EXPLAIN_PREFIX.length()).replaceAll("^/+", "");
        if (id.isEmpty() || id.contains("/")) {
            api.error(x, 404, "not found");
            return;
        }
        Optional<Event> event;
        try {
            event = lookup.apply(id);
        } catch (RuntimeException e) {
            fail(x, e);
            return;
        }
        if (event.isEmpty()) {
            api.error(x, 404, "event not found");
            return;
        }
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("eventId", event.get().getId());
        out.put("explanation", heuristic.explain(event.get()));
        out.put("model", Heuristic.MODEL);
        out.put("source", "heuristic");
        api.reply(x, 200, out);
    }

    // ---- helpers -------------------------------------------------------------------------------------------

    private void fail(HttpExchange x, RuntimeException e) throws IOException {
        LOG.error("attention request failed error={}", e.getMessage());
        api.error(x, 500, "internal error");
    }

    /** A duration the way Go's {@code Duration.String} writes it: 0s, 30s, 5m0s, 1h0m0s. */
    static String goDuration(Duration d) {
        long total = d.getSeconds();
        if (total == 0) {
            return "0s";
        }
        long h = total / 3600;
        long m = (total % 3600) / 60;
        long s = total % 60;
        if (h > 0) {
            return h + "h" + m + "m" + s + "s";
        }
        if (m > 0) {
            return m + "m" + s + "s";
        }
        return s + "s";
    }
}
