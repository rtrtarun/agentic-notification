package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Delivery;
import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.application.RulesService;
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
import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** /notify, the rules API, /api/policies and /api/deliveries through a real HTTP server. */
class ApiEndpointsTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Map<String, String> KEY = Map.of("X-API-Key", "k");
    private static final List<Rule> SEED = List.of(
            new Rule("", "all-critical", "", new Rule.Condition("github", "security_advisory", ""),
                    new Rule.Outcome("critical", "bad", false), false, true),
            new Rule("", "default", "", new Rule.Condition("github", "", ""),
                    new Rule.Outcome("standard", "default routing", false), false, false));
    private static final NotificationProvider OK = new NotificationProvider() {
        @Override public void send(Notification n) { }
        @Override public String name() { return "ok"; }
    };

    private final HttpClient client = HttpClient.newHttpClient();
    private HttpServer server;
    private MemoryStores.Deliveries deliveries;

    @AfterEach
    void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private String start(Map<String, String> env, NotificationProvider provider) throws Exception {
        Config config = Config.load(env);
        EventRepository repo = new InMemoryEventRepository(Clock.systemUTC());
        PolicyEngine engine = new PolicyEngine(List.of());
        RulesService rules = new RulesService(new MemoryStores.Rules(), engine, SEED);
        deliveries = new MemoryStores.Deliveries();
        server = new HttpApi(config, new Pipeline(engine, provider, repo), repo,
                new Notify.Service(config.notifyLimits(), provider), rules, deliveries, Clock.systemUTC()).start(0);
        return "http://localhost:" + server.getAddress().getPort();
    }

    private String start() throws Exception {
        return start(Map.of("API_KEY", "k", "ALLOW_UNSIGNED_WEBHOOKS", "true"), OK);
    }

    private HttpResponse<String> call(String method, String url, String body, Map<String, String> headers) throws Exception {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(url))
                .method(method, HttpRequest.BodyPublishers.ofString(body == null ? "" : body));
        headers.forEach(b::header);
        return client.send(b.build(), HttpResponse.BodyHandlers.ofString());
    }

    private HttpResponse<String> call(String method, String url, String body) throws Exception {
        return call(method, url, body, KEY);
    }

    private static String error(HttpResponse<String> r) throws Exception {
        return JSON.readTree(r.body()).get("error").asText();
    }

    private String webhookClass(String base, String type, String body) throws Exception {
        Map<String, String> h = new HashMap<>(Map.of("X-GitHub-Event", type));
        return JSON.readTree(call("POST", base + "/webhooks/github", body, h).body()).get("attentionClass").asText();
    }

    // ---- /notify -------------------------------------------------------------------------------------------------

    @Test
    void notifyNeedsTheKeyAndPostAndAValidJsonBody() throws Exception {
        String base = start();
        assertEquals(401, call("POST", base + "/notify", "{}", Map.of()).statusCode());
        assertEquals(405, call("GET", base + "/notify", null).statusCode());
        assertEquals(400, call("POST", base + "/notify", "not json").statusCode());
        assertEquals(400, call("POST", base + "/notify", "").statusCode());
        assertEquals(400, call("POST", base + "/notify", "{\"emailRecipients\":\"a@x.com\"}").statusCode(), "a string where a list belongs");
        HttpResponse<String> empty = call("POST", base + "/notify", "null");
        assertEquals(400, empty.statusCode());
        assertEquals("invalid notification request: agent name required", error(empty));
    }

    @Test
    void notifyAcceptsAValidRequestAndRefusesBrokenOnesWithTheMessage() throws Exception {
        String base = start();
        HttpResponse<String> ok = call("POST", base + "/notify", "{\"agent\":{\"name\":\"bot\"},\"title\":\"hi\",\"unknownField\":1}");
        assertEquals(202, ok.statusCode());
        assertEquals("accepted", JSON.readTree(ok.body()).get("status").asText());

        HttpResponse<String> ssrf = call("POST", base + "/notify",
                "{\"agent\":{\"name\":\"bot\"},\"title\":\"hi\",\"slackWebhookUrls\":[\"https://169.254.169.254/x\"]}");
        assertEquals(400, ssrf.statusCode());
        assertEquals("invalid notification request: webhook host not allowed", error(ssrf));
    }

    @Test
    void aFailedNotifyDeliveryAnswers500WithoutTheCause() throws Exception {
        NotificationProvider down = new NotificationProvider() {
            @Override public void send(Notification n) { throw new PortException("smtp password wrong", null); }
            @Override public String name() { return "down"; }
        };
        String base = start(Map.of("API_KEY", "k"), down);
        HttpResponse<String> r = call("POST", base + "/notify", "{\"agent\":{\"name\":\"bot\"},\"title\":\"hi\"}");
        assertEquals(500, r.statusCode());
        assertEquals("internal error", error(r));
    }

    // ---- rules ---------------------------------------------------------------------------------------------------

    private JsonNode rulesOf(String base) throws Exception {
        return JSON.readTree(call("GET", base + "/api/rules", null).body()).get("rules");
    }

    @Test
    void theRulesApiCreatesEditsReordersAndDeletesAndTheChangeAppliesToWebhooksAtOnce() throws Exception {
        String base = start();
        assertEquals(401, call("GET", base + "/api/rules", null, Map.of()).statusCode());
        assertEquals(2, rulesOf(base).size());
        assertTrue(rulesOf(base).get(0).get("locked").asBoolean());
        assertNull(rulesOf(base).get(1).get("locked"), "false flags are left out, as in Go");

        HttpResponse<String> created = call("POST", base + "/api/rules",
                "{\"name\":\"quiet pushes\",\"when\":{\"source\":\"github\",\"eventType\":\"push\"},\"then\":{\"attentionClass\":\"background\"}}");
        assertEquals(201, created.statusCode());
        String mine = JSON.readTree(created.body()).get("id").asText();
        assertEquals("matched rule quiet pushes", JSON.readTree(created.body()).get("then").get("reason").asText());

        JsonNode list = rulesOf(base);
        String defaultId = list.get(1).get("id").asText();
        String locked = list.get(0).get("id").asText();
        assertEquals("standard", webhookClass(base, "push", "{}"), "the broad default still wins");

        assertEquals(200, call("POST", base + "/api/rules/reorder",
                "{\"ids\":[\"" + locked + "\",\"" + mine + "\",\"" + defaultId + "\"]}").statusCode());
        assertEquals("background", webhookClass(base, "push", "{\"after\":\"x\"}"),
                "the reordered rule decides the next event without a restart");

        assertEquals(200, call("PUT", base + "/api/rules/" + mine,
                "{\"name\":\"quiet pushes\",\"when\":{\"source\":\"github\",\"eventType\":\"push\"},\"then\":{\"attentionClass\":\"standard\"}}").statusCode());
        assertEquals(409, call("PUT", base + "/api/rules/" + locked, "{\"name\":\"x\",\"then\":{\"attentionClass\":\"standard\"}}").statusCode());
        assertEquals(409, call("DELETE", base + "/api/rules/" + locked, null).statusCode());
        assertEquals(204, call("DELETE", base + "/api/rules/" + mine, null).statusCode());
        assertEquals(404, call("DELETE", base + "/api/rules/" + mine, null).statusCode());
        assertEquals(404, call("PUT", base + "/api/rules/nope", "{\"name\":\"x\",\"then\":{\"attentionClass\":\"standard\"}}").statusCode());
    }

    @Test
    void theRulesApiRefusesBadInputAndWrongMethods() throws Exception {
        String base = start();
        assertEquals(400, call("POST", base + "/api/rules", "[1]").statusCode());
        assertEquals(400, call("POST", base + "/api/rules", "not json").statusCode());
        assertEquals(400, call("POST", base + "/api/rules", "{\"name\":\"x\",\"then\":{\"attentionClass\":\"urgent\"}}").statusCode());
        HttpResponse<String> badOp = call("POST", base + "/api/rules",
                "{\"name\":\"x\",\"when\":{\"payloadMatch\":[{\"field\":\"a\",\"op\":\"between\",\"value\":1}]},\"then\":{\"attentionClass\":\"standard\"}}");
        assertEquals(400, badOp.statusCode());
        assertTrue(badOp.body().contains("unknown operator"), badOp.body());
        assertEquals(400, call("POST", base + "/api/rules",
                "{\"name\":\"x\",\"then\":{\"attentionClass\":\"standard\",\"channels\":[\"pager\"]}}").statusCode());
        assertEquals(400, call("POST", base + "/api/rules",
                "{\"name\":\"x\",\"when\":{\"payloadMatch\":[{\"feild\":\"a\",\"value\":1}]},\"then\":{\"attentionClass\":\"standard\"}}").statusCode(),
                "a misspelt key is refused, never dropped");
        HttpResponse<String> good = call("POST", base + "/api/rules",
                "{\"name\":\"quiet\",\"when\":{\"payloadMatch\":[{\"field\":\"a\",\"op\":\"gt\",\"value\":1}]},\"then\":{\"attentionClass\":\"standard\",\"channels\":[\"slack\"]}}");
        assertEquals(201, good.statusCode(), good.body());
        assertTrue(good.body().contains("payloadMatch") && good.body().contains("slack"), good.body());
        assertEquals(400, call("POST", base + "/api/rules/test", "[1]").statusCode());

        HttpResponse<String> patch = call("PATCH", base + "/api/rules", "{}");
        assertEquals(405, patch.statusCode());
        assertEquals("GET, POST", patch.headers().firstValue("Allow").orElse(""));
        assertEquals(405, call("GET", base + "/api/rules/reorder", null).statusCode());
        assertEquals(405, call("GET", base + "/api/rules/test", null).statusCode());
        assertEquals(405, call("GET", base + "/api/rules/someid", null).statusCode());
        assertEquals(404, call("GET", base + "/api/rules/a/b", null).statusCode());
        assertEquals(400, call("POST", base + "/api/rules/reorder", "{\"ids\":[1]}").statusCode());
        assertEquals(400, call("POST", base + "/api/rules/reorder", "{\"ids\":[\"a\"]}").statusCode());
        assertEquals(400, call("POST", base + "/api/rules/reorder", "[]").statusCode());
        assertEquals(400, call("POST", base + "/api/rules/test", "[]").statusCode());
    }

    @Test
    void theDryRunAndThePoliciesEndpointDescribeTheLiveRules() throws Exception {
        String base = start();
        JsonNode critical = JSON.readTree(call("POST", base + "/api/rules/test",
                "{\"source\":\"github\",\"eventType\":\"security_advisory\"}").body());
        assertTrue(critical.get("aiBypassed").asBoolean());
        assertEquals("all-critical", critical.get("matchedRule").asText());
        assertTrue(critical.get("steps").get(0).get("winner").asBoolean());

        JsonNode none = JSON.readTree(call("POST", base + "/api/rules/test", "{\"source\":\"gitlab\"}").body());
        assertTrue(!none.get("matched").asBoolean());
        assertNull(none.get("matchedRule"));

        assertEquals(401, call("GET", base + "/api/policies", null, Map.of()).statusCode());
        assertEquals(405, call("POST", base + "/api/policies", "{}").statusCode());
        JsonNode policies = JSON.readTree(call("GET", base + "/api/policies", null).body());
        assertEquals(600, policies.get("dedupWindowSeconds").asInt(), "the default window when none is set");
        assertEquals(2160, policies.get("retentionHours").asInt());
        assertEquals(2, policies.get("ruleCount").asInt());
        assertEquals("all-critical", policies.get("lockedRules").get(0).asText());
    }

    // ---- deliveries ----------------------------------------------------------------------------------------------

    @Test
    void deliveriesAreListedWithCountsAndADeadOneCanBeRetried() throws Exception {
        String base = start();
        Instant now = Instant.now();
        deliveries.enqueue(Delivery.Job.pending("e1", "slack", "standard", "t", "SECRET BODY", "github", now));
        String id = deliveries.list(1).get(0).id();
        deliveries.fail(id, "boom", null, now);

        assertEquals(401, call("GET", base + "/api/deliveries", null, Map.of()).statusCode());
        HttpResponse<String> list = call("GET", base + "/api/deliveries", null);
        assertEquals(200, list.statusCode());
        JsonNode body = JSON.readTree(list.body());
        assertEquals("dead", body.get("deliveries").get(0).get("status").asText());
        assertEquals(1, body.get("counts").get("dead").asInt());
        assertTrue(!list.body().contains("SECRET BODY"), "message text is never shown");

        assertEquals(400, call("GET", base + "/api/deliveries?limit=0", null).statusCode());
        assertEquals(400, call("GET", base + "/api/deliveries?limit=201", null).statusCode());
        assertEquals(400, call("GET", base + "/api/deliveries?limit=x", null).statusCode());
        assertEquals(405, call("POST", base + "/api/deliveries", "{}").statusCode());
        assertEquals(404, call("POST", base + "/api/deliveries/" + id + "/nope", "{}").statusCode());
        assertEquals(405, call("GET", base + "/api/deliveries/" + id + "/retry", null).statusCode());
        assertEquals(404, call("POST", base + "/api/deliveries/missing/retry", "{}").statusCode());

        HttpResponse<String> retried = call("POST", base + "/api/deliveries/" + id + "/retry", "{}");
        assertEquals(200, retried.statusCode());
        assertEquals("pending", JSON.readTree(retried.body()).get("status").asText());
        assertEquals(409, call("POST", base + "/api/deliveries/" + id + "/retry", "{}").statusCode(), "only a dead one can be retried");
    }
}
