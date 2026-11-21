package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.AgentEvents;
import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.domain.Event;
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
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** What the shared contract file 16 cannot reach: Go-style body decoding, server errors and tie order over HTTP. */
class AgentApiTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Map<String, String> KEY = Map.of("X-API-Key", "k");
    private final HttpClient client = HttpClient.newHttpClient();
    private HttpServer server;

    @AfterEach
    void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private static final NotificationProvider OK = new NotificationProvider() {
        @Override public void send(Notification n) { }
        @Override public String name() { return "ok"; }
    };

    private String start(EventRepository repo) throws Exception {
        Config config = Config.load(Map.of("API_KEY", "k"));
        PolicyEngine engine = new PolicyEngine(List.of());
        RulesService rules = new RulesService(new MemoryStores.Rules(), engine, List.of());
        Pipeline pipeline = new Pipeline(engine, OK, repo);
        server = new HttpApi(config, pipeline, repo, new Notify.Service(config.notifyLimits(), OK), rules,
                new MemoryStores.Deliveries(), Clock.systemUTC()).start(0);
        return "http://localhost:" + server.getAddress().getPort();
    }

    private HttpResponse<String> send(String method, String url, String body, Map<String, String> headers) throws Exception {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(url))
                .method(method, HttpRequest.BodyPublishers.ofString(body == null ? "" : body));
        headers.forEach(b::header);
        return client.send(b.build(), HttpResponse.BodyHandlers.ofString());
    }

    private static AgentEvents.Input decode(String json) throws Exception {
        return AgentApi.decode(JSON.readTree(json));
    }

    @Test
    void keysMatchWithoutCaseAndTheLastOneWinsAsInGo() throws Exception {
        AgentEvents.Input in = decode("{\"EVENTTYPE\":\"first\",\"eventType\":\"second\",\"TraceId\":\"run\",\"unknown\":1,"
                + "\"Metadata\":{\"attempt\":2},\"payload\":{\"x\":true}}");
        assertEquals("second", in.eventType());
        assertEquals("run", in.traceId());
        assertEquals(Map.of("attempt", 2), in.metadata());
        assertEquals(Map.of("x", true), in.payload());
    }

    @Test
    void nullLeavesTextUnsetAndClearsAMap() throws Exception {
        AgentEvents.Input in = decode("{\"eventType\":\"kept\",\"eventtype\":null,\"metadata\":{\"a\":1},\"metadata\":null}");
        assertEquals("kept", in.eventType());
        assertEquals(Map.of(), in.metadata());
        AgentEvents.Input empty = decode("null");
        assertEquals("", empty.eventType(), "a JSON null body is an empty step that then fails validation");
    }

    @Test
    void aValueOfTheWrongTypeMakesTheBodyInvalid() throws Exception {
        assertNull(decode("{\"eventType\":5}"));
        assertNull(decode("{\"metadata\":[1]}"));
        assertNull(decode("{\"payload\":\"text\"}"));
        assertNull(decode("[]"));
        assertNull(decode("\"text\""));
    }

    @Test
    void theHttpAnswersForBadBodies() throws Exception {
        String base = start(new InMemoryEventRepository(Clock.systemUTC()));
        HttpResponse<String> wrongType = send("POST", base + "/api/events", "{\"eventType\":5}", KEY);
        assertEquals(400, wrongType.statusCode());
        assertEquals("invalid request payload", JSON.readTree(wrongType.body()).get("error").asText());
        HttpResponse<String> nullBody = send("POST", base + "/api/events", "null", KEY);
        assertEquals(400, nullBody.statusCode());
        assertEquals("invalid agent event: eventType is required, such as tool.call.failed", JSON.readTree(nullBody.body()).get("error").asText());
        assertEquals(401, send("PUT", base + "/api/events", "", Map.of()).statusCode(), "the key is checked before the method");
    }

    @Test
    void stepsStoredAtTheSameInstantKeepTheirOrderAndTracesTieByTraceId() throws Exception {
        Clock frozen = Clock.fixed(Instant.parse("2026-11-21T00:00:00Z"), ZoneOffset.UTC);
        String base = start(new InMemoryEventRepository(frozen));
        for (String[] s : new String[][] {{"zeta", "z-2", "agent.plan"}, {"zeta", "z-1", "agent.complete"}, {"alpha", "a-1", "agent.plan"}}) {
            String body = "{\"traceId\":\"" + s[0] + "\",\"id\":\"" + s[1] + "\",\"eventType\":\"" + s[2] + "\",\"metadata\":{\"step_kind\":\""
                    + (s[2].endsWith("complete") ? "complete" : "plan") + "\"}}";
            assertEquals(202, send("POST", base + "/api/events", body, KEY).statusCode());
        }
        JsonNode list = JSON.readTree(send("GET", base + "/api/traces", null, KEY).body()).get("traces");
        assertEquals("alpha", list.get(0).get("traceId").asText(), "traces that ended together are ordered by trace id");
        assertEquals("zeta", list.get(1).get("traceId").asText());
        JsonNode zeta = JSON.readTree(send("GET", base + "/api/traces/zeta/", null, KEY).body());
        assertEquals("z-2", zeta.get("stepList").get(0).get("eventId").asText(), "steps at one instant keep the order they were stored in");
        assertEquals("z-1", zeta.get("stepList").get(1).get("eventId").asText());
        assertEquals("complete", zeta.get("status").asText());
    }

    @Test
    void theOldEventListAlsoNarrowsToARunAndShowsTheChain() throws Exception {
        String base = start(new InMemoryEventRepository(Clock.systemUTC()));
        send("POST", base + "/api/events", "{\"traceId\":\"r\",\"id\":\"p\",\"eventType\":\"agent.plan\",\"agentName\":\"bot\"}", KEY);
        send("POST", base + "/api/events", "{\"traceId\":\"r\",\"id\":\"c\",\"parentEventId\":\"p\",\"eventType\":\"agent.complete\"}", KEY);
        send("POST", base + "/api/events", "{\"eventType\":\"agent.heartbeat\"}", KEY);
        JsonNode all = JSON.readTree(send("GET", base + "/events", null, KEY).body()).get("events");
        assertEquals(3, all.size());
        assertFalse(all.get(0).has("traceId"), "an event outside a run has no chain fields");
        JsonNode run = JSON.readTree(send("GET", base + "/events?traceId=r", null, KEY).body()).get("events");
        assertEquals(2, run.size());
        assertEquals("p", run.get(0).get("parentEventId").asText());
        assertEquals("bot", run.get(1).get("agentName").asText());
        assertEquals(3, JSON.readTree(send("GET", base + "/events?traceId=", null, KEY).body()).get("events").size(), "an empty traceId does not filter");
    }

    @Test
    void storageFailuresAreInternalErrors() throws Exception {
        List<String> saved = new ArrayList<>();
        EventRepository broken = new EventRepository() {
            @Override public void save(Event event) { saved.add(event.getId()); throw new PortException("db down", null); }
            @Override public boolean exists(String id) { return false; }
            @Override public List<Event> listRecent(int limit) { throw new PortException("db down", null); }
        };
        String base = start(broken);
        HttpResponse<String> post = send("POST", base + "/api/events", "{\"eventType\":\"agent.plan\"}", KEY);
        assertEquals(500, post.statusCode());
        assertEquals("internal error", JSON.readTree(post.body()).get("error").asText());
        assertEquals(1, saved.size());
        assertEquals(500, send("GET", base + "/api/traces", null, KEY).statusCode());
        assertEquals(500, send("GET", base + "/api/traces/x", null, KEY).statusCode());
        assertTrue(send("GET", base + "/api/tracesx", null, KEY).statusCode() == 404, "only the traces prefix itself is routed");
    }
}
