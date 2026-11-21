package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Telemetry;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.http.HttpClient;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The exporter's JSON (the same strings the Go test expects), batching, retries, buffer, shutdown and settings. */
class OtlpExporterTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private HttpServer server;

    @AfterEach
    void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    /** A fake collector answering the given statuses in turn and recording what it got. */
    private final class Collector {
        final List<String> bodies = new ArrayList<>();
        final List<String> paths = new ArrayList<>();
        final List<String> contentTypes = new ArrayList<>();
        final List<String> keys = new ArrayList<>();
        final AtomicInteger calls = new AtomicInteger();

        String start(int... statuses) throws IOException {
            server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
            server.createContext("/", x -> {
                int n = calls.getAndIncrement();
                synchronized (this) {
                    bodies.add(new String(x.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
                    paths.add(x.getRequestURI().getPath());
                    contentTypes.add(x.getRequestHeaders().getFirst("Content-Type"));
                    keys.add(x.getRequestHeaders().getFirst("X-Collector-Key"));
                }
                x.sendResponseHeaders(n < statuses.length ? statuses[n] : 200, -1);
                x.close();
            });
            server.start();
            return "http://127.0.0.1:" + server.getAddress().getPort();
        }
    }

    private static OtlpExporter.Settings settings(String endpoint, int batch, int buffer) {
        return new OtlpExporter.Settings(endpoint, Map.of("X-Collector-Key", "s3cret-key"), "svc", batch, buffer);
    }

    private static OtlpExporter exporter(String endpoint, int batch, int buffer, List<Duration> slept) {
        return new OtlpExporter(settings(endpoint, batch, buffer), "0.39.0", HttpClient.newHttpClient(), Duration.ofMillis(20), slept::add);
    }

    private static Telemetry.SpanData span(String name, String parent, boolean failed) {
        return new Telemetry.SpanData("ab010000000000000000000000000000", "cd02000000000000", parent, name, Telemetry.Kind.SERVER,
                Instant.ofEpochSecond(1700000000, 123), Instant.ofEpochSecond(1700000001, 456),
                List.of(new Telemetry.Attribute(Telemetry.ATTR_HTTP_ROUTE, "/events", 0, false),
                        new Telemetry.Attribute(Telemetry.ATTR_HTTP_STATUS_CODE, "200", 200, true)), failed);
    }

    @Test
    void theJsonIsTheShapeGoWrites() throws Exception {
        String body = JSON.writeValueAsString(OtlpExporter.encode(
                List.of(span("GET /events", "ef03000000000000", true), span("pipeline.Handle", "", false)), "svc", "0.39.0"));
        for (String want : List.of(
                "\"traceId\":\"ab010000000000000000000000000000\"", "\"spanId\":\"cd02000000000000\"", "\"parentSpanId\":\"ef03000000000000\"",
                "\"startTimeUnixNano\":\"1700000000000000123\"", "\"endTimeUnixNano\":\"1700000001000000456\"", "\"kind\":2",
                "{\"key\":\"http.route\",\"value\":{\"stringValue\":\"/events\"}}", "{\"key\":\"http.response.status_code\",\"value\":{\"intValue\":\"200\"}}",
                "\"code\":2", "\"message\":\"failed\"", "{\"key\":\"service.name\",\"value\":{\"stringValue\":\"svc\"}}",
                "{\"key\":\"service.version\",\"value\":{\"stringValue\":\"0.39.0\"}}", "\"scope\":{\"name\":\"agentpulse\"}")) {
            String w = want;
            if (!body.contains(w)) {
                // Map.of does not keep key order; compare the parsed form for the small objects.
                assertTrue(JSON.readTree(body).toString().contains(w) || containsReordered(body, w), "missing " + w + " in " + body);
            }
        }
        assertEquals(1, body.split("parentSpanId", -1).length - 1, "a root span has no parentSpanId");
        assertEquals(1, body.split("\"status\"", -1).length - 1, "only a failed span has a status");
        assertFalse(JSON.writeValueAsString(OtlpExporter.encode(List.of(span("x", "", false)), "svc", "")).contains("service.version"));
        assertEquals("-1000000000", OtlpExporter.nanos(Instant.ofEpochSecond(-1)));
    }

    private static boolean containsReordered(String body, String want) throws Exception {
        if (!want.startsWith("{")) {
            return false;
        }
        String target = JSON.readTree(want).toString();
        for (com.fasterxml.jackson.databind.JsonNode n : JSON.readTree(body).findParents("key")) {
            if (JSON.readTree(n.toString()).equals(JSON.readTree(target))) {
                return true;
            }
        }
        return false;
    }

    @Test
    void spansAreBatchedAndSentWithHeadersAndContentType() throws Exception {
        Collector c = new Collector();
        String url = c.start();
        OtlpExporter e = exporter(url + "/", 2, 10, new ArrayList<>());
        e.start();
        for (int i = 0; i < 3; i++) {
            e.export(span("s", "", false));
        }
        e.shutdown(Duration.ofSeconds(5));
        assertArrayEquals(new long[] {3, 0, 0}, e.stats());
        assertEquals(2, c.bodies.size(), "3 spans in batches of 2 make 2 requests");
        assertEquals("/v1/traces", c.paths.get(0));
        assertEquals("application/json", c.contentTypes.get(0));
        assertEquals("s3cret-key", c.keys.get(0));
    }

    @Test
    void onlyNetworkErrorsTooManyRequestsAndServerErrorsAreRetriedOnce() throws Exception {
        int[][] cases = {{503, 200}, {429, 200}, {500, 502}, {400, 200}, {401, 200}};
        int[] calls = {2, 2, 2, 1, 1};
        long[][] stats = {{1, 0, 0}, {1, 0, 0}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}};
        for (int i = 0; i < cases.length; i++) {
            Collector c = new Collector();
            String url = c.start(cases[i]);
            OtlpExporter e = exporter(url, 1, 4, new ArrayList<>());
            e.send(List.of(span("s", "", false)));
            assertEquals(calls[i], c.calls.get(), "calls for " + cases[i][0]);
            assertArrayEquals(stats[i], e.stats(), "stats for " + cases[i][0]);
            server.stop(0);
            server = null;
        }
        int closedPort;
        try (ServerSocket s = new ServerSocket(0)) {
            closedPort = s.getLocalPort();
        }
        List<Duration> slept = new ArrayList<>();
        OtlpExporter e = exporter("http://127.0.0.1:" + closedPort, 1, 4, slept);
        e.send(List.of(span("s", "", false), span("t", "", false)));
        assertArrayEquals(new long[] {0, 0, 2}, e.stats(), "a network error is retried once, then failed");
        assertEquals(List.of(OtlpExporter.RETRY_DELAY), slept);
        assertTrue(OtlpExporter.retryable(0, true) && OtlpExporter.retryable(504, false) && !OtlpExporter.retryable(404, false));
    }

    @Test
    void exportNeverBlocksAndAFullBufferDropsAndCounts() {
        OtlpExporter e = exporter("http://127.0.0.1:1", 1, 2, new ArrayList<>()); // never started: nothing drains it
        long start = System.nanoTime();
        for (int i = 0; i < 10; i++) {
            e.export(span("s", "", false));
        }
        assertTrue(System.nanoTime() - start < 100_000_000L, "export must not block");
        assertEquals(8, e.stats()[1]);
        e.shutdown(Duration.ofMillis(10)); // never started: returns at once
    }

    @Test
    void aHangingCollectorNeverHoldsUpExportOrShutdown() throws Exception {
        CountDownLatch release = new CountDownLatch(1);
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/", x -> {
            try {
                release.await();
            } catch (InterruptedException ignored) {
                Thread.currentThread().interrupt();
            }
            x.close();
        });
        server.setExecutor(java.util.concurrent.Executors.newCachedThreadPool());
        server.start();
        OtlpExporter e = exporter("http://127.0.0.1:" + server.getAddress().getPort(), 1, 10, new ArrayList<>());
        e.start();
        long start = System.nanoTime();
        for (int i = 0; i < 5; i++) {
            e.export(span("s", "", false));
        }
        assertTrue(System.nanoTime() - start < 50_000_000L, "export waited for the collector");
        start = System.nanoTime();
        e.shutdown(Duration.ofMillis(100));
        assertTrue(System.nanoTime() - start < 1_000_000_000L, "shutdown gives up at its deadline");
        release.countDown();
    }

    @Test
    void settingsAreReadAsGoReadsThem() {
        OtlpExporter.Settings off = OtlpExporter.Settings.load(Map.of());
        assertFalse(off.enabled());
        assertEquals("notification-service-java", off.serviceName());
        assertEquals(OtlpExporter.DEFAULT_BATCH, off.batch());
        assertEquals(OtlpExporter.DEFAULT_BUFFER, off.buffer());
        assertThrows(IllegalArgumentException.class, () -> new OtlpExporter(off, ""));

        OtlpExporter.Settings on = OtlpExporter.Settings.load(Map.of("OTEL_EXPORTER_OTLP_ENDPOINT", " http://jaeger:4318/ ",
                "OTEL_EXPORTER_OTLP_HEADERS", "x-api-key=a%2Cb%3Dc, X-Team = ops ", "OTEL_SERVICE_NAME", "agentpulse-java",
                "OTEL_EXPORT_BATCH", "10", "OTEL_EXPORT_BUFFER", "50"));
        assertEquals("http://jaeger:4318", on.endpoint());
        assertEquals(Map.of("x-api-key", "a,b=c", "X-Team", "ops"), on.headers());
        assertEquals("agentpulse-java", on.serviceName());
        assertEquals(10, on.batch());
        assertEquals(50, on.buffer());

        for (Map<String, String> bad : List.of(Map.of("OTEL_EXPORTER_OTLP_ENDPOINT", "http://user:pw@jaeger:4318"),
                Map.of("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4318"), Map.of("OTEL_EXPORTER_OTLP_ENDPOINT", "http://h:4318?x=1"),
                Map.of("OTEL_EXPORTER_OTLP_ENDPOINT", "http://h#f"), Map.of("OTEL_EXPORTER_OTLP_ENDPOINT", "ftp://h"),
                Map.of("OTEL_EXPORTER_OTLP_ENDPOINT", "http://bad host"), Map.of("OTEL_EXPORT_BATCH", "0"),
                Map.of("OTEL_EXPORT_BUFFER", "100001"), Map.of("OTEL_EXPORT_BATCH", "x"), Map.of("OTEL_EXPORT_BATCH", "60", "OTEL_EXPORT_BUFFER", "50"))) {
            assertThrows(Config.InvalidConfigException.class, () -> OtlpExporter.Settings.load(bad), bad.toString());
        }
        Config.InvalidConfigException e = assertThrows(Config.InvalidConfigException.class,
                () -> OtlpExporter.Settings.load(Map.of("OTEL_EXPORTER_OTLP_HEADERS", "good=1,sup3r-s3cret-no-equals")));
        assertTrue(e.getMessage().contains("entry 2") && !e.getMessage().contains("sup3r"), e.getMessage());
        assertThrows(Config.InvalidConfigException.class, () -> OtlpExporter.Settings.load(Map.of("OTEL_EXPORTER_OTLP_HEADERS", "k=%zz")));
    }
}
