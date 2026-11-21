package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.AgentEvents;
import com.agentpulse.notification.application.Heuristic;
import com.agentpulse.notification.application.Mcp;
import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.domain.Event;
import com.sun.net.httpserver.HttpExchange;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.io.OutputStream;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.function.Function;

/**
 * {@code POST /api/mcp}: the HTTP transport of the MCP server, one JSON-RPC message per request, matching the Go
 * service's {@code mcp_handlers.go}: 200 with the JSON-RPC answer, or 202 with no body for a notification.
 *
 * <p>The tools run in process through the same pipeline, validating notification service and explainer the REST API
 * uses. Go's stdio {@code cmd/mcp-server} reaches the same behaviour over HTTP, so pointing it at this service gives a
 * stdio MCP server too.
 */
final class McpApi {
    static final String PATH = "/api/mcp";
    /** What the in-process server reports as its version, as Go's in-process server does. */
    static final String VERSION = "service";
    private static final int LIST_WINDOW = 1000;
    private static final Logger LOG = LoggerFactory.getLogger(McpApi.class);

    private final ApiSupport api;
    private final Mcp.Server server;

    McpApi(ApiSupport api, Mcp.Backend backend) {
        this.api = api;
        this.server = new Mcp.Server(backend, VERSION);
    }

    void handle(HttpExchange x) throws IOException {
        if (!api.authorised(x)) {
            return;
        }
        if (!"POST".equals(x.getRequestMethod())) {
            api.error(x, 405, "method not allowed");
            return;
        }
        byte[] body = api.readBody(x);
        if (body == null) {
            return;
        }
        byte[] reply = server.handle(body);
        if (reply == null) {
            x.sendResponseHeaders(202, -1);
            return;
        }
        x.getResponseHeaders().set("Content-Type", "application/json");
        x.sendResponseHeaders(200, reply.length);
        try (OutputStream out = x.getResponseBody()) {
            out.write(reply);
        }
    }

    /** The tools acting inside the service. Every failure becomes a message the client may see; details are logged. */
    static final class InProcess implements Mcp.Backend {
        private final Pipeline pipeline;
        private final EventRepository events;
        private final Notify.Service notify;
        private final Function<Event, String> explainer;
        private final Function<Event, Map<String, Object>> summary;

        InProcess(Pipeline pipeline, EventRepository events, Notify.Service notify, Function<Event, String> explainer,
                  Function<Event, Map<String, Object>> summary) {
            this.pipeline = pipeline;
            this.events = events;
            this.notify = notify;
            this.explainer = explainer;
            this.summary = summary;
        }

        @Override
        public Object publishEvent(AgentEvents.Input in) {
            Event normalized;
            try {
                normalized = AgentEvents.normalize(in);
            } catch (AgentEvents.InvalidException e) {
                throw Mcp.ToolException.invalid(e.getMessage());
            }
            Event result;
            try {
                result = pipeline.handle(normalized);
            } catch (Pipeline.DuplicateDeliveryException e) {
                return Map.of("status", "duplicate", "eventId", normalized.getId());
            } catch (RuntimeException e) {
                LOG.error("pipeline failed event_id={} error={}", normalized.getId(), e.getMessage());
                throw new Mcp.ToolException("the event could not be processed");
            }
            Map<String, Object> out = new LinkedHashMap<>();
            out.put("status", "accepted");
            out.put("eventId", result.getId());
            out.put("traceId", result.getTraceId());
            out.put("attentionClass", result.getAttentionClass());
            out.put("decisionState", result.getDecisionState());
            return out;
        }

        @Override
        public Object recentEvents(String traceId, int limit) {
            List<Event> newestFirst;
            try {
                newestFirst = events.listRecent(LIST_WINDOW);
            } catch (RuntimeException e) {
                LOG.error("list events for mcp failed error={}", e.getMessage());
                throw new Mcp.ToolException("the events could not be read");
            }
            List<Map<String, Object>> out = new ArrayList<>();
            for (Event e : newestFirst) {
                if (out.size() == limit) {
                    break;
                }
                if (traceId.isEmpty() || traceId.equals(e.getTraceId())) {
                    out.add(summary.apply(e));
                }
            }
            return Map.of("events", out);
        }

        @Override
        public Object notify(Mcp.Notification n) {
            String agent = n.agent().isEmpty() ? "mcp" : n.agent();
            try {
                notify.send(new Notify.Request(new Notify.Agent(agent, "", ""), n.title(), n.message(),
                        n.emailRecipients(), n.slackWebhookUrls(), n.teamsWebhookUrls(), null));
            } catch (Notify.InvalidRequestException e) {
                throw Mcp.ToolException.invalid(e.getMessage());
            } catch (RuntimeException e) {
                LOG.error("send notification failed error={}", e.getMessage());
                throw new Mcp.ToolException("the notification could not be sent");
            }
            return Map.of("status", "accepted");
        }

        @Override
        public Object explain(String eventId) {
            Optional<Event> event;
            try {
                event = events.listRecent(LIST_WINDOW).stream().filter(e -> e.getId().equals(eventId)).findFirst();
            } catch (RuntimeException e) {
                event = Optional.empty(); // as in Go, a failed lookup reads as not found
            }
            if (event.isEmpty()) {
                throw Mcp.ToolException.invalid("event not found");
            }
            String text;
            try {
                text = explainer.apply(event.get());
            } catch (RuntimeException e) {
                LOG.error("explain for mcp failed event_id={} error={}", eventId, e.getMessage());
                throw new Mcp.ToolException("the explanation could not be made");
            }
            Map<String, Object> out = new LinkedHashMap<>();
            out.put("eventId", event.get().getId());
            out.put("explanation", text);
            out.put("model", Heuristic.MODEL);
            return out;
        }
    }
}
