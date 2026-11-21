package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.application.Telemetry;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import com.agentpulse.notification.infrastructure.MemoryStores;
import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Clock;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Request spans over real HTTP: names, attributes, status, the untraced /health, and the request-to-pipeline link. */
class HttpTracingTest {
    private static final String TRACE = "0af7651916cd43dd8448eb211c80319c";
    private static final String SPAN = "b7ad6b7169203331";
    private final HttpClient client = HttpClient.newHttpClient();
    private final List<Telemetry.SpanData> spans = new ArrayList<>();
    private HttpServer server;

    @AfterEach
    void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private String start(EventRepository repo) throws Exception {
        Telemetry.Tracer tracer = new Telemetry.Tracer(Clock.systemUTC(), s -> {
            synchronized (spans) {
                spans.add(s);
            }
        });
        Config config = Config.load(Map.of("API_KEY", "k"));
        NotificationProvider ok = new NotificationProvider() {
            @Override public void send(Notification n) { }
            @Override public String name() { return "ok"; }
        };
        PolicyEngine engine = new PolicyEngine(List.of());
        Pipeline pipeline = new Pipeline(engine, ok, repo).withTracer(tracer);
        server = new HttpApi(config, pipeline, repo, new Notify.Service(config.notifyLimits(), ok),
                new RulesService(new MemoryStores.Rules(), engine, List.of()), new MemoryStores.Deliveries(), Clock.systemUTC())
                .withTracer(tracer).start(0);
        return "http://localhost:" + server.getAddress().getPort();
    }

    private HttpResponse<String> send(String method, String url, String body, boolean traced) throws Exception {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(url)).header("X-API-Key", "k")
                .method(method, HttpRequest.BodyPublishers.ofString(body == null ? "" : body));
        if (traced) {
            b.header("traceparent", "00-" + TRACE + "-" + SPAN + "-01");
        }
        return client.send(b.build(), HttpResponse.BodyHandlers.ofString());
    }

    private Telemetry.SpanData named(String name) {
        synchronized (spans) {
            return spans.stream().filter(s -> s.name().equals(name)).findFirst().orElseThrow(() -> new AssertionError(name + " not in " + spans));
        }
    }

    private static Map<String, String> attrs(Telemetry.SpanData s) {
        Map<String, String> m = new HashMap<>();
        s.attributes().forEach(a -> m.put(a.key(), a.value()));
        return m;
    }

    @Test
    void aRequestIsAServerSpanNamedByItsRouteAndThePipelineIsItsChild() throws Exception {
        String base = start(new InMemoryEventRepository(Clock.systemUTC()));
        HttpResponse<String> r = send("POST", base + "/api/events", "{\"id\":\"s1\",\"eventType\":\"agent.plan\"}", true);
        assertEquals(202, r.statusCode());
        Telemetry.SpanData request = named("POST /api/events");
        assertEquals(Telemetry.Kind.SERVER, request.kind());
        assertEquals(TRACE, request.traceId());
        assertEquals(SPAN, request.parentSpanId(), "the request span continues the caller's span");
        assertEquals(Map.of(Telemetry.ATTR_HTTP_METHOD, "POST", Telemetry.ATTR_HTTP_ROUTE, "/api/events", Telemetry.ATTR_HTTP_STATUS_CODE, "202"),
                attrs(request));
        assertEquals("00-" + TRACE + "-" + request.spanId() + "-01", r.headers().firstValue("traceparent").orElse(""),
                "the response names the request's own span");
        Telemetry.SpanData pipeline = named("pipeline.Handle");
        assertEquals(request.spanId(), pipeline.parentSpanId(), "the pipeline span is the request span's child, passed explicitly");
        assertEquals(TRACE, pipeline.traceId());
    }

    @Test
    void mcpPublishingJoinsTheRequestsTraceAndIdsNeverBecomeSpanNames() throws Exception {
        String base = start(new InMemoryEventRepository(Clock.systemUTC()));
        send("POST", base + "/api/mcp", "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"publish_event\","
                + "\"arguments\":{\"eventType\":\"agent.plan\"}}}", true);
        assertEquals(named("POST /api/mcp").spanId(), named("pipeline.Handle").parentSpanId());
        send("GET", base + "/api/traces/run-123", null, false);
        send("GET", base + "/no/such/thing", null, false);
        named("GET /api/traces");
        named("GET other");
    }

    @Test
    void healthIsNotTracedAndAServerErrorFailsTheSpan() throws Exception {
        EventRepository broken = new EventRepository() {
            @Override public void save(Event event) { throw new PortException("db down", null); }
            @Override public boolean exists(String id) { return false; }
            @Override public List<Event> listRecent(int limit) { throw new PortException("db down", null); }
        };
        String base = start(broken);
        HttpResponse<String> health = send("GET", base + "/health", null, true);
        assertEquals(200, health.statusCode());
        assertFalse(health.headers().firstValue("traceparent").isPresent());
        synchronized (spans) {
            assertTrue(spans.isEmpty(), "no span for /health: " + spans);
        }
        assertEquals(500, send("GET", base + "/api/traces", null, false).statusCode());
        assertTrue(named("GET /api/traces").failed());
        assertEquals("500", attrs(named("GET /api/traces")).get(Telemetry.ATTR_HTTP_STATUS_CODE));
        assertEquals(500, send("POST", base + "/api/events", "{\"eventType\":\"agent.plan\"}", false).statusCode());
        assertTrue(named("pipeline.Handle").failed());
        synchronized (spans) {
            assertFalse(spans.toString().contains("db down"), "error text never reaches the exporter");
        }
    }

    @Test
    void routeLabelsMatchGo() {
        assertEquals("/api/notifications", Routes.label("/api/notifications/42/state"));
        assertEquals("/api/rules", Routes.label("/api/rules/7"));
        assertEquals("/events", Routes.label("/events"));
        assertEquals("other", Routes.label("/api/unknown/x"));
        assertEquals("other", Routes.label("/x"));
        assertEquals(null, Routes.traced("/health"));
        assertEquals("/health", Routes.label("/health"));
    }
}
