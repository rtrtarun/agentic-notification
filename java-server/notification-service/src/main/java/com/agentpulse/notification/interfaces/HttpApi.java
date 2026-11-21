package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Digests;
import com.agentpulse.notification.application.Feedback;
import com.agentpulse.notification.application.Heuristic;
import com.agentpulse.notification.application.Learning;
import com.agentpulse.notification.application.Tuning;
import com.agentpulse.notification.application.GitHubNormalizer;
import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Overrides;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.Ports.DeliveryStore;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.application.Telemetry;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.infrastructure.Config;
import com.fasterxml.jackson.core.type.TypeReference;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.time.Clock;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executors;

/**
 * The HTTP interface: it decodes a request, calls one pipeline, service or repository method and encodes the
 * answer. No business decision is made here. Paths, status codes and bodies match the Go service, which the shared
 * contract tests in {@code docs/contract} check.
 */
public final class HttpApi {
    private static final Logger LOG = LoggerFactory.getLogger(HttpApi.class);
    private static final TypeReference<Map<String, Object>> MAP = new TypeReference<>() {};

    static final int DEFAULT_EVENTS_LIMIT = 100;
    static final int MAX_EVENTS_LIMIT = 500;
    private static final int LIST_WINDOW = 1000;

    private final Config config;
    private final Pipeline pipeline;
    private final EventRepository events;
    private final ApiSupport api;
    private final NotifyApi notify;
    private final RulesApi rules;
    private final DeliveriesApi deliveries;
    private final AuthApi auth;
    private final FeedbackApi feedbackApi;
    private final AgentApi agent;
    private final McpApi mcp;
    private volatile AttentionApi attention;
    private volatile LearningApi learning;
    private volatile TuningApi tuning;
    private volatile Telemetry.Tracer tracer;

    public HttpApi(Config config, Pipeline pipeline, EventRepository events, Notify.Service notifyService,
                   RulesService rulesService, DeliveryStore deliveryStore, Clock clock) {
        this(config, pipeline, events, notifyService, rulesService, deliveryStore, clock, null);
    }

    /** With accounts, overrides and feedback as well. Either of the last two may be null. */
    public HttpApi(Config config, Pipeline pipeline, EventRepository events, Notify.Service notifyService,
                   RulesService rulesService, DeliveryStore deliveryStore, Clock clock, Accounts.Service accounts,
                   Overrides.Service overrides, Feedback.Service feedback) {
        this.config = config;
        this.pipeline = pipeline;
        this.events = events;
        this.api = new ApiSupport(config).withAccounts(accounts);
        this.auth = accounts == null ? null : new AuthApi(api, accounts);
        this.notify = new NotifyApi(api, notifyService);
        this.rules = new RulesApi(api, rulesService);
        this.deliveries = new DeliveriesApi(api, deliveryStore, clock);
        this.feedbackApi = new FeedbackApi(api, feedback, overrides, accounts);
        this.tracer = new Telemetry.Tracer(clock);
        this.agent = new AgentApi(api, pipeline, events, this::listEvents);
        this.mcp = new McpApi(api, new McpApi.InProcess(pipeline, events, notifyService, new Heuristic()::explain, HttpApi::summary));
    }

    /** With an accounts service, people can sign up, sign in and use the API with a session as well as the API key. */
    public HttpApi(Config config, Pipeline pipeline, EventRepository events, Notify.Service notifyService,
                   RulesService rulesService, DeliveryStore deliveryStore, Clock clock, Accounts.Service accounts) {
        this.config = config;
        this.pipeline = pipeline;
        this.events = events;
        this.api = new ApiSupport(config).withAccounts(accounts);
        this.auth = accounts == null ? null : new AuthApi(api, accounts);
        this.notify = new NotifyApi(api, notifyService);
        this.rules = new RulesApi(api, rulesService);
        this.deliveries = new DeliveriesApi(api, deliveryStore, clock);
        this.feedbackApi = new FeedbackApi(api, null, null, accounts);
        this.tracer = new Telemetry.Tracer(clock);
        this.agent = new AgentApi(api, pipeline, events, this::listEvents);
        this.mcp = new McpApi(api, new McpApi.InProcess(pipeline, events, notifyService, new Heuristic()::explain, HttpApi::summary));
    }

    /**
     * Turns on digests, explanations and the attention status. {@code lookup} finds an event by id; {@code every}
     * is the schedule shown with the digests (zero means only on request).
     */
    public HttpApi withAttention(Digests.Generator generator, Digests.Store store, java.util.function.Function<String, java.util.Optional<Event>> lookup,
                                 java.time.Duration every, Accounts.Service accounts) {
        this.attention = new AttentionApi(api, generator, store, new Heuristic(), lookup, every, accounts);
        return this;
    }

    /** Turns on the learning suggestions API. */
    public HttpApi withLearning(Learning.Service service, Accounts.Service accounts) {
        this.learning = new LearningApi(api, service, accounts);
        return this;
    }

    /** Turns on offline score tuning: the adjustments and runs APIs. {@code trainEvery} is the schedule shown with the runs. */
    public HttpApi withTuning(Tuning.Tuner tuner, Accounts.Service accounts, java.time.Duration trainEvery) {
        this.tuning = new TuningApi(api, tuner, accounts, trainEvery);
        return this;
    }

    /**
     * Uses this tracer for request spans (normally one with an exporter; see {@code OtlpExporter}). Without it, request
     * spans are only logged.
     */
    public HttpApi withTracer(Telemetry.Tracer tracer) {
        this.tracer = tracer;
        return this;
    }

    /** Starts serving on the given port (0 picks a free one) and returns the running server. */
    public HttpServer start(int port) throws IOException {
        HttpServer server = HttpServer.create(new InetSocketAddress(port), 0);
        server.createContext("/", this::serve);
        server.setExecutor(Executors.newFixedThreadPool(16));
        server.start();
        return server;
    }

    private void route(HttpExchange x, Telemetry.SpanContext span) throws IOException {
        String path = x.getRequestURI().getPath();
        if (under(path, RulesApi.PREFIX)) {
            rules.rules(x);
        } else if (under(path, DeliveriesApi.PREFIX)) {
            deliveries.handle(x);
        } else if (under(path, AgentApi.TRACES_PREFIX)) {
            agent.traces(x);
        } else if (under(path, FeedbackApi.OVERRIDES_PREFIX)) {
            feedbackApi.overrides(x);
        } else if (learning != null && under(path, LearningApi.PREFIX)) {
            learning.handle(x);
        } else if (tuning != null && under(path, TuningApi.ADJUSTMENTS_PREFIX)) {
            tuning.adjustments(x);
        } else if (tuning != null && under(path, TuningApi.RUNS_PREFIX)) {
            tuning.runs(x);
        } else if (attention != null && under(path, AttentionApi.DIGESTS_PREFIX)) {
            attention.digests(x);
        } else if (attention != null && under(path, AttentionApi.EXPLAIN_PREFIX)) {
            attention.explain(x);
        } else if (auth != null && (under(path, AuthApi.AUTH_PREFIX) || under(path, AuthApi.USERS_PREFIX))) {
            auth.handle(x);
        } else {
            switch (path) {
                case "/health" -> api.reply(x, 200, Map.of("status", "ok", "service", "attention-orchestration", "version", config.appVersion()));
                case "/webhooks/github" -> webhook(x, span);
                case "/events" -> listEvents(x);
                case AgentApi.EVENTS_PATH -> agent.events(x, span);
                case McpApi.PATH -> mcp.handle(x, span);
                case "/notify" -> notify.handle(x);
                case FeedbackApi.FEEDBACK_PATH -> feedbackApi.feedback(x);
                case AttentionApi.STATUS_PATH -> {
                    if (attention != null) {
                        attention.status(x);
                    } else {
                        api.error(x, 404, "not found");
                    }
                }
                case "/api/policies" -> rules.policies(x, config);
                default -> api.error(x, 404, "not found");
            }
        }
    }

    private static boolean under(String path, String prefix) {
        return path.equals(prefix) || path.startsWith(prefix + "/");
    }

    // ---- webhook -------------------------------------------------------------------------------------------

    private void webhook(HttpExchange x, Telemetry.SpanContext span) throws IOException {
        String secret = config.githubWebhookSecret();
        if (secret.isEmpty() && !config.allowUnsignedWebhooks()) {
            api.error(x, 503, "webhook secret not configured");
            return;
        }
        byte[] body = api.readBody(x);
        if (body == null) {
            return;
        }
        if (!secret.isEmpty() && !Signature.valid(secret, body, x.getRequestHeaders().getFirst("X-Hub-Signature-256"))) {
            LOG.warn("rejected webhook with invalid signature remote={}", x.getRemoteAddress());
            api.error(x, 401, "invalid signature");
            return;
        }
        if (!"POST".equals(x.getRequestMethod())) {
            api.error(x, 405, "method not allowed");
            return;
        }
        String eventType = x.getRequestHeaders().getFirst("X-GitHub-Event");
        if (!GitHubNormalizer.validEventType(eventType)) {
            api.error(x, 400, "missing or invalid github event header");
            return;
        }
        Map<String, Object> payload;
        try {
            payload = ApiSupport.JSON.readValue(body, MAP);
        } catch (IOException e) {
            api.error(x, 400, "invalid request payload");
            return;
        }

        Event normalized = GitHubNormalizer.normalize(eventType, x.getRequestHeaders().getFirst("X-GitHub-Delivery"), payload);
        Event result;
        try {
            result = pipeline.handle(normalized, span);
        } catch (Pipeline.DuplicateDeliveryException e) {
            LOG.info("ignored redelivered event event_id={}", normalized.getId());
            api.reply(x, 200, Map.of("status", "duplicate", "eventId", normalized.getId()));
            return;
        } catch (RuntimeException e) {
            LOG.error("pipeline failed event_id={} event_type={} error={}", normalized.getId(), eventType, e.getMessage());
            api.error(x, 500, "internal error");
            return;
        }
        LOG.info("event processed event_id={} thread_id={} attention_class={} decision_status={}",
                result.getId(), result.getThreadId(), result.getAttentionClass(), result.getDecisionState().getStatus());
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("status", "accepted");
        out.put("eventId", result.getId());
        out.put("attentionClass", result.getAttentionClass());
        out.put("decisionState", result.getDecisionState());
        api.reply(x, 202, out);
    }

    // ---- events --------------------------------------------------------------------------------------------

    private void listEvents(HttpExchange x) throws IOException {
        if (!api.authorised(x)) {
            return;
        }
        if (!"GET".equals(x.getRequestMethod())) {
            api.error(x, 405, "method not allowed");
            return;
        }
        Map<String, String> query = Query.parse(x.getRequestURI().getRawQuery());

        int limit = DEFAULT_EVENTS_LIMIT;
        if (query.containsKey("limit")) {
            try {
                limit = Integer.parseInt(query.get("limit"));
            } catch (NumberFormatException e) {
                limit = 0;
            }
            if (limit < 1 || limit > MAX_EVENTS_LIMIT) {
                api.error(x, 400, "limit must be a whole number from 1 to " + MAX_EVENTS_LIMIT);
                return;
            }
        }
        String traceId = query.getOrDefault("traceId", "");
        Cursor cursor = null;
        if (query.containsKey("cursor")) {
            cursor = Cursor.decode(query.get("cursor"));
            if (cursor == null) {
                api.error(x, 400, "cursor is not valid; use the nextCursor from a previous page");
                return;
            }
        }

        List<Event> stored;
        try {
            stored = new ArrayList<>(events.listRecent(LIST_WINDOW));
        } catch (RuntimeException e) {
            LOG.error("list events failed error={}", e.getMessage());
            api.error(x, 500, "internal error");
            return;
        }
        // Newest first, ties broken by id, so a cursor names one position exactly.
        stored.sort(Comparator.comparing(Event::getCreatedAt).thenComparing(Event::getId).reversed());

        List<Map<String, Object>> page = new ArrayList<>();
        boolean more = false;
        Event last = null;
        for (Event e : stored) {
            if (!traceId.isEmpty() && !traceId.equals(e.getTraceId())) {
                continue;
            }
            if (cursor != null && !cursor.isAfter(e)) {
                continue;
            }
            if (page.size() == limit) {
                more = true;
                break;
            }
            page.add(summary(e));
            last = e;
        }
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("events", page);
        if (more) {
            out.put("nextCursor", Cursor.of(last).encode());
        }
        api.reply(x, 200, out);
    }

    /** The read model for the list (and MCP's get_recent_events): the payload is left out because it can carry names and emails. */
    static Map<String, Object> summary(Event e) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", e.getId());
        m.put("source", e.getSource());
        m.put("eventType", e.getEventType());
        putIfNotEmpty(m, "repository", e.getRepository());
        m.put("attentionClass", e.getAttentionClass());
        if (e.getDecisionState() != null) {
            m.put("decisionState", e.getDecisionState());
        }
        putIfNotEmpty(m, "threadId", e.getThreadId());
        putIfNotEmpty(m, "mergedInto", e.getMergedInto());
        m.put("createdAt", DateTimeFormatter.ISO_INSTANT.format(e.getCreatedAt()));
        putIfNotEmpty(m, "traceId", e.getTraceId());
        putIfNotEmpty(m, "parentEventId", e.getParentEventId());
        putIfNotEmpty(m, "agentName", e.getAgentName());
        return m;
    }

    private static void putIfNotEmpty(Map<String, Object> m, String key, String value) {
        if (!value.isEmpty()) {
            m.put(key, value);
        }
    }

    // ---- shared --------------------------------------------------------------------------------------------

    /**
     * Every request: a server span named {@code "<method> <route>"} (none for /health) that continues a valid incoming
     * traceparent and keeps its sampled flag, the traceparent returned in the response, the request handled with the
     * span's context passed explicitly, and one log line. A 5xx answer marks the span failed.
     */
    private void serve(HttpExchange x) throws IOException {
        long started = System.nanoTime();
        String path = x.getRequestURI().getPath();
        String route = Routes.traced(path);
        Telemetry.Span span = null;
        if (route != null) {
            Telemetry.SpanContext incoming = Telemetry.parseTraceparent(x.getRequestHeaders().getFirst(Telemetry.HEADER)).orElse(null);
            span = tracer.start(incoming, x.getRequestMethod() + " " + route, Telemetry.Kind.SERVER);
            span.setAttribute(Telemetry.ATTR_HTTP_METHOD, x.getRequestMethod());
            span.setAttribute(Telemetry.ATTR_HTTP_ROUTE, route);
            x.getResponseHeaders().set(Telemetry.HEADER, span.context().traceparent());
        }
        try {
            route(x, span == null ? null : span.context());
        } catch (RuntimeException e) {
            LOG.error("unhandled error path={} error={}", path, e.toString());
            api.error(x, 500, "internal error");
        } finally {
            int status = x.getResponseCode() < 0 ? 200 : x.getResponseCode();
            LOG.info("request method={} path={} status={} ms={}", x.getRequestMethod(), path, status,
                    (System.nanoTime() - started) / 1_000_000);
            if (span != null) {
                span.setIntAttribute(Telemetry.ATTR_HTTP_STATUS_CODE, status);
                span.end(status >= 500 ? "server error" : null);
            }
            x.close();
        }
    }
}
