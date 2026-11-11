package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Tuning;
import com.sun.net.httpserver.HttpExchange;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * {@code /api/learning/adjustments} and {@code /api/learning/runs}: offline score tuning. It decodes, calls the
 * tuner and encodes the answer. Same paths, statuses and bodies as the Go service.
 */
final class TuningApi {
    static final String ADJUSTMENTS_PREFIX = "/api/learning/adjustments";
    static final String RUNS_PREFIX = "/api/learning/runs";
    private static final int RUNS_SHOWN = 20;
    private static final Set<String> STATUSES = Set.of(Tuning.PROPOSED, Tuning.ACTIVE, Tuning.REJECTED, Tuning.RETIRED);
    private static final Logger LOG = LoggerFactory.getLogger(TuningApi.class);

    private final ApiSupport api;
    private final Tuning.Tuner tuner;
    private final Accounts.Service accounts;
    private final Duration trainEvery;

    TuningApi(ApiSupport api, Tuning.Tuner tuner, Accounts.Service accounts, Duration trainEvery) {
        this.api = api;
        this.tuner = tuner;
        this.accounts = accounts;
        this.trainEvery = trainEvery;
    }

    /** {@code GET /api/learning/adjustments?status=} and admin {@code POST .../{id}/approve|reject|retire}. */
    void adjustments(HttpExchange x) throws IOException {
        ApiSupport.Principal who = api.authenticate(x);
        if (who == null) {
            return;
        }
        String rest = x.getRequestURI().getPath().substring(ADJUSTMENTS_PREFIX.length()).replaceAll("^/+|/+$", "");
        String method = x.getRequestMethod();
        if (rest.isEmpty() && "GET".equals(method)) {
            list(x);
        } else if (!rest.isEmpty() && "POST".equals(method)) {
            int slash = rest.indexOf('/');
            String id = slash < 0 ? "" : rest.substring(0, slash);
            String action = slash < 0 ? "" : rest.substring(slash + 1);
            if (id.isEmpty() || !(action.equals("approve") || action.equals("reject") || action.equals("retire"))) {
                api.error(x, 404, "not found");
                return;
            }
            if (!who.admin()) {
                api.error(x, 403, "admin access required");
                return;
            }
            decide(x, who, id, action);
        } else {
            api.error(x, 405, "method not allowed");
        }
    }

    /** {@code GET /api/learning/runs} and admin {@code POST /api/learning/runs[?wait=true]}. */
    void runs(HttpExchange x) throws IOException {
        ApiSupport.Principal who = api.authenticate(x);
        if (who == null) {
            return;
        }
        String rest = x.getRequestURI().getPath().substring(RUNS_PREFIX.length()).replaceAll("^/+|/+$", "");
        if (!rest.isEmpty()) {
            api.error(x, 404, "not found");
            return;
        }
        String method = x.getRequestMethod();
        if ("GET".equals(method)) {
            try {
                List<Map<String, Object>> out = new ArrayList<>();
                tuner.runs(RUNS_SHOWN).forEach(r -> out.add(Tuning.toMap(r)));
                Map<String, Object> body = new LinkedHashMap<>();
                body.put("runs", out);
                body.put("every", AttentionApi.goDuration(trainEvery));
                api.reply(x, 200, body);
            } catch (RuntimeException e) {
                fail(x, e);
            }
        } else if ("POST".equals(method)) {
            if (!who.admin()) {
                api.error(x, 403, "admin access required");
                return;
            }
            boolean wait = hasWait(x.getRequestURI().getRawQuery());
            Tuning.Run run;
            try {
                run = wait ? tuner.runNow() : tuner.start();
            } catch (RuntimeException e) {
                fail(x, e);
                return;
            }
            audit(x, who, "learning.run", "run " + run.id(), wait ? run.status() : "started");
            api.reply(x, wait ? 200 : 202, Map.of("run", Tuning.toMap(run)));
        } else {
            api.error(x, 405, "method not allowed");
        }
    }

    private static boolean hasWait(String query) {
        if (query == null) {
            return false;
        }
        for (String pair : query.split("&")) {
            if (pair.equals("wait=true")) {
                return true;
            }
        }
        return false;
    }

    private void list(HttpExchange x) throws IOException {
        String status = queryValue(x.getRequestURI().getRawQuery(), "status");
        if (!status.isEmpty() && !STATUSES.contains(status)) {
            api.error(x, 400, "status must be proposed, active, rejected or retired");
            return;
        }
        List<Tuning.Adjustment> found;
        try {
            found = tuner.list(status);
        } catch (RuntimeException e) {
            fail(x, e);
            return;
        }
        List<Map<String, Object>> out = new ArrayList<>();
        found.forEach(a -> out.add(Tuning.toMap(a)));
        Tuning.Config cfg = tuner.config();
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("adjustments", out);
        body.put("version", tuner.version());
        body.put("maxAdjustment", cfg.maxAdjustment());
        body.put("minSignals", cfg.minSignals());
        body.put("minPeople", cfg.minPeople());
        api.reply(x, 200, body);
    }

    private void decide(HttpExchange x, ApiSupport.Principal who, String id, String action) throws IOException {
        Tuning.Adjustment adj;
        try {
            adj = switch (action) {
                case "approve" -> tuner.approve(id, who.actor());
                case "reject" -> tuner.reject(id, who.actor());
                default -> tuner.retire(id, who.actor());
            };
        } catch (RuntimeException e) {
            fail(x, e);
            return;
        }
        audit(x, who, "learning." + action, "adjustment " + adj.source() + " " + adj.eventType(), adj.evidence().summary());
        api.reply(x, 200, Map.of("adjustment", Tuning.toMap(adj)));
    }

    private void audit(HttpExchange x, ApiSupport.Principal who, String action, String target, String detail) {
        if (accounts != null) {
            InetSocketAddress a = x.getRemoteAddress();
            String ip = a == null || a.getAddress() == null ? "unknown" : a.getAddress().getHostAddress();
            accounts.auditEvent(who.actor(), action, target, ip, detail);
        }
    }

    private static String queryValue(String query, String name) {
        if (query == null) {
            return "";
        }
        for (String pair : query.split("&")) {
            int eq = pair.indexOf('=');
            if (eq > 0 && pair.substring(0, eq).equals(name)) {
                return java.net.URLDecoder.decode(pair.substring(eq + 1), java.nio.charset.StandardCharsets.UTF_8);
            }
        }
        return "";
    }

    private void fail(HttpExchange x, RuntimeException e) throws IOException {
        if (e instanceof Tuning.NotFoundException) {
            api.error(x, 404, e.getMessage());
        } else if (e instanceof Tuning.NotProposedException || e instanceof Tuning.NotActiveException || e instanceof Tuning.RunInProgressException) {
            api.error(x, 409, e.getMessage());
        } else if (e instanceof Tuning.GuardrailException) {
            api.error(x, 403, e.getMessage());
        } else {
            LOG.error("learning request failed error={}", e.getMessage());
            api.error(x, 500, "internal error");
        }
    }
}
