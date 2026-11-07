package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.infrastructure.MemoryStores;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Things the shared contract does not cover: the fail-closed switches, size limits, 404 and server errors. */
class HttpApiTest {
    private static final ObjectMapper JSON = new ObjectMapper();
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

    private static final List<Rule> RULES = List.of(
            new Rule("1", "all-critical", "", new Rule.Condition("github", "security_advisory", ""),
                    new Rule.Outcome("critical", "bad", false), false, true),
            new Rule("2", "default", "", new Rule.Condition("github", "", ""),
                    new Rule.Outcome("standard", "default routing", false), false, false));

    private String start(Map<String, String> env, NotificationProvider provider, EventRepository repo) throws Exception {
        Config config = Config.load(env);
        PolicyEngine engine = new PolicyEngine(List.of());
        RulesService rules = new RulesService(new MemoryStores.Rules(), engine, RULES);
        Pipeline pipeline = new Pipeline(engine, provider, repo);
        server = new HttpApi(config, pipeline, repo, new Notify.Service(config.notifyLimits(), provider), rules,
                new MemoryStores.Deliveries(), Clock.systemUTC()).start(0);
        return "http://localhost:" + server.getAddress().getPort();
    }

    private String start(Map<String, String> env) throws Exception {
        return start(env, OK, new InMemoryEventRepository(Clock.systemUTC()));
    }

    private HttpResponse<String> send(String method, String url, String body, Map<String, String> headers) throws Exception {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(url))
                .method(method, HttpRequest.BodyPublishers.ofString(body == null ? "" : body));
        headers.forEach(b::header);
        return client.send(b.build(), HttpResponse.BodyHandlers.ofString());
    }

    private HttpResponse<String> webhook(String base, String body, String event) throws Exception {
        Map<String, String> h = new HashMap<>();
        h.put("X-GitHub-Event", event);
        return send("POST", base + "/webhooks/github", body, h);
    }

    @Test
    void withNoSecretAndNoOptInTheWebhookRefusesEverything() throws Exception {
        String base = start(Map.of());
        HttpResponse<String> r = webhook(base, "{}", "push");
        assertEquals(503, r.statusCode());
        assertEquals("webhook secret not configured", JSON.readTree(r.body()).get("error").asText());
    }

    @Test
    void unsignedWebhooksAreAcceptedOnlyWhenExplicitlyAllowed() throws Exception {
        String base = start(Map.of("ALLOW_UNSIGNED_WEBHOOKS", "true"));
        HttpResponse<String> r = webhook(base, "{\"repository\":{\"full_name\":\"a/b\"}}", "push");
        assertEquals(202, r.statusCode());
        assertEquals("application/json", r.headers().firstValue("Content-Type").orElse(""));
    }

    @Test
    void aBodyOverTheLimitIsRefused() throws Exception {
        String base = start(Map.of("ALLOW_UNSIGNED_WEBHOOKS", "true", "MAX_BODY_BYTES", "20"));
        HttpResponse<String> r = webhook(base, "{\"a\":\"" + "x".repeat(100) + "\"}", "push");
        assertEquals(413, r.statusCode());
        assertEquals("request body too large", JSON.readTree(r.body()).get("error").asText());
    }

    @Test
    void anArrayBodyAndAnEmptyBodyAreNotPayloads() throws Exception {
        String base = start(Map.of("ALLOW_UNSIGNED_WEBHOOKS", "true"));
        assertEquals(400, webhook(base, "[1,2]", "push").statusCode());
        assertEquals(400, webhook(base, "", "push").statusCode());
        assertEquals(202, webhook(base, "null", "push").statusCode(), "JSON null is an empty payload, as in the Go service");
    }

    @Test
    void aFailedCriticalDeliveryAnswers500WithoutLeakingTheCause() throws Exception {
        NotificationProvider down = new NotificationProvider() {
            @Override public void send(Notification n) { throw new PortException("smtp password wrong", null); }
            @Override public String name() { return "down"; }
        };
        String base = start(Map.of("ALLOW_UNSIGNED_WEBHOOKS", "true"), down, new InMemoryEventRepository(Clock.systemUTC()));
        HttpResponse<String> r = webhook(base, "{}", "security_advisory");
        assertEquals(500, r.statusCode());
        assertEquals("internal error", JSON.readTree(r.body()).get("error").asText());
        assertTrue(!r.body().contains("password"));
    }

    @Test
    void aBrokenStoreAnswers500OnTheListToo() throws Exception {
        EventRepository broken = new EventRepository() {
            @Override public void save(Event event) { throw new PortException("db", null); }
            @Override public boolean exists(String id) { return false; }
            @Override public List<Event> listRecent(int limit) { throw new PortException("db", null); }
        };
        String base = start(Map.of("API_KEY", "k"), OK, broken);
        HttpResponse<String> r = send("GET", base + "/events", null, Map.of("X-API-Key", "k"));
        assertEquals(500, r.statusCode());
    }

    @Test
    void theApiFailsClosedWithoutAKeyUnlessUnauthenticatedAccessIsAllowed() throws Exception {
        String closed = start(Map.of());
        HttpResponse<String> r = send("GET", closed + "/events", null, Map.of());
        assertEquals(503, r.statusCode());
        assertEquals("api key not configured", JSON.readTree(r.body()).get("error").asText());
        stop();

        String open = start(Map.of("ALLOW_UNAUTHENTICATED_API", "true"));
        assertEquals(200, send("GET", open + "/events", null, Map.of()).statusCode());
    }

    @Test
    void healthIsOpenAndUnknownPathsAre404() throws Exception {
        String base = start(Map.of());
        assertEquals(200, send("GET", base + "/health", null, Map.of()).statusCode());
        HttpResponse<String> r = send("GET", base + "/nope", null, Map.of());
        assertEquals(404, r.statusCode());
        assertEquals("not found", JSON.readTree(r.body()).get("error").asText());
        assertEquals(404, send("GET", base + "/healthz", null, Map.of()).statusCode(), "paths match exactly");
    }

    @Test
    void theListNeverIncludesThePayload() throws Exception {
        String base = start(Map.of("ALLOW_UNSIGNED_WEBHOOKS", "true", "API_KEY", "k"));
        webhook(base, "{\"sender\":{\"email\":\"person@example.com\"}}", "push");
        HttpResponse<String> r = send("GET", base + "/events", null, Map.of("X-API-Key", "k"));
        assertTrue(!r.body().contains("person@example.com"));
        JsonNode first = JSON.readTree(r.body()).get("events").get(0);
        assertNotNull(first.get("createdAt"));
        assertNull(first.get("payload"));
    }

    @Test
    void signaturesAreVerifiedOverTheExactBytes() {
        byte[] body = "{\"a\":1}".getBytes(StandardCharsets.UTF_8);
        String good = Signature.sign("secret", body);
        assertTrue(Signature.valid("secret", body, good));
        assertTrue(!Signature.valid("other", body, good));
        assertTrue(!Signature.valid("secret", "{\"a\": 1}".getBytes(StandardCharsets.UTF_8), good));
        assertTrue(!Signature.valid("secret", body, null));
        assertTrue(!Signature.valid("secret", body, "sha1=abc"));
        assertTrue(!Signature.valid("secret", body, "sha256=zz"));
    }

    @Test
    void cursorsRoundTripAndRejectGarbage() {
        Event e = new Event("id-1", "github", "push", "", null);
        e.setCreatedAt(java.time.Instant.parse("2026-03-01T12:00:00.123456789Z"));
        Cursor c = Cursor.decode(Cursor.of(e).encode());
        assertEquals(e.getCreatedAt(), c.at());
        assertEquals("id-1", c.id());

        assertNull(Cursor.decode(null));
        assertNull(Cursor.decode(""));
        assertNull(Cursor.decode("!!!"));
        assertNull(Cursor.decode("a".repeat(401)));
        assertNull(Cursor.decode(java.util.Base64.getUrlEncoder().withoutPadding().encodeToString("{\"t\":1}".getBytes())));
        assertNull(Cursor.decode(java.util.Base64.getUrlEncoder().withoutPadding().encodeToString("{\"t\":\"x\",\"i\":\"a\"}".getBytes())));

        Event older = new Event("id-0", "github", "push", "", null);
        older.setCreatedAt(e.getCreatedAt().minusNanos(1));
        Event sameTimeSmallerId = new Event("id-0", "github", "push", "", null);
        sameTimeSmallerId.setCreatedAt(e.getCreatedAt());
        assertTrue(c.isAfter(older));
        assertTrue(c.isAfter(sameTimeSmallerId));
        assertTrue(!c.isAfter(e));
    }

    @Test
    void queryStringsAreDecoded() {
        Map<String, String> q = Query.parse("limit=2&cursor=a%2Bb&flag&&x=1&x=2");
        assertEquals("2", q.get("limit"));
        assertEquals("a+b", q.get("cursor"));
        assertEquals("", q.get("flag"));
        assertEquals("1", q.get("x"));
        assertTrue(Query.parse(null).isEmpty());
        assertTrue(Query.parse("").isEmpty());
    }
}
