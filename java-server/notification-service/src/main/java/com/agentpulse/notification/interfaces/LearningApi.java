package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Learning;
import com.agentpulse.notification.application.Overrides;
import com.sun.net.httpserver.HttpExchange;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * {@code /api/learning/suggestions}: reviewable suggestions made from stored feedback. It decodes, calls the
 * learning service and encodes the answer. Same paths, statuses and bodies as the Go service.
 */
final class LearningApi {
    static final String PREFIX = "/api/learning/suggestions";
    private static final Logger LOG = LoggerFactory.getLogger(LearningApi.class);

    private final ApiSupport api;
    private final Learning.Service learning;
    private final Accounts.Service accounts;

    LearningApi(ApiSupport api, Learning.Service learning, Accounts.Service accounts) {
        this.api = api;
        this.learning = learning;
        this.accounts = accounts;
    }

    void handle(HttpExchange x) throws IOException {
        ApiSupport.Principal who = api.authenticate(x);
        if (who == null) {
            return;
        }
        String rest = x.getRequestURI().getPath().substring(PREFIX.length()).replaceAll("^/+|/+$", "");
        String method = x.getRequestMethod();
        if (rest.isEmpty() && "GET".equals(method)) {
            list(x);
        } else if (!rest.isEmpty() && "POST".equals(method)) {
            int slash = rest.indexOf('/');
            String id = slash < 0 ? "" : rest.substring(0, slash);
            String action = slash < 0 ? "" : rest.substring(slash + 1);
            if (id.isEmpty() || !(action.equals("apply") || action.equals("dismiss"))) {
                api.error(x, 404, "not found");
                return;
            }
            if (!who.admin()) {
                api.error(x, 403, "admin access required");
                return;
            }
            resolve(x, who, id, action);
        } else {
            api.error(x, 405, "method not allowed");
        }
    }

    private void list(HttpExchange x) throws IOException {
        List<Learning.Suggestion> found;
        try {
            found = learning.review();
        } catch (RuntimeException e) {
            fail(x, e);
            return;
        }
        List<Map<String, Object>> out = new ArrayList<>();
        found.forEach(s -> out.add(Learning.toMap(s)));
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("suggestions", out);
        body.put("minSignals", learning.minSignals());
        api.reply(x, 200, body);
    }

    private void resolve(HttpExchange x, ApiSupport.Principal who, String id, String action) throws IOException {
        Learning.Suggestion sug;
        try {
            sug = action.equals("apply") ? learning.apply(id, who.actor()) : learning.dismiss(id, who.actor());
        } catch (RuntimeException e) {
            fail(x, e);
            return;
        }
        if (accounts != null) {
            InetSocketAddress a = x.getRemoteAddress();
            String ip = a == null || a.getAddress() == null ? "unknown" : a.getAddress().getHostAddress();
            accounts.auditEvent(who.actor(), "learning." + action, sug.kind() + " " + sug.source() + " " + sug.eventType(), ip, sug.proposed());
        }
        api.reply(x, 200, Map.of("suggestion", Learning.toMap(sug)));
    }

    private void fail(HttpExchange x, RuntimeException e) throws IOException {
        if (e instanceof Learning.NotFoundException) {
            api.error(x, 404, e.getMessage());
        } else if (e instanceof Learning.NotPendingException || e instanceof Learning.RuleGoneException) {
            api.error(x, 409, e.getMessage());
        } else if (e instanceof Learning.GuardrailException) {
            api.error(x, 403, e.getMessage());
        } else if (e instanceof Overrides.InvalidException) {
            api.error(x, 400, e.getMessage());
        } else {
            LOG.error("learning request failed error={}", e.getMessage());
            api.error(x, 500, "internal error");
        }
    }
}
