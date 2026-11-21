package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.AgentEvents;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.Telemetry;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Traces;
import com.agentpulse.notification.domain.Event;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * Agent steps and traces, matching the Go service's {@code agent_handlers.go}:
 * <ul>
 *   <li>{@code POST /api/events}: one step; 202 when accepted, 200 {@code {"status":"duplicate"}} for a repeated id.</li>
 *   <li>{@code GET /api/events}: the event list ({@code ?traceId=} narrows it to one run).</li>
 *   <li>{@code GET /api/traces}: recent runs; {@code GET /api/traces/{id}}: one run with its ordered steps.</li>
 * </ul>
 * A step goes through exactly the same pipeline as a webhook: the same rules, deduplication, overrides and delivery.
 */
final class AgentApi {
    static final String EVENTS_PATH = "/api/events";
    static final String TRACES_PREFIX = "/api/traces";
    static final int DEFAULT_TRACES = 30;
    static final int MAX_TRACES = 100;
    private static final int LIST_WINDOW = 1000;

    private static final Logger LOG = LoggerFactory.getLogger(AgentApi.class);
    private static final TypeReference<Map<String, Object>> MAP = new TypeReference<>() {};

    private final ApiSupport api;
    private final Pipeline pipeline;
    private final EventRepository events;
    private final HttpHandler listEvents;

    /** {@code listEvents} answers {@code GET /api/events}; it is the same handler as {@code GET /events}, as in Go. */
    AgentApi(ApiSupport api, Pipeline pipeline, EventRepository events, HttpHandler listEvents) {
        this.api = api;
        this.pipeline = pipeline;
        this.events = events;
        this.listEvents = listEvents;
    }

    /** {@code span} is the request's span context; a published step becomes its child. */
    void events(HttpExchange x, Telemetry.SpanContext span) throws IOException {
        switch (x.getRequestMethod()) {
            case "GET" -> listEvents.handle(x);
            case "POST" -> publish(x, span);
            default -> {
                if (api.authorised(x)) {
                    api.error(x, 405, "method not allowed");
                }
            }
        }
    }

    private void publish(HttpExchange x, Telemetry.SpanContext span) throws IOException {
        if (!api.authorised(x)) {
            return;
        }
        Optional<JsonNode> body = api.readJson(x);
        if (body.isEmpty()) {
            return;
        }
        AgentEvents.Input in = decode(body.get());
        if (in == null) {
            api.error(x, 400, "invalid request payload");
            return;
        }
        Event normalized;
        try {
            normalized = AgentEvents.normalize(in);
        } catch (AgentEvents.InvalidException e) {
            api.error(x, 400, e.getMessage());
            return;
        }
        Event result;
        try {
            result = pipeline.handle(normalized, span);
        } catch (Pipeline.DuplicateDeliveryException e) {
            api.reply(x, 200, Map.of("status", "duplicate", "eventId", normalized.getId()));
            return;
        } catch (RuntimeException e) {
            LOG.error("pipeline failed event_id={} event_type={} error={}", normalized.getId(), normalized.getEventType(), e.getMessage());
            api.error(x, 500, "internal error");
            return;
        }
        LOG.info("agent event processed event_id={} trace_id={} attention_class={} decision_status={}",
                result.getId(), result.getTraceId(), result.getAttentionClass(), result.getDecisionState().getStatus());
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("status", "accepted");
        out.put("eventId", result.getId());
        out.put("traceId", result.getTraceId()); // always present, empty outside a run, as in Go
        out.put("attentionClass", result.getAttentionClass());
        out.put("decisionState", result.getDecisionState());
        api.reply(x, 202, out);
    }

    void traces(HttpExchange x) throws IOException {
        if (!api.authorised(x)) {
            return;
        }
        if (!"GET".equals(x.getRequestMethod())) {
            api.error(x, 405, "method not allowed");
            return;
        }
        List<Event> oldestFirst;
        try {
            oldestFirst = new ArrayList<>(events.listRecent(LIST_WINDOW));
        } catch (RuntimeException e) {
            LOG.error("list events for traces failed error={}", e.getMessage());
            api.error(x, 500, "internal error");
            return;
        }
        Collections.reverse(oldestFirst); // the repository lists newest first; traces read oldest first, as Go's List
        String path = x.getRequestURI().getPath();
        String rest = trimSlashes(path.substring(TRACES_PREFIX.length()));
        if (!rest.isEmpty()) {
            if (rest.contains("/")) {
                api.error(x, 404, "not found");
                return;
            }
            Optional<Traces.Trace> trace = Traces.find(oldestFirst, rest);
            if (trace.isEmpty()) {
                api.error(x, 404, "trace not found");
                return;
            }
            api.reply(x, 200, trace.get().toJson());
            return;
        }
        int limit = DEFAULT_TRACES;
        Map<String, String> query = Query.parse(x.getRequestURI().getRawQuery());
        String raw = query.getOrDefault("limit", "");
        if (!raw.isEmpty()) {
            try {
                limit = Integer.parseInt(raw);
            } catch (NumberFormatException e) {
                limit = 0;
            }
            if (limit < 1 || limit > MAX_TRACES) {
                api.error(x, 400, "limit must be between 1 and " + MAX_TRACES);
                return;
            }
        }
        List<Map<String, Object>> summaries = Traces.group(oldestFirst).stream().limit(limit)
                .map(t -> t.summary().toJson()).toList();
        api.reply(x, 200, Map.of("traces", summaries));
    }

    /**
     * Fills an input the way Go's encoding/json fills its struct: keys match field names without regard to case and
     * the last match wins, a JSON null leaves a text field unset, and a value of the wrong type makes the whole body
     * invalid. Returns null for a body that is not an object (or null).
     */
    static AgentEvents.Input decode(JsonNode body) {
        if (body.isNull()) {
            return new AgentEvents.Input(null, null, null, null, null, null, null, null, null, null);
        }
        if (!body.isObject()) {
            return null;
        }
        Map<String, String> text = new HashMap<>();
        Map<String, Map<String, Object>> maps = new HashMap<>();
        for (Iterator<Map.Entry<String, JsonNode>> it = body.fields(); it.hasNext(); ) {
            Map.Entry<String, JsonNode> f = it.next();
            String field = field(f.getKey());
            if (field == null) {
                continue; // unknown keys are ignored, as in Go
            }
            JsonNode v = f.getValue();
            if (field.equals("metadata") || field.equals("payload")) {
                if (v.isNull()) {
                    maps.put(field, null);
                } else if (v.isObject()) {
                    maps.put(field, ApiSupport.JSON.convertValue(v, MAP));
                } else {
                    return null;
                }
            } else if (v.isTextual()) {
                text.put(field, v.textValue());
            } else if (!v.isNull()) {
                return null;
            }
        }
        return new AgentEvents.Input(text.get("id"), text.get("source"), text.get("eventType"), text.get("repository"),
                text.get("traceId"), text.get("parentEventId"), text.get("agentName"), text.get("severity"),
                maps.get("metadata"), maps.get("payload"));
    }

    private static final List<String> FIELDS = List.of("id", "source", "eventType", "repository", "traceId",
            "parentEventId", "agentName", "severity", "metadata", "payload");

    private static String field(String key) {
        for (String f : FIELDS) {
            if (f.equals(key)) {
                return f;
            }
        }
        for (String f : FIELDS) {
            if (f.equalsIgnoreCase(key)) {
                return f;
            }
        }
        return null;
    }

    private static String trimSlashes(String s) {
        int start = 0;
        int end = s.length();
        while (start < end && s.charAt(start) == '/') {
            start++;
        }
        while (end > start && s.charAt(end - 1) == '/') {
            end--;
        }
        return s.substring(start, end);
    }
}
