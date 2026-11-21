package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Predicate;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import com.agentpulse.notification.infrastructure.MemoryStores;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Clock;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** {@code POST /api/mcp} over real HTTP with the in-process backend: failures, limits and the critical path. */
class McpApiTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Map<String, String> KEY = Map.of("X-API-Key", "k");
    private final HttpClient client = HttpClient.newHttpClient();
    private final List<Notification> sent = new ArrayList<>();
    private HttpServer server;

    @AfterEach
    void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private NotificationProvider provider(boolean fail) {
        return new NotificationProvider() {
            @Override public void send(Notification n) {
                if (fail) {
                    throw new PortException("smtp down", null);
                }
                sent.add(n);
            }
            @Override public String name() { return "test"; }
        };
    }

    private String start(EventRepository repo, List<Rule> rules, NotificationProvider provider, Map<String, String> env) throws Exception {
        Config config = Config.load(env);
        PolicyEngine engine = new PolicyEngine(rules);
        Pipeline pipeline = new Pipeline(engine, provider, repo);
        server = new HttpApi(config, pipeline, repo, new Notify.Service(config.notifyLimits(), provider),
                new RulesService(new MemoryStores.Rules(), engine, rules), new MemoryStores.Deliveries(), Clock.systemUTC()).start(0);
        return "http://localhost:" + server.getAddress().getPort() + McpApi.PATH;
    }

    private String start() throws Exception {
        return start(new InMemoryEventRepository(Clock.systemUTC()), List.of(), provider(false), Map.of("API_KEY", "k"));
    }

    private HttpResponse<String> post(String url, String body) throws Exception {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(url)).POST(HttpRequest.BodyPublishers.ofString(body));
        KEY.forEach(b::header);
        return client.send(b.build(), HttpResponse.BodyHandlers.ofString());
    }

    private static String call(String tool, String args) {
        return "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"" + tool + "\",\"arguments\":" + args + "}}";
    }

    private JsonNode result(String url, String tool, String args) throws Exception {
        HttpResponse<String> r = post(url, call(tool, args));
        assertEquals(200, r.statusCode(), r.body());
        assertEquals("application/json", r.headers().firstValue("Content-Type").orElse(""));
        return JSON.readTree(r.body()).get("result");
    }

    @Test
    void anAgentCannotMakeItsStepCriticalThroughMcpButARuleCanAndItIsSentInline() throws Exception {
        Rule rule = new Rule("r", "agent critical", "", new Rule.Condition("agent", "deploy.failed", "", List.of(new Predicate("severity", "eq", "critical"))),
                new Rule.Outcome("critical", "the agent reported a critical step", false), false, false);
        String url = start(new InMemoryEventRepository(Clock.systemUTC()), List.of(rule), provider(false), Map.of("API_KEY", "k"));
        JsonNode said = JSON.readTree(result(url, "publish_event", "{\"eventType\":\"tool.failed\",\"severity\":\"critical\"}").at("/content/0/text").asText());
        assertEquals("standard", said.get("attentionClass").asText());
        assertTrue(sent.isEmpty());
        JsonNode ruled = JSON.readTree(result(url, "publish_event", "{\"id\":\"c1\",\"eventType\":\"deploy.failed\",\"severity\":\"critical\"}").at("/content/0/text").asText());
        assertEquals("critical", ruled.get("attentionClass").asText());
        assertEquals("sent", ruled.at("/decisionState/status").asText());
        assertEquals(1, sent.size(), "delivered inline, before the answer");
    }

    @Test
    void storageAndProviderFailuresAreSafeToolErrors() throws Exception {
        EventRepository broken = new EventRepository() {
            @Override public void save(Event event) { throw new PortException("db down", null); }
            @Override public boolean exists(String id) { return false; }
            @Override public List<Event> listRecent(int limit) { throw new PortException("db down", null); }
        };
        String url = start(broken, List.of(), provider(true), Map.of("API_KEY", "k"));
        JsonNode publish = result(url, "publish_event", "{\"eventType\":\"agent.plan\"}");
        assertTrue(publish.get("isError").asBoolean());
        assertEquals("the event could not be processed", publish.at("/content/0/text").asText());
        assertEquals("the events could not be read", result(url, "get_recent_events", "{}").at("/content/0/text").asText());
        assertEquals("invalid request: event not found", result(url, "explain_decision", "{\"eventId\":\"e\"}").at("/content/0/text").asText(),
                "a failed lookup reads as not found, as in Go");
        assertEquals("the notification could not be sent",
                result(url, "notify_user", "{\"title\":\"t\",\"message\":\"m\"}").at("/content/0/text").asText());
    }

    @Test
    void anExplainerFailureIsASafeToolError() {
        InMemoryEventRepository repo = new InMemoryEventRepository(Clock.systemUTC());
        repo.save(new Event("e1", "agent", "agent.plan", "", null));
        McpApi.InProcess backend = new McpApi.InProcess(null, repo, null, e -> { throw new IllegalStateException("boom"); }, HttpApi::summary);
        com.agentpulse.notification.application.Mcp.ToolException e = org.junit.jupiter.api.Assertions.assertThrows(
                com.agentpulse.notification.application.Mcp.ToolException.class, () -> backend.explain("e1"));
        assertEquals("the explanation could not be made", e.getMessage());
    }

    @Test
    void theHttpRules() throws Exception {
        String url = start();
        assertEquals(401, client.send(HttpRequest.newBuilder(URI.create(url)).POST(HttpRequest.BodyPublishers.ofString("{}")).build(),
                HttpResponse.BodyHandlers.ofString()).statusCode());
        HttpResponse<String> note = post(url, "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}");
        assertEquals(202, note.statusCode());
        assertTrue(note.body().isEmpty());
        assertFalse(note.headers().firstValue("Content-Type").isPresent(), "no body, no content type");
        HttpResponse<String> tooBig = post(url, "{\"x\":\"" + "y".repeat((int) Config.DEFAULT_MAX_BODY_BYTES) + "\"}");
        assertEquals(413, tooBig.statusCode());
        assertEquals("request body too large", JSON.readTree(tooBig.body()).get("error").asText());
    }

    @Test
    void recentEventsAreNewestFirstAndBounded() throws Exception {
        String url = start();
        for (String id : List.of("a", "b", "c")) {
            post(url, call("publish_event", "{\"id\":\"" + id + "\",\"traceId\":\"r\",\"eventType\":\"agent.plan\"}"));
        }
        post(url, call("publish_event", "{\"id\":\"other\",\"eventType\":\"agent.plan\"}"));
        JsonNode events = JSON.readTree(result(url, "get_recent_events", "{\"traceId\":\"r\",\"limit\":2}").at("/content/0/text").asText()).get("events");
        assertEquals(2, events.size());
        assertEquals("c", events.get(0).get("id").asText());
        assertFalse(events.get(0).has("payload"));
        JsonNode explained = JSON.readTree(result(url, "explain_decision", "{\"eventId\":\"a\"}").at("/content/0/text").asText());
        assertEquals("heuristic-v1", explained.get("model").asText());
        assertFalse(explained.get("explanation").asText().isEmpty());
    }
}
