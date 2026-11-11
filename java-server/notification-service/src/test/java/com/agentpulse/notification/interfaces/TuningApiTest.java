package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Feedback;
import com.agentpulse.notification.application.Heuristic;
import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Overrides;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.application.GuardedClassifier;
import com.agentpulse.notification.application.Tuning;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.FeedbackStores;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import com.agentpulse.notification.infrastructure.MemoryAccountStore;
import com.agentpulse.notification.infrastructure.MemoryStores;
import com.agentpulse.notification.infrastructure.TuningStores;
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
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** /api/learning/adjustments and /api/learning/runs through a real HTTP server: who may do what, what is audited, what is refused. */
class TuningApiTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Map<String, String> KEY = Map.of("X-API-Key", "k");
    private static final String PASSWORD = "a long enough password";
    private static final List<Rule> SEED = List.of(
            new Rule("", "advisory", "", new Rule.Condition("github", "security_advisory", ""), new Rule.Outcome("critical", "security", false), false, true),
            new Rule("", "default", "", new Rule.Condition("github", "", ""), new Rule.Outcome("standard", "default routing", true), false, false));
    private static final NotificationProvider OK = new NotificationProvider() {
        @Override public void send(Notification n) { }
        @Override public String name() { return "ok"; }
    };

    private final HttpClient client = HttpClient.newHttpClient();
    private HttpServer server;
    private String base;
    private Accounts.Service accounts;
    private Tuning.Tuner tuner;

    @AfterEach
    void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private void start(boolean withTuning) throws Exception {
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
        Tuning.Snapshot snapshot = new Tuning.Snapshot(Tuning.DEFAULT_MAX_AGE, Clock.systemUTC());
        Pipeline pipeline = new Pipeline(engine, OK, repo).withOverrides(overrides).withClassifier(GuardedClassifier.of(new Heuristic().withTuning(snapshot)));
        HttpApi api = new HttpApi(config, pipeline, repo, new Notify.Service(config.notifyLimits(), OK),
                rules, new MemoryStores.Deliveries(), Clock.systemUTC(), accounts, overrides, feedback);
        tuner = new Tuning.Tuner(new TuningStores.MemoryAdjustments(), new TuningStores.MemoryRuns(), feedbackStore::list,
                () -> repo.listRecent(1000), rules::list, snapshot, new Tuning.Config(5, 2, 20, null, null), Clock.systemUTC());
        if (withTuning) {
            api.withTuning(tuner, accounts, Duration.ZERO);
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

    private HttpResponse<String> pullRequest(int number, String delivery) throws Exception {
        return call("POST", "/webhooks/github", "{\"action\":\"opened\",\"number\":" + number + ",\"repository\":{\"full_name\":\"acme/api\"}}",
                Map.of("X-GitHub-Event", "pull_request", "X-GitHub-Delivery", delivery));
    }

    /** Five pull requests, dismissed by the API key and a member in turn: two people. */
    private void fiveDismissalsByTwoPeople(Map<String, String> member) throws Exception {
        for (int i = 1; i <= 5; i++) {
            assertEquals(202, pullRequest(i, "d" + i).statusCode());
            assertEquals(202, call("POST", "/feedback", "{\"eventId\":\"github-pull_request-d" + i + "\",\"action\":\"dismissed\"}",
                    i % 2 == 0 ? member : KEY).statusCode());
        }
    }

    private JsonNode adjustments(String query) throws Exception {
        return json(call("GET", "/api/learning/adjustments" + query, null, KEY));
    }

    @Test
    void feedbackFromTwoPeopleBecomesAProposalThatChangesNothingUntilAnAdminApprovesIt() throws Exception {
        start(true);
        Map<String, String> member = signIn("member@corp.com");
        fiveDismissalsByTwoPeople(member);

        JsonNode empty = adjustments("");
        assertEquals(0, empty.get("adjustments").size());
        assertEquals(20, empty.get("maxAdjustment").asInt());
        assertEquals(5, empty.get("minSignals").asInt());
        assertEquals(2, empty.get("minPeople").asInt());
        assertEquals(403, call("POST", "/api/learning/runs?wait=true", "", member).statusCode(), "a member may not start a run");

        HttpResponse<String> run = call("POST", "/api/learning/runs?wait=true", "", KEY);
        assertEquals(200, run.statusCode(), run.body());
        assertEquals("done", json(run).at("/run/status").asText());
        assertEquals(1, json(run).at("/run/proposalsMade").asInt());
        assertEquals(5, json(run).at("/run/feedbackRead").asInt());

        JsonNode proposed = json(call("GET", "/api/learning/adjustments?status=proposed", null, member));
        assertEquals(1, proposed.get("adjustments").size(), "a member may read proposals");
        JsonNode adj = proposed.get("adjustments").get(0);
        assertEquals(-14, adj.get("delta").asInt());
        assertEquals("proposed", adj.get("status").asText());
        assertEquals(2, adj.at("/evidence/people").asInt());
        assertEquals("5 of 5 signals from 2 people were negative", adj.at("/evidence/summary").asText());
        String id = adj.get("id").asText();

        assertFalse(json(pullRequest(90, "after-run")).toString().contains("learned adjustment"), "a proposal must not change a decision");
        assertEquals(403, call("POST", "/api/learning/adjustments/" + id + "/approve", "", member).statusCode());

        HttpResponse<String> approved = call("POST", "/api/learning/adjustments/" + id + "/approve", "", KEY);
        assertEquals(200, approved.statusCode(), approved.body());
        assertEquals("active", json(approved).at("/adjustment/status").asText());
        assertEquals(1, json(approved).at("/adjustment/version").asInt());
        assertEquals(409, call("POST", "/api/learning/adjustments/" + id + "/approve", "", KEY).statusCode());
        assertEquals(1, adjustments("?status=active").get("version").asInt());

        String after = pullRequest(91, "after-approve").body();
        assertTrue(after.contains("learned adjustment -14: 5 of 5 signals from 2 people were negative"), after);
        assertTrue(after.contains("heuristic-v1+tuned-1"), after);

        HttpResponse<String> retired = call("POST", "/api/learning/adjustments/" + id + "/retire", "", KEY);
        assertEquals("retired", json(retired).at("/adjustment/status").asText());
        assertEquals(2, json(retired).at("/adjustment/version").asInt());
        String restored = pullRequest(92, "after-retire").body();
        assertFalse(restored.contains("learned adjustment") || restored.contains("tuned"), restored);

        for (String action : List.of("learning.run", "learning.approve", "learning.retire")) {
            assertTrue(accounts.audit(20).stream().anyMatch(a -> a.action().equals(action)), action + " must be audited");
        }
        assertTrue(accounts.audit(20).stream().anyMatch(a -> a.action().equals("learning.approve") && a.actor().equals("api-key")
                && a.detail().equals("5 of 5 signals from 2 people were negative")), "the audit entry carries the evidence");
    }

    @Test
    void rejectingAProposalChangesNothingAndItIsNotProposedAgain() throws Exception {
        start(true);
        fiveDismissalsByTwoPeople(signIn("member@corp.com"));
        call("POST", "/api/learning/runs?wait=true", "", KEY);
        String id = adjustments("").get("adjustments").get(0).get("id").asText();

        HttpResponse<String> rejected = call("POST", "/api/learning/adjustments/" + id + "/reject", "", KEY);
        assertEquals("rejected", json(rejected).at("/adjustment/status").asText());
        assertEquals(409, call("POST", "/api/learning/adjustments/" + id + "/retire", "", KEY).statusCode());
        assertEquals(0, json(call("POST", "/api/learning/runs?wait=true", "", KEY)).at("/run/proposalsMade").asInt());
        assertEquals(1, adjustments("?status=rejected").get("adjustments").size());
        assertTrue(accounts.audit(20).stream().anyMatch(a -> a.action().equals("learning.reject")), "rejecting must be audited");
    }

    @Test
    void aRunStartedWithoutWaitingReturnsAtOnceAndFinishesInTheBackground() throws Exception {
        start(true);
        HttpResponse<String> started = call("POST", "/api/learning/runs", "", KEY);
        assertEquals(202, started.statusCode(), started.body());
        assertEquals("running", json(started).at("/run/status").asText());
        tuner.await();
        JsonNode runs = json(call("GET", "/api/learning/runs", null, KEY));
        assertEquals("done", runs.at("/runs/0/status").asText());
        assertEquals("0s", runs.get("every").asText());
    }

    @Test
    void theEndpointsRefuseWhatTheyShould() throws Exception {
        start(true);
        assertEquals(401, call("GET", "/api/learning/adjustments", null, Map.of()).statusCode());
        assertEquals(401, call("GET", "/api/learning/runs", null, Map.of()).statusCode());
        assertEquals(400, call("GET", "/api/learning/adjustments?status=maybe", null, KEY).statusCode());
        assertEquals(405, call("PUT", "/api/learning/adjustments", "{}", KEY).statusCode());
        assertEquals(405, call("DELETE", "/api/learning/runs", null, KEY).statusCode());
        assertEquals(404, call("POST", "/api/learning/adjustments/abc/explode", "{}", KEY).statusCode());
        assertEquals(404, call("POST", "/api/learning/adjustments/abc", "{}", KEY).statusCode());
        assertEquals(404, call("POST", "/api/learning/adjustments/abc/approve", "{}", KEY).statusCode());
        assertEquals("adjustment not found", json(call("POST", "/api/learning/adjustments/abc/approve", "{}", KEY)).get("error").asText());
        assertEquals(404, call("GET", "/api/learning/runs/abc", null, KEY).statusCode());
        assertEquals(403, call("POST", "/api/learning/adjustments/abc/approve", "{}", signIn("member@corp.com")).statusCode(),
                "a member is refused before the id is looked up");
    }

    @Test
    void theEndpointsDoNotExistUntilTuningIsTurnedOn() throws Exception {
        start(false);
        assertEquals(404, call("GET", "/api/learning/adjustments", null, KEY).statusCode());
        assertEquals(404, call("GET", "/api/learning/runs", null, KEY).statusCode());
    }

    @Test
    void feedbackAboutCriticalEventsNeverBecomesAProposal() throws Exception {
        start(true);
        Map<String, String> member = signIn("member@corp.com");
        for (int i = 1; i <= 8; i++) {
            call("POST", "/webhooks/github", "{\"action\":\"published\",\"number\":" + i + ",\"repository\":{\"full_name\":\"acme/api\"}}",
                    Map.of("X-GitHub-Event", "security_advisory", "X-GitHub-Delivery", "c" + i));
            call("POST", "/feedback", "{\"eventId\":\"github-security_advisory-c" + i + "\",\"action\":\"dismissed\"}", i % 2 == 0 ? member : KEY);
        }
        assertEquals(0, json(call("POST", "/api/learning/runs?wait=true", "", KEY)).at("/run/proposalsMade").asInt());
        assertEquals(0, adjustments("").get("adjustments").size());
    }

    @Test
    void anExplicitOverrideStillWinsOverAnApprovedAdjustment() throws Exception {
        start(true);
        fiveDismissalsByTwoPeople(signIn("member@corp.com"));
        call("POST", "/api/learning/runs?wait=true", "", KEY);
        String id = adjustments("").get("adjustments").get(0).get("id").asText();
        call("POST", "/api/learning/adjustments/" + id + "/approve", "", KEY);

        assertEquals(201, call("POST", "/api/overrides", "{\"mode\":\"always\",\"eventType\":\"pull_request\"}", KEY).statusCode());
        String decision = pullRequest(93, "override-wins").body();
        assertFalse(decision.contains("learned adjustment"), decision);
        assertTrue(decision.contains("always notify override"), decision);
    }
}
