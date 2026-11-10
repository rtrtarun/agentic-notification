package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Feedback;
import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Overrides;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.FeedbackStores;
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
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** /feedback and /api/overrides through a real HTTP server: who may do what, and what is refused. */
class FeedbackApiTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Map<String, String> KEY = Map.of("X-API-Key", "k");
    private static final String PASSWORD = "a long enough password";
    private static final List<Rule> SEED = List.of(new Rule("", "default", "", new Rule.Condition("github", "", ""),
            new Rule.Outcome("standard", "default routing", false), false, false));
    private static final NotificationProvider OK = new NotificationProvider() {
        @Override public void send(Notification n) { }
        @Override public String name() { return "ok"; }
    };

    private final HttpClient client = HttpClient.newHttpClient();
    private HttpServer server;
    private String base;
    private Accounts.Service accounts;

    @AfterEach
    void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private void start(Map<String, String> extraEnv, boolean withServices) throws Exception {
        Map<String, String> env = new HashMap<>(Map.of("API_KEY", "k", "ALLOW_UNSIGNED_WEBHOOKS", "true"));
        env.putAll(extraEnv);
        Config config = Config.load(env);
        EventRepository repo = new InMemoryEventRepository(Clock.systemUTC());
        PolicyEngine engine = new PolicyEngine(List.of());
        RulesService rules = new RulesService(new MemoryStores.Rules(), engine, SEED);
        accounts = new Accounts.Service(new MemoryAccountStore(), Duration.ofHours(1), Clock.systemUTC());
        accounts.bootstrap("admin@corp.com", PASSWORD);
        accounts.createUser("test", "member@corp.com", "Member", "member", PASSWORD);
        Overrides.Service overrides = withServices ? new Overrides.Service(new FeedbackStores.MemoryOverrides(), Clock.systemUTC()) : null;
        Feedback.Service feedback = withServices ? new Feedback.Service(new FeedbackStores.MemoryFeedback(),
                id -> repo.listRecent(1000).stream().filter(e -> e.getId().equals(id)).findFirst(), overrides, Clock.systemUTC()) : null;
        Pipeline pipeline = new Pipeline(engine, OK, repo);
        if (withServices) {
            pipeline.withOverrides(overrides);
        }
        server = new HttpApi(config, pipeline, repo, new Notify.Service(config.notifyLimits(), OK), rules,
                new MemoryStores.Deliveries(), Clock.systemUTC(), accounts, overrides, feedback).start(0);
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

    private void sendEvent(String delivery) throws Exception {
        assertEquals(202, call("POST", "/webhooks/github",
                "{\"action\":\"opened\",\"number\":7,\"repository\":{\"full_name\":\"acme/api\"}}",
                Map.of("X-GitHub-Event", "pull_request", "X-GitHub-Delivery", delivery)).statusCode());
    }

    @Test
    void aSignedInMemberGivesFeedbackAndMakesOverridesByDefault() throws Exception {
        start(Map.of(), true);
        sendEvent("m1");
        Map<String, String> member = signIn("member@corp.com");
        HttpResponse<String> never = call("POST", "/feedback", "{\"eventId\":\"github-pull_request-m1\",\"action\":\"never\"}", member);
        assertEquals(202, never.statusCode(), never.body());
        assertEquals("member@corp.com", json(never).get("override").get("createdBy").asText(), "attributed to the person, not the key");
        assertEquals(1, json(call("GET", "/api/overrides", null, member)).get("overrides").size());
        assertTrue(accounts.audit(10).stream().anyMatch(a -> a.action().equals("override.never") && a.actor().equals("member@corp.com")),
                "an override is a security-relevant change, so it is in the audit trail");
    }

    @Test
    void aCookieThatChangesSomethingNeedsTheCsrfToken() throws Exception {
        start(Map.of(), true);
        sendEvent("m2");
        Map<String, String> member = signIn("member@corp.com");
        Map<String, String> noCsrf = Map.of("Cookie", member.get("Cookie"));
        assertEquals(403, call("POST", "/feedback", "{\"eventId\":\"github-pull_request-m2\",\"action\":\"opened\"}", noCsrf).statusCode());
        assertEquals(403, call("POST", "/api/overrides", "{\"mode\":\"never\",\"source\":\"github\"}", noCsrf).statusCode());
        assertEquals(200, call("GET", "/api/overrides", null, noCsrf).statusCode(), "reading needs no token");
    }

    @Test
    void withOverridesAdminOnlyAMembersChoiceIsOnlyASignalAndTheyCannotManageOverrides() throws Exception {
        start(Map.of("OVERRIDES_ADMIN_ONLY", "true"), true);
        sendEvent("m3");
        Map<String, String> member = signIn("member@corp.com");
        HttpResponse<String> never = call("POST", "/feedback", "{\"eventId\":\"github-pull_request-m3\",\"action\":\"never\"}", member);
        assertEquals(202, never.statusCode());
        assertTrue(json(never).get("overrideSkipped").asBoolean());
        assertTrue(json(never).get("override").isNull());

        assertEquals(403, call("POST", "/api/overrides", "{\"mode\":\"never\",\"source\":\"github\"}", member).statusCode());
        assertEquals(403, call("DELETE", "/api/overrides/anything", null, member).statusCode());
        JsonNode list = json(call("GET", "/api/overrides", null, member));
        assertTrue(list.get("adminOnly").asBoolean());
        assertEquals(0, list.get("overrides").size());

        Map<String, String> admin = signIn("admin@corp.com");
        assertEquals(201, call("POST", "/api/overrides", "{\"mode\":\"never\",\"source\":\"github\"}", admin).statusCode());
    }

    @Test
    void theFeedbackAndOverridesEndpointsNeedSignInOrTheKey() throws Exception {
        start(Map.of(), true);
        assertEquals(401, call("POST", "/feedback", "{}", Map.of()).statusCode());
        assertEquals(401, call("GET", "/api/overrides", null, Map.of()).statusCode());
        assertEquals(401, call("GET", "/api/overrides", null, Map.of("X-API-Key", "wrong")).statusCode());
    }

    @Test
    void badBodiesAreRefusedBeforeAnythingHappens() throws Exception {
        start(Map.of(), true);
        assertEquals(400, call("POST", "/feedback", "not json", KEY).statusCode());
        assertEquals(400, call("POST", "/feedback", "[1]", KEY).statusCode());
        assertEquals(400, call("POST", "/feedback", "{\"eventId\":5,\"action\":\"opened\"}", KEY).statusCode(), "a number is not an id");
        assertEquals(400, call("POST", "/api/overrides", "[1]", KEY).statusCode());
        assertEquals(400, call("POST", "/api/overrides", "{\"mode\":\"never\",\"source\":5}", KEY).statusCode());
        assertEquals(400, call("POST", "/api/overrides", "not json", KEY).statusCode());
    }

    @Test
    void wrongMethodsAndPathsAreRefused() throws Exception {
        start(Map.of(), true);
        assertEquals(405, call("GET", "/feedback", null, KEY).statusCode());
        assertEquals(405, call("PUT", "/api/overrides", "{}", KEY).statusCode());
        assertEquals(405, call("GET", "/api/overrides/abc", null, KEY).statusCode());
        assertEquals(405, call("DELETE", "/api/overrides/a/b", null, KEY).statusCode());
        assertEquals(404, call("DELETE", "/api/overrides/missing", null, KEY).statusCode());
    }

    @Test
    void withoutTheServicesBothEndpointsSayTheyAreNotAvailable() throws Exception {
        start(Map.of(), false);
        assertEquals(503, call("POST", "/feedback", "{\"eventId\":\"x\",\"action\":\"opened\"}", KEY).statusCode());
        assertEquals(503, call("GET", "/api/overrides", null, KEY).statusCode());
    }

    @Test
    void anOverrideCreatedThroughTheApiChangesTheNextEventAndRemovingItChangesItBack() throws Exception {
        start(Map.of(), true);
        HttpResponse<String> made = call("POST", "/api/overrides", "{\"mode\":\"never\",\"source\":\"github\",\"note\":\"quiet\"}", KEY);
        assertEquals(201, made.statusCode());
        String id = json(made).get("id").asText();
        assertEquals("quiet", json(made).get("note").asText());

        HttpResponse<String> held = call("POST", "/webhooks/github", "{\"action\":\"opened\",\"number\":9,\"repository\":{\"full_name\":\"acme/api\"}}",
                Map.of("X-GitHub-Event", "pull_request", "X-GitHub-Delivery", "o1"));
        assertEquals("background", json(held).get("attentionClass").asText());
        assertEquals(204, call("DELETE", "/api/overrides/" + id, null, KEY).statusCode());
        HttpResponse<String> back = call("POST", "/webhooks/github", "{\"action\":\"opened\",\"number\":10,\"repository\":{\"full_name\":\"acme/api\"}}",
                Map.of("X-GitHub-Event", "pull_request", "X-GitHub-Delivery", "o2"));
        assertEquals("standard", json(back).get("attentionClass").asText());
        assertFalse(json(call("GET", "/api/overrides", null, KEY)).get("overrides").elements().hasNext());
    }
}
