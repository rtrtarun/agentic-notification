package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Telemetry;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.URI;
import java.net.URISyntaxException;
import java.net.URLDecoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import java.util.function.Consumer;

/**
 * Sends finished spans to an OpenTelemetry collector as OTLP/HTTP JSON, as the Go service's {@code OTLPExporter}
 * does, with the same JSON and the same rules.
 *
 * <p>{@link #export} never blocks: a span goes into a bounded buffer and is dropped and counted when the buffer is
 * full. A daemon thread sends batches on an interval or when a batch fills. A request is retried once, and only after
 * a network error, 429 or a 5xx; any other answer counts the batch as failed. Nothing here can slow or fail the request
 * that ended the span. Headers are never logged.
 */
public final class OtlpExporter implements Telemetry.Exporter {
    public static final int DEFAULT_BATCH = 100;
    public static final int DEFAULT_BUFFER = 2048;
    public static final int MAX_BATCH = 1000;
    public static final int MAX_BUFFER = 100000;
    static final Duration INTERVAL = Duration.ofSeconds(2);
    static final Duration TIMEOUT = Duration.ofSeconds(5);
    static final Duration RETRY_DELAY = Duration.ofSeconds(1);
    /** How often the worker looks at the stop flag while waiting for spans. */
    static final Duration POLL_SLICE = Duration.ofMillis(50);
    private static final String TRACES_PATH = "/v1/traces";
    private static final String SCOPE = "agentpulse";
    private static final int STATUS_ERROR = 2;
    private static final Logger LOG = LoggerFactory.getLogger(OtlpExporter.class);
    private static final ObjectMapper JSON = new ObjectMapper();

    /** The span export settings, read from the same names as Go; nothing is exported without an endpoint. */
    public record Settings(String endpoint, Map<String, String> headers, String serviceName, int batch, int buffer) {
        /** Whether span export is on. */
        public boolean enabled() {
            return !endpoint.isEmpty();
        }

        /** Reads OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS, OTEL_SERVICE_NAME, OTEL_EXPORT_BATCH, OTEL_EXPORT_BUFFER. */
        public static Settings load(Map<String, String> env) {
            String raw = env.getOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", "").trim();
            String endpoint = "";
            if (!raw.isEmpty()) {
                try {
                    endpoint = parseEndpoint(raw);
                } catch (IllegalArgumentException e) {
                    throw new Config.InvalidConfigException("OTEL_EXPORTER_OTLP_ENDPOINT " + e.getMessage());
                }
            }
            Map<String, String> headers = parseHeaders(env.getOrDefault("OTEL_EXPORTER_OTLP_HEADERS", ""));
            String name = env.getOrDefault("OTEL_SERVICE_NAME", "").trim();
            int buffer = whole(env, "OTEL_EXPORT_BUFFER", DEFAULT_BUFFER, MAX_BUFFER);
            int batch = whole(env, "OTEL_EXPORT_BATCH", DEFAULT_BATCH, MAX_BATCH);
            if (batch > buffer) {
                throw new Config.InvalidConfigException("OTEL_EXPORT_BATCH must not be larger than OTEL_EXPORT_BUFFER");
            }
            return new Settings(endpoint, headers, name.isEmpty() ? "notification-service-java" : name, batch, buffer);
        }

        private static int whole(Map<String, String> env, String name, int fallback, int max) {
            String v = env.getOrDefault(name, "").trim();
            if (v.isEmpty()) {
                return fallback;
            }
            try {
                int n = Integer.parseInt(v);
                if (n >= 1 && n <= max) {
                    return n;
                }
            } catch (NumberFormatException ignored) {
                // reported below
            }
            throw new Config.InvalidConfigException(name + " must be a whole number from 1 to " + max);
        }
    }

    /**
     * Checks a collector base URL: http or https, a host, and no user information, query or fragment (credentials go
     * in the headers, which are never logged). Returns it without a trailing slash.
     */
    static String parseEndpoint(String raw) {
        URI u;
        try {
            u = new URI(raw.trim());
        } catch (URISyntaxException e) {
            throw new IllegalArgumentException("must be an http or https URL such as http://localhost:4318");
        }
        if (!("http".equals(u.getScheme()) || "https".equals(u.getScheme())) || u.getHost() == null) {
            throw new IllegalArgumentException("must be an http or https URL such as http://localhost:4318");
        }
        if (u.getRawUserInfo() != null || u.getRawQuery() != null || u.getRawFragment() != null) {
            throw new IllegalArgumentException("must not contain a user, password, query or fragment; put credentials in OTEL_EXPORTER_OTLP_HEADERS");
        }
        String s = u.toString();
        while (s.endsWith("/")) {
            s = s.substring(0, s.length() - 1);
        }
        return s;
    }

    /**
     * Reads "name=value,name2=value2" with URL-encoded values, as the OpenTelemetry specification defines. A mistake is
     * reported by position only, because the value may be a credential.
     */
    static Map<String, String> parseHeaders(String raw) {
        Map<String, String> out = new LinkedHashMap<>();
        int position = 0;
        for (String pair : raw.split(",")) {
            if (pair.trim().isEmpty()) {
                continue;
            }
            position++;
            int eq = pair.indexOf('=');
            String name = eq < 0 ? "" : pair.substring(0, eq).trim();
            try {
                if (name.isEmpty()) {
                    throw new IllegalArgumentException();
                }
                out.put(name, URLDecoder.decode(pair.substring(eq + 1).trim(), StandardCharsets.UTF_8));
            } catch (IllegalArgumentException e) {
                throw new Config.InvalidConfigException("OTEL_EXPORTER_OTLP_HEADERS: entry " + position + " must be name=value");
            }
        }
        return out;
    }

    private final URI url;
    private final Map<String, String> headers;
    private final String serviceName;
    private final String serviceVersion;
    private final int batchSize;
    private final BlockingQueue<Telemetry.SpanData> queue;
    private final HttpClient client;
    private final Duration interval;
    private final Consumer<Duration> sleep;
    private final AtomicLong sent = new AtomicLong();
    private final AtomicLong dropped = new AtomicLong();
    private final AtomicLong failed = new AtomicLong();
    private final CountDownLatch done = new CountDownLatch(1);
    private volatile boolean stopping;
    private Thread worker;

    /** An exporter for {@code settings}; call {@link #start} to begin sending. */
    public OtlpExporter(Settings settings, String serviceVersion) {
        this(settings, serviceVersion, HttpClient.newBuilder().connectTimeout(TIMEOUT).build(), INTERVAL, OtlpExporter::pause);
    }

    OtlpExporter(Settings settings, String serviceVersion, HttpClient client, Duration interval, Consumer<Duration> sleep) {
        if (!settings.enabled()) {
            throw new IllegalArgumentException("span export needs an endpoint");
        }
        String base = settings.endpoint();
        while (base.endsWith("/")) {
            base = base.substring(0, base.length() - 1); // "//v1/traces" is a bad request to many servers
        }
        this.url = URI.create(base + TRACES_PATH);
        this.headers = Map.copyOf(settings.headers());
        this.serviceName = settings.serviceName();
        this.serviceVersion = serviceVersion == null ? "" : serviceVersion;
        this.batchSize = settings.batch();
        this.queue = new ArrayBlockingQueue<>(settings.buffer());
        this.client = client;
        this.interval = interval;
        this.sleep = sleep;
    }

    /** Queues a span, or drops and counts it when the buffer is full. Never blocks. */
    @Override
    public void export(Telemetry.SpanData span) {
        if (!queue.offer(span)) {
            dropped.incrementAndGet();
        }
    }

    /** Starts the daemon thread that sends batches. */
    public synchronized void start() {
        if (worker == null) {
            worker = new Thread(this::run, "otlp-exporter");
            worker.setDaemon(true);
            worker.start();
        }
    }

    /** Stops sending after one last flush, waiting at most {@code wait}, and logs the counts. */
    public void shutdown(Duration wait) {
        stopping = true; // the worker checks this at least every POLL_SLICE; it is never interrupted mid-send
        Thread w;
        synchronized (this) {
            w = worker;
        }
        if (w != null) {
            try {
                done.await(wait.toMillis(), TimeUnit.MILLISECONDS);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
        }
        LOG.info("span export stopped sent={} dropped={} failed={}", sent.get(), dropped.get(), failed.get());
    }

    /** How many spans were sent, dropped because the buffer was full, and failed: {sent, dropped, failed}. */
    public long[] stats() {
        return new long[] {sent.get(), dropped.get(), failed.get()};
    }

    private void run() {
        List<Telemetry.SpanData> batch = new ArrayList<>(batchSize);
        long deadline = System.nanoTime() + interval.toNanos();
        try {
            while (!stopping) {
                long left = Math.min(deadline - System.nanoTime(), POLL_SLICE.toNanos());
                Telemetry.SpanData span = null;
                try {
                    span = left > 0 ? queue.poll(left, TimeUnit.NANOSECONDS) : null;
                } catch (InterruptedException e) {
                    return; // only an outside interrupt lands here; the process is going away
                }
                if (span != null) {
                    batch.add(span);
                    if (batch.size() == batchSize) {
                        send(batch);
                        batch = new ArrayList<>(batchSize);
                    }
                }
                if (System.nanoTime() >= deadline) {
                    if (!batch.isEmpty()) {
                        send(batch);
                        batch = new ArrayList<>(batchSize);
                    }
                    deadline = System.nanoTime() + interval.toNanos();
                }
            }
            Telemetry.SpanData span;
            while ((span = queue.poll()) != null) {
                batch.add(span);
                if (batch.size() == batchSize) {
                    send(batch);
                    batch = new ArrayList<>(batchSize);
                }
            }
            if (!batch.isEmpty()) {
                send(batch);
            }
        } finally {
            done.countDown();
        }
    }

    /** Posts one batch, retrying once after a network error, 429 or 5xx. */
    void send(List<Telemetry.SpanData> batch) {
        byte[] body;
        try {
            body = JSON.writeValueAsBytes(encode(batch, serviceName, serviceVersion));
        } catch (JsonProcessingException e) {
            failed.addAndGet(batch.size());
            return;
        }
        for (int attempt = 0; attempt < 2; attempt++) {
            int status = 0;
            boolean networkError = false;
            try {
                status = post(body);
            } catch (IOException e) {
                networkError = true;
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                networkError = true;
            }
            if (!networkError && status >= 200 && status < 300) {
                sent.addAndGet(batch.size());
                return;
            }
            if (attempt == 0 && retryable(status, networkError)) {
                sleep.accept(RETRY_DELAY);
                continue;
            }
            failed.addAndGet(batch.size());
            // The status and the kind of failure only: never the headers, which can hold credentials.
            LOG.warn("span export failed spans={} status={} network_error={}", batch.size(), status, networkError);
            return;
        }
    }

    static boolean retryable(int status, boolean networkError) {
        return networkError || status == 429 || status >= 500;
    }

    private int post(byte[] body) throws IOException, InterruptedException {
        HttpRequest.Builder b = HttpRequest.newBuilder(url).timeout(TIMEOUT)
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofByteArray(body));
        headers.forEach(b::header);
        return client.send(b.build(), HttpResponse.BodyHandlers.discarding()).statusCode();
    }

    /**
     * The OTLP/HTTP JSON body, the same shape Go writes: ids as lower-case hex, times as nanoseconds since the epoch
     * written as strings, kinds and status codes as numbers, integer attributes as strings.
     */
    public static Map<String, Object> encode(List<Telemetry.SpanData> spans, String serviceName, String serviceVersion) {
        List<Map<String, Object>> resource = new ArrayList<>();
        resource.add(stringAttr("service.name", serviceName));
        if (serviceVersion != null && !serviceVersion.isEmpty()) {
            resource.add(stringAttr("service.version", serviceVersion));
        }
        List<Map<String, Object>> out = new ArrayList<>();
        for (Telemetry.SpanData s : spans) {
            Map<String, Object> span = new LinkedHashMap<>();
            span.put("traceId", s.traceId());
            span.put("spanId", s.spanId());
            if (!s.parentSpanId().isEmpty()) {
                span.put("parentSpanId", s.parentSpanId());
            }
            span.put("name", s.name());
            span.put("kind", s.kind().number);
            span.put("startTimeUnixNano", nanos(s.start()));
            span.put("endTimeUnixNano", nanos(s.end()));
            List<Map<String, Object>> attrs = new ArrayList<>();
            for (Telemetry.Attribute a : s.attributes()) {
                attrs.add(a.isInt()
                        ? ordered("key", a.key(), "value", ordered("intValue", Long.toString(a.intValue())))
                        : stringAttr(a.key(), a.value()));
            }
            span.put("attributes", attrs);
            if (s.failed()) {
                span.put("status", ordered("code", STATUS_ERROR, "message", Telemetry.FAILED_STATUS_MESSAGE));
            }
            out.add(span);
        }
        Map<String, Object> scopeSpans = new LinkedHashMap<>();
        scopeSpans.put("scope", ordered("name", SCOPE));
        scopeSpans.put("spans", out);
        Map<String, Object> resourceSpans = new LinkedHashMap<>();
        resourceSpans.put("resource", ordered("attributes", resource));
        resourceSpans.put("scopeSpans", List.of(scopeSpans));
        return ordered("resourceSpans", List.of(resourceSpans));
    }

    static String nanos(Instant t) {
        return Long.toString(Math.addExact(Math.multiplyExact(t.getEpochSecond(), 1_000_000_000L), t.getNano()));
    }

    private static Map<String, Object> stringAttr(String key, String value) {
        return ordered("key", key, "value", ordered("stringValue", value));
    }

    /** A map that keeps the order written, so the same spans are always the same bytes. */
    private static Map<String, Object> ordered(Object... pairs) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < pairs.length; i += 2) {
            m.put((String) pairs[i], pairs[i + 1]);
        }
        return m;
    }

    private static void pause(Duration d) {
        try {
            Thread.sleep(d.toMillis());
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }
}
