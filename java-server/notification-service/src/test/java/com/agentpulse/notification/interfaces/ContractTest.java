package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.MemoryDeduper;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.RulesLoader;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.TestFactory;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.Iterator;
import java.util.List;
import java.util.Map;
import java.util.stream.Stream;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

/**
 * Replays the shared HTTP contract in {@code docs/contract} against this service. The Go service replays the same
 * files, so the two are held to one behaviour. Set CONTRACT_BASE_URL to replay against a running service instead
 * of the in-process one.
 */
class ContractTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Path CONTRACT_DIR = Path.of("../../docs/contract");
    private static final Path RULES = Path.of("../../go-server/notification-service/config/rules.yaml");
    private static final String KEY = "contract-key";
    private static final String SECRET = "contract-secret";

    @TestFactory
    Stream<DynamicTest> contractFiles() throws Exception {
        List<Path> files;
        try (Stream<Path> all = Files.list(CONTRACT_DIR)) {
            files = all.filter(p -> p.getFileName().toString().endsWith(".json")).sorted().toList();
        }
        assertFalse(files.isEmpty(), "no contract files in " + CONTRACT_DIR.toAbsolutePath());
        return files.stream().map(f -> DynamicTest.dynamicTest(f.getFileName().toString(), () -> run(f)));
    }

    private void run(Path file) throws Exception {
        JsonNode contract = JSON.readTree(Files.readString(file));
        String external = System.getenv("CONTRACT_BASE_URL");
        HttpServer server = null;
        String base;
        if (external == null || external.isEmpty()) {
            server = startService();
            base = "http://localhost:" + server.getAddress().getPort();
        } else {
            base = external;
        }
        try {
            replay(base, contract);
        } finally {
            if (server != null) {
                server.stop(0);
            }
        }
    }

    private HttpServer startService() throws Exception {
        // Each stored event gets a later time, so "newest first" does not depend on the machine's clock resolution.
        Clock ticking = new Clock() {
            private Instant now = Instant.parse("2026-01-01T00:00:00Z");

            @Override public ZoneOffset getZone() { return ZoneOffset.UTC; }
            @Override public Clock withZone(java.time.ZoneId zone) { return this; }
            @Override public synchronized Instant instant() { now = now.plusMillis(1); return now; }
        };
        InMemoryEventRepository repository = new InMemoryEventRepository(ticking);
        NotificationProvider logOnly = new NotificationProvider() {
            @Override public void send(Notification n) { }
            @Override public String name() { return "test"; }
        };
        Pipeline pipeline = new Pipeline(new PolicyEngine(RulesLoader.loadFile(RULES)), logOnly, repository)
                .withDeduper(new MemoryDeduper(Duration.ofMinutes(10), Clock.systemUTC()));
        Config config = Config.load(Map.of("API_KEY", KEY, "GITHUB_WEBHOOK_SECRET", SECRET));
        return new HttpApi(config, pipeline, repository).start(0);
    }

    private void replay(String base, JsonNode contract) throws Exception {
        HttpClient client = HttpClient.newHttpClient();
        Map<String, String> vars = new HashMap<>();
        for (JsonNode step : contract.get("steps")) {
            String name = step.get("name").asText();
            JsonNode req = step.get("request");
            String path = req.get("path").asText();
            for (Map.Entry<String, String> v : vars.entrySet()) {
                path = path.replace("{{" + v.getKey() + "}}", v.getValue());
            }
            byte[] body = new byte[0];
            if (req.has("rawBody")) {
                body = req.get("rawBody").asText().getBytes(StandardCharsets.UTF_8);
            } else if (req.has("body")) {
                body = JSON.writeValueAsBytes(req.get("body"));
            }
            HttpRequest.Builder builder = HttpRequest.newBuilder(URI.create(base + path))
                    .method(req.get("method").asText(), HttpRequest.BodyPublishers.ofByteArray(body));
            if (req.has("headers")) {
                for (Iterator<Map.Entry<String, JsonNode>> it = req.get("headers").fields(); it.hasNext(); ) {
                    Map.Entry<String, JsonNode> h = it.next();
                    builder.header(h.getKey(), h.getValue().asText());
                }
            }
            if (req.path("sign").asBoolean(false)) {
                builder.header("X-Hub-Signature-256", Signature.sign(SECRET, body));
            }
            HttpResponse<String> response = client.send(builder.build(), HttpResponse.BodyHandlers.ofString());
            check(name, step.get("expect"), response);
            if (step.has("save")) {
                JsonNode answer = JSON.readTree(response.body());
                for (Iterator<Map.Entry<String, JsonNode>> it = step.get("save").fields(); it.hasNext(); ) {
                    Map.Entry<String, JsonNode> s = it.next();
                    vars.put(s.getKey(), answer.get(s.getValue().asText()).asText());
                }
            }
        }
    }

    private void check(String step, JsonNode expect, HttpResponse<String> response) throws Exception {
        assertEquals(expect.get("status").asInt(), response.statusCode(), step + ": status (body " + response.body() + ")");
        boolean needsJson = expect.has("json") || expect.has("absent") || expect.has("present") || expect.has("absentInEach");
        if (!needsJson) {
            return;
        }
        JsonNode got = JSON.readTree(response.body());
        if (expect.has("json")) {
            String problem = subsetOf(expect.get("json"), got, "$");
            assertTrue(problem.isEmpty(), step + ": " + problem + " (body " + response.body() + ")");
        }
        for (JsonNode key : expect.path("absent")) {
            assertFalse(got.has(key.asText()), step + ": " + key.asText() + " must be absent");
        }
        for (JsonNode key : expect.path("present")) {
            assertTrue(got.has(key.asText()), step + ": " + key.asText() + " must be present");
        }
        if (expect.has("absentInEach")) {
            for (Iterator<Map.Entry<String, JsonNode>> it = expect.get("absentInEach").fields(); it.hasNext(); ) {
                Map.Entry<String, JsonNode> e = it.next();
                for (JsonNode item : got.path(e.getKey())) {
                    for (JsonNode key : e.getValue()) {
                        assertFalse(item.has(key.asText()), step + ": " + e.getKey() + " element must not have " + key.asText());
                    }
                }
            }
        }
    }

    /** Empty when every key written in {@code want} matches {@code got}; arrays must match element by element. */
    private static String subsetOf(JsonNode want, JsonNode got, String at) {
        if (want.isObject()) {
            if (!got.isObject()) {
                return at + ": want an object, got " + got;
            }
            for (Iterator<Map.Entry<String, JsonNode>> it = want.fields(); it.hasNext(); ) {
                Map.Entry<String, JsonNode> e = it.next();
                if (!got.has(e.getKey())) {
                    return at + "." + e.getKey() + ": missing";
                }
                String problem = subsetOf(e.getValue(), got.get(e.getKey()), at + "." + e.getKey());
                if (!problem.isEmpty()) {
                    return problem;
                }
            }
            return "";
        }
        if (want.isArray()) {
            if (!got.isArray() || got.size() != want.size()) {
                return at + ": want " + want.size() + " elements, got " + got;
            }
            List<String> problems = new ArrayList<>();
            for (int i = 0; i < want.size(); i++) {
                String problem = subsetOf(want.get(i), got.get(i), at + "[" + i + "]");
                if (!problem.isEmpty()) {
                    problems.add(problem);
                }
            }
            return problems.isEmpty() ? "" : problems.get(0);
        }
        return want.equals(got) ? "" : at + ": want " + want + ", got " + got;
    }

    static {
        if (!Files.isDirectory(CONTRACT_DIR)) {
            fail("contract directory not found: " + CONTRACT_DIR.toAbsolutePath());
        }
    }
}
