package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Feedback;
import com.agentpulse.notification.application.Learning;
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
import com.agentpulse.notification.infrastructure.LearningStores;
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

/** /api/learning/suggestions through a real HTTP server: who may do what, what is audited, what is refused. */
class LearningApiTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Map<String, String> KEY = Map.of("X-API-Key", "k");
    private static final String PASSWORD = "a long enough password";
    private static final List<Rule> SEED = List.of(
            new Rule("", "advisory", "", new Rule.Condition("github", "security_advisory", ""), new Rule.Outcome("critical", "security", false), false, true),
            new Rule("", "default", "", new Rule.Condition("github", "", ""), new Rule.Outcome("standard", "default routing", false), false, false));
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

    private void start(boolean withLearning) throws Exception {
        Config config = Config.load(Map.of("API_KEY", "k", "ALLOW_UNSIGNED_WEBHOOKS", "true"));
        EventRepository repo = new InMemoryEventRepository(Clock.systemUTC());
        PolicyEngine engine = new PolicyEngine(List.of());
        RulesService rules = new RulesService(new MemoryStores.Rules(), engine, SEED);
        accounts = new Accounts.Service(new MemoryAccountStore(), Duration.ofHours(1), Clock.systemUTC());
        accounts.bootstrap("admin@corp.com", PASSWORD);
        accounts.createUser("test", "member@corp.com", "Member", "member", PASSWORD);
        Overrides.Service overrides = new Overrides.Service(new FeedbackStores.MemoryOverrides(), Clock.systemUTC());
        Feedback.Store feedbackStore = new FeedbackStores.MemoryFeedback();
        Feedback.Service feedback = new Feedback.Service(feedbackStore,
                id -> repo.listRecent(1000).stream().filter(e -> e.getId().equals(id)).findFirst(), overrides, Clock.systemUTC());
        HttpApi api = new HttpApi(config, new Pipeline(engine, OK, repo).withOverrides(overrides), repo, new Notify.Service(config.notifyLimits(), OK),
                rules, new MemoryStores.Deliveries(), Clock.systemUTC(), accounts, overrides, feedback);
        if (withLearning) {
            api.withLearning(new Learning.Service(new LearningStores.Memory(), feedbackStore::list, () -> repo.listRecent(1000),
                    Learning.ruleEditor(rules), overrides::upsert, new Learning.Config(5, 0, null), Clock.systemUTC()), accounts);
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

    /** Five different pull requests, each dismissed. */
    private void fiveDismissals() throws Exception {
        for (int i = 1; i <= 5; i++) {
            assertEquals(202, call("POST", "/webhooks/github", "{\"action\":\"opened\",\"number\":" + i + ",\"repository\":{\"full_name\":\"acme/api\"}}",
                    Map.of("X-GitHub-Event", "pull_request", "X-GitHub-Delivery", "d" + i)).statusCode());
            assertEquals(202, call("POST", "/feedback", "{\"eventId\":\"github-pull_request-d" + i + "\",\"action\":\"dismissed\"}", KEY).statusCode());
        }
    }

    private String firstSuggestionId() throws Exception {
        JsonNode list = json(call("GET", "/api/learning/suggestions", null, KEY));
        assertEquals(1, list.get("suggestions").size(), list.toString());
        return list.get("suggestions").get(0).get("id").asText();
    }

    @Test
    void aMemberMayReadSuggestionsButOnlyAnAdminMayApplyOrDismiss() throws Exception {
        start(true);
        fiveDismissals();
        Map<String, String> member = signIn("member@corp.com");
        assertEquals(200, call("GET", "/api/learning/suggestions", null, member).statusCode());
        String id = firstSuggestionId();
        assertEquals(403, call("POST", "/api/learning/suggestions/" + id + "/apply", "", member).statusCode());
        assertEquals(403, call("POST", "/api/learning/suggestions/" + id + "/dismiss", "", member).statusCode());
        assertEquals("pending", json(call("GET", "/api/learning/suggestions", null, member)).get("suggestions").get(0).get("status").asText(),
                "a refused request changed nothing");
    }

    @Test
    void anAdminApplyingChangesTheRuleAndIsAuditedWithTheProposal() throws Exception {
        start(true);
        fiveDismissals();
        String id = firstSuggestionId();
        Map<String, String> admin = signIn("admin@corp.com");

        HttpResponse<String> applied = call("POST", "/api/learning/suggestions/" + id + "/apply", "", admin);

        assertEquals(200, applied.statusCode(), applied.body());
        assertEquals("applied", json(applied).get("suggestion").get("status").asText());
        assertEquals("admin@corp.com", json(applied).get("suggestion").get("resolvedBy").asText());
        JsonNode rules = json(call("GET", "/api/rules", null, KEY)).get("rules");
        boolean adaptive = false;
        for (JsonNode r : rules) {
            if (r.get("name").asText().equals("default")) {
                adaptive = r.get("then").path("adaptive").asBoolean(false);
            }
        }
        assertTrue(adaptive, "the standard, non-adaptive rule was made adaptive");
        assertTrue(accounts.audit(10).stream().anyMatch(a -> a.action().equals("learning.apply") && a.actor().equals("admin@corp.com")
                && a.detail().startsWith("Let Attention refine the rule")), "an applied suggestion is in the audit trail");
        assertEquals(409, call("POST", "/api/learning/suggestions/" + id + "/apply", "", admin).statusCode());
    }

    @Test
    void aSessionThatChangesSomethingNeedsTheCsrfToken() throws Exception {
        start(true);
        fiveDismissals();
        String id = firstSuggestionId();
        Map<String, String> admin = signIn("admin@corp.com");
        assertEquals(403, call("POST", "/api/learning/suggestions/" + id + "/apply", "", Map.of("Cookie", admin.get("Cookie"))).statusCode());
    }

    @Test
    void dismissingLeavesTheRuleAloneAndIsAudited() throws Exception {
        start(true);
        fiveDismissals();
        String id = firstSuggestionId();
        HttpResponse<String> r = call("POST", "/api/learning/suggestions/" + id + "/dismiss", "", KEY);
        assertEquals(200, r.statusCode());
        assertEquals("dismissed", json(r).get("suggestion").get("status").asText());
        assertEquals("api-key", json(r).get("suggestion").get("resolvedBy").asText());
        assertEquals(0, json(call("GET", "/api/learning/suggestions", null, KEY)).get("suggestions").size());
        assertTrue(accounts.audit(10).stream().anyMatch(a -> a.action().equals("learning.dismiss")));
    }

    @Test
    void pathsMethodsAndSignInAreChecked() throws Exception {
        start(true);
        assertEquals(401, call("GET", "/api/learning/suggestions", null, Map.of()).statusCode());
        assertEquals(401, call("POST", "/api/learning/suggestions/x/apply", "", Map.of()).statusCode());
        assertEquals(404, call("POST", "/api/learning/suggestions/x/apply", "", KEY).statusCode());
        assertEquals(404, call("POST", "/api/learning/suggestions/x/explode", "", KEY).statusCode());
        assertEquals(404, call("POST", "/api/learning/suggestions/x", "", KEY).statusCode());
        assertEquals(404, call("POST", "/api/learning/suggestions/x/apply/y", "", KEY).statusCode());
        assertEquals(405, call("PUT", "/api/learning/suggestions", "{}", KEY).statusCode());
        assertEquals(405, call("DELETE", "/api/learning/suggestions/x/apply", null, KEY).statusCode());
        JsonNode empty = json(call("GET", "/api/learning/suggestions", null, KEY));
        assertEquals(0, empty.get("suggestions").size());
        assertEquals(5, empty.get("minSignals").asInt());
    }

    @Test
    void withoutLearningTheEndpointIsNotThere() throws Exception {
        start(false);
        assertEquals(404, call("GET", "/api/learning/suggestions", null, KEY).statusCode());
    }
}
