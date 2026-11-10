package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Digests;
import com.agentpulse.notification.application.Heuristic;
import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.DigestStores;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import com.agentpulse.notification.infrastructure.MemoryAccountStore;
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
import java.time.Duration;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** /api/digests, /api/explain and /api/attention/status through a real HTTP server: who may do what, and what is refused. */
class AttentionApiTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Map<String, String> KEY = Map.of("X-API-Key", "k");
    private static final String PASSWORD = "a long enough password";
    private static final List<Rule> SEED = List.of(
            new Rule("", "stars", "", new Rule.Condition("github", "star", ""), new Rule.Outcome("background", "quiet", false), false, false),
            new Rule("", "default", "", new Rule.Condition("github", "", ""), new Rule.Outcome("standard", "default routing", false), false, false));
    private static final NotificationProvider OK = new NotificationProvider() {
        @Override public void send(Notification n) { }
        @Override public String name() { return "ok"; }
    };

    private final HttpClient client = HttpClient.newHttpClient();
    private HttpServer server;
    private String base;

    @AfterEach
    void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private void start(boolean withAttention, Duration every) throws Exception {
        Config config = Config.load(Map.of("API_KEY", "k", "ALLOW_UNSIGNED_WEBHOOKS", "true"));
        EventRepository repo = new InMemoryEventRepository(Clock.systemUTC());
        PolicyEngine engine = new PolicyEngine(List.of());
        RulesService rules = new RulesService(new MemoryStores.Rules(), engine, SEED);
        Accounts.Service accounts = new Accounts.Service(new MemoryAccountStore(), Duration.ofHours(1), Clock.systemUTC());
        accounts.bootstrap("admin@corp.com", PASSWORD);
        accounts.createUser("test", "member@corp.com", "Member", "member", PASSWORD);
        HttpApi api = new HttpApi(config, new Pipeline(engine, OK, repo), repo, new Notify.Service(config.notifyLimits(), OK), rules,
                new MemoryStores.Deliveries(), Clock.systemUTC(), accounts, null, null);
        if (withAttention) {
            Heuristic h = new Heuristic();
            Digests.Store store = new DigestStores.Memory();
            Digests.Generator generator = new Digests.Generator(() -> repo.listRecent(1000), store, h, h, Clock.systemUTC());
            api.withAttention(generator, store, id -> repo.listRecent(1000).stream().filter(e -> e.getId().equals(id)).findFirst(), every, accounts);
        }
        server = api.start(0);
        base = "http://localhost:" + server.getAddress().getPort();
    }

    private HttpResponse<String> call(String method, String path, String body, Map<String, String> headers) throws Exception {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(base + path))
                .method(method, HttpRequest.BodyPublishers.ofString(body == null ? "" : body));
        headers.forEach(b::header);
        return client.send(b.build(), HttpResponse.BodyHandlers.ofString());
    }

    private static JsonNode json(HttpResponse<String> r) throws Exception {
        return JSON.readTree(r.body());
    }

    private Map<String, String> signIn(String email) throws Exception {
        HttpResponse<String> r = call("POST", "/api/auth/login", "{\"email\":\"" + email + "\",\"password\":\"" + PASSWORD + "\"}", Map.of());
        assertEquals(200, r.statusCode(), r.body());
        String cookie = r.headers().firstValue("Set-Cookie").orElseThrow();
        return Map.of("Cookie", cookie.substring(0, cookie.indexOf(';')), "X-CSRF-Token", json(r).get("csrfToken").asText());
    }

    private void star(String delivery) throws Exception {
        assertEquals(202, call("POST", "/webhooks/github", "{\"action\":\"created\",\"repository\":{\"full_name\":\"acme/web\"}}",
                Map.of("X-GitHub-Event", "star", "X-GitHub-Delivery", delivery)).statusCode());
    }

    @Test
    void aMemberCanReadDigestsAndExplanationsButOnlyAnAdminCanGenerateOrSeeTheStatus() throws Exception {
        start(true, Duration.ofMinutes(30));
        star("a1");
        Map<String, String> member = signIn("member@corp.com");
        assertEquals(403, call("POST", "/api/digests/generate", "", member).statusCode());
        assertEquals(403, call("GET", "/api/attention/status", null, member).statusCode());
        assertEquals(200, call("GET", "/api/digests", null, member).statusCode());
        assertEquals(200, call("GET", "/api/explain/github-star-a1", null, member).statusCode());

        Map<String, String> admin = signIn("admin@corp.com");
        HttpResponse<String> made = call("POST", "/api/digests/generate", "", admin);
        assertEquals(201, made.statusCode(), made.body());
        assertEquals("admin@corp.com", json(made).get("digest").get("createdBy").asText());
        String id = json(made).get("digest").get("id").asText();
        assertEquals(id, json(call("GET", "/api/digests/" + id, null, member)).get("id").asText());
        assertEquals("30m0s", json(call("GET", "/api/digests", null, member)).get("every").asText());
        assertEquals("heuristic", json(call("GET", "/api/attention/status", null, admin)).get("provider").asText());
    }

    @Test
    void generatingNeedsTheCsrfTokenForASession() throws Exception {
        start(true, Duration.ZERO);
        Map<String, String> admin = signIn("admin@corp.com");
        assertEquals(403, call("POST", "/api/digests/generate", "", Map.of("Cookie", admin.get("Cookie"))).statusCode());
    }

    @Test
    void personalDigestsAreRefusedNotAnsweredWithTheWorkspaceOnes() throws Exception {
        start(true, Duration.ZERO);
        HttpResponse<String> list = call("GET", "/api/digests?scope=me", null, KEY);
        assertEquals(400, list.statusCode());
        assertTrue(list.body().contains("not available in this service yet"), list.body());
        assertEquals(400, call("POST", "/api/digests/generate?scope=me", "", KEY).statusCode());
    }

    @Test
    void limitsPathsAndMethodsAreChecked() throws Exception {
        start(true, Duration.ZERO);
        assertEquals(400, call("GET", "/api/digests?limit=0", null, KEY).statusCode());
        assertEquals(400, call("GET", "/api/digests?limit=101", null, KEY).statusCode());
        assertEquals(400, call("GET", "/api/digests?limit=many", null, KEY).statusCode());
        assertEquals(200, call("GET", "/api/digests?limit=100", null, KEY).statusCode());
        assertEquals(404, call("GET", "/api/digests/nope", null, KEY).statusCode());
        assertEquals(405, call("PUT", "/api/digests", "{}", KEY).statusCode());
        assertEquals(405, call("GET", "/api/digests/generate", null, KEY).statusCode());
        assertEquals(405, call("GET", "/api/digests/a/b", null, KEY).statusCode());
        assertEquals(405, call("POST", "/api/attention/status", "{}", KEY).statusCode());
        assertEquals(405, call("POST", "/api/explain/x", "{}", KEY).statusCode());
        assertEquals(404, call("GET", "/api/explain/", null, KEY).statusCode());
        assertEquals(404, call("GET", "/api/explain/a/b", null, KEY).statusCode());
        assertEquals(404, call("GET", "/api/explain/missing", null, KEY).statusCode());
    }

    @Test
    void everythingNeedsSignInOrTheKey() throws Exception {
        start(true, Duration.ZERO);
        assertEquals(401, call("GET", "/api/digests", null, Map.of()).statusCode());
        assertEquals(401, call("POST", "/api/digests/generate", "", Map.of()).statusCode());
        assertEquals(401, call("GET", "/api/explain/x", null, Map.of()).statusCode());
        assertEquals(401, call("GET", "/api/attention/status", null, Map.of()).statusCode());
    }

    @Test
    void withoutAttentionTheDigestEndpointsAreSimplyNotThere() throws Exception {
        start(false, Duration.ZERO);
        assertEquals(404, call("GET", "/api/digests", null, KEY).statusCode());
        assertEquals(404, call("GET", "/api/explain/x", null, KEY).statusCode());
        assertEquals(404, call("GET", "/api/attention/status", null, KEY).statusCode());
    }

    @Test
    void durationsAreWrittenTheWayGoWritesThem() {
        assertEquals("0s", AttentionApi.goDuration(Duration.ZERO));
        assertEquals("45s", AttentionApi.goDuration(Duration.ofSeconds(45)));
        assertEquals("5m0s", AttentionApi.goDuration(Duration.ofMinutes(5)));
        assertEquals("1m30s", AttentionApi.goDuration(Duration.ofSeconds(90)));
        assertEquals("1h0m0s", AttentionApi.goDuration(Duration.ofHours(1)));
        assertEquals("24h30m5s", AttentionApi.goDuration(Duration.ofSeconds(24 * 3600 + 30 * 60 + 5)));
    }

    @Test
    void anEmptyListIsAnEmptyArrayAndASecondGenerateSaysNothingWasMade() throws Exception {
        start(true, Duration.ZERO);
        assertEquals(0, json(call("GET", "/api/digests", null, KEY)).get("digests").size());
        JsonNode none = json(call("POST", "/api/digests/generate", "", KEY));
        assertEquals(false, none.get("generated").asBoolean());
        assertTrue(none.get("digest").isNull());
        star("b1");
        assertEquals(201, call("POST", "/api/digests/generate", "", KEY).statusCode());
        assertEquals(false, json(call("POST", "/api/digests/generate", "", KEY)).get("generated").asBoolean());
    }
}
