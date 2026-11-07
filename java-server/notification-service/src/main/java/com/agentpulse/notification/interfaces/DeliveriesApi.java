package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Delivery;
import com.agentpulse.notification.application.Ports.DeliveryStore;
import com.sun.net.httpserver.HttpExchange;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.time.Clock;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * {@code GET /api/deliveries?limit=50} lists the queue newest first with counts by status; {@code POST
 * /api/deliveries/{id}/retry} moves a dead delivery back to pending. Targets and message text are never shown.
 */
final class DeliveriesApi {
    static final String PREFIX = "/api/deliveries";
    private static final Logger LOG = LoggerFactory.getLogger(DeliveriesApi.class);
    private static final int DEFAULT_LIMIT = 50;
    private static final int MAX_LIMIT = 200;

    private final ApiSupport api;
    private final DeliveryStore store;
    private final Clock clock;

    DeliveriesApi(ApiSupport api, DeliveryStore store, Clock clock) {
        this.api = api;
        this.store = store;
        this.clock = clock;
    }

    void handle(HttpExchange x) throws IOException {
        // Anyone signed in may read the queue; a manual retry needs an admin.
        boolean retry = "POST".equals(x.getRequestMethod());
        if (!(retry ? api.authorisedAdmin(x) : api.authorised(x))) {
            return;
        }
        String rest = x.getRequestURI().getPath().substring(PREFIX.length()).replaceAll("^/+|/+$", "");
        boolean get = "GET".equals(x.getRequestMethod());
        if (rest.isEmpty()) {
            if (get) {
                list(x);
            } else {
                api.error(x, 405, "method not allowed");
            }
            return;
        }
        int slash = rest.indexOf('/');
        String id = slash < 0 ? rest : rest.substring(0, slash);
        String action = slash < 0 ? "" : rest.substring(slash + 1);
        if (!action.equals("retry")) {
            api.error(x, 404, "not found");
        } else if (!"POST".equals(x.getRequestMethod())) {
            api.error(x, 405, "method not allowed");
        } else {
            retry(x, id);
        }
    }

    private void list(HttpExchange x) throws IOException {
        int limit = DEFAULT_LIMIT;
        String raw = Query.parse(x.getRequestURI().getRawQuery()).get("limit");
        if (raw != null) {
            try {
                limit = Integer.parseInt(raw);
            } catch (NumberFormatException e) {
                limit = 0;
            }
            if (limit < 1 || limit > MAX_LIMIT) {
                api.error(x, 400, "limit must be between 1 and " + MAX_LIMIT);
                return;
            }
        }
        try {
            List<Map<String, Object>> jobs = new ArrayList<>();
            for (Delivery.Job j : store.list(limit)) {
                jobs.add(view(j));
            }
            Map<String, Object> out = new LinkedHashMap<>();
            out.put("deliveries", jobs);
            out.put("counts", store.counts());
            api.reply(x, 200, out);
        } catch (RuntimeException e) {
            LOG.error("list deliveries failed error={}", Delivery.redact(e.getMessage()));
            api.error(x, 500, "internal error");
        }
    }

    private void retry(HttpExchange x, String id) throws IOException {
        try {
            store.requeue(id, clock.instant());
            api.reply(x, 200, Map.of("status", Delivery.Status.PENDING));
        } catch (Delivery.NotFoundException e) {
            api.error(x, 404, e.getMessage());
        } catch (Delivery.NotRetryableException e) {
            api.error(x, 409, e.getMessage());
        } catch (RuntimeException e) {
            LOG.error("retry delivery failed error={}", Delivery.redact(e.getMessage()));
            api.error(x, 500, "internal error");
        }
    }

    private static Map<String, Object> view(Delivery.Job j) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", j.id());
        m.put("eventId", j.eventId());
        m.put("channel", j.channel());
        m.put("class", j.attentionClass());
        m.put("title", j.title());
        if (!j.group().isEmpty()) {
            m.put("group", j.group());
        }
        m.put("status", j.status());
        m.put("attempts", j.attempts());
        m.put("nextAttemptAt", DateTimeFormatter.ISO_INSTANT.format(j.nextAttemptAt()));
        if (!j.lastError().isEmpty()) {
            m.put("lastError", j.lastError());
        }
        if (j.direct()) {
            m.put("direct", true);
        }
        m.put("createdAt", DateTimeFormatter.ISO_INSTANT.format(j.createdAt()));
        m.put("updatedAt", DateTimeFormatter.ISO_INSTANT.format(j.updatedAt()));
        return m;
    }
}
