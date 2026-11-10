package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Feedback;
import com.agentpulse.notification.application.Overrides;
import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpExchange;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * {@code POST /feedback} and the always/never overrides at {@code /api/overrides}. It decodes, calls the feedback
 * or overrides service and encodes the answer. Same paths, statuses and bodies as the Go service.
 */
final class FeedbackApi {
    static final String OVERRIDES_PREFIX = "/api/overrides";
    static final String FEEDBACK_PATH = "/feedback";
    private static final Logger LOG = LoggerFactory.getLogger(FeedbackApi.class);

    private final ApiSupport api;
    private final Feedback.Service feedback;
    private final Overrides.Service overrides;
    private final Accounts.Service accounts;

    FeedbackApi(ApiSupport api, Feedback.Service feedback, Overrides.Service overrides, Accounts.Service accounts) {
        this.api = api;
        this.feedback = feedback;
        this.overrides = overrides;
        this.accounts = accounts;
    }

    /** Whether the caller may create or remove overrides: anyone, unless the service limits that to admins. */
    private boolean mayOverride(ApiSupport.Principal p) {
        return !api.config().overridesAdminOnly() || p.admin();
    }

    // ---- POST /feedback ------------------------------------------------------------------------------------

    void feedback(HttpExchange x) throws IOException {
        ApiSupport.Principal who = api.authenticate(x);
        if (who == null) {
            return;
        }
        if (!"POST".equals(x.getRequestMethod())) {
            api.error(x, 405, "method not allowed");
            return;
        }
        Optional<JsonNode> body = api.readJson(x);
        if (body.isEmpty()) {
            return;
        }
        JsonNode n = body.get();
        if (!n.isObject() || !textOrAbsent(n, "eventId") || !textOrAbsent(n, "action") || !textOrAbsent(n, "scope")
                || !textOrAbsent(n, "reason")) {
            api.error(x, 400, "invalid request payload");
            return;
        }
        if (feedback == null) {
            api.error(x, 503, "feedback is not available");
            return;
        }
        Feedback.Input in = new Feedback.Input(text(n, "eventId"), text(n, "action"), text(n, "scope"), text(n, "reason"));
        Feedback.Result res;
        try {
            res = feedback.record(who.actor(), in, mayOverride(who));
        } catch (Feedback.InvalidException | Overrides.InvalidException e) {
            api.error(x, 400, e.getMessage());
            return;
        } catch (Feedback.EventNotFoundException e) {
            api.error(x, 404, "event not found");
            return;
        } catch (RuntimeException e) {
            LOG.error("record feedback failed error={}", e.getMessage());
            api.error(x, 500, "internal error");
            return;
        }
        if (res.override() != null || res.reverted()) {
            String target = res.override() != null ? Overrides.describe(res.override()) : res.entry().eventId();
            audit(x, who, "override." + in.action(), target);
        }
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("status", "accepted");
        out.put("override", res.override() == null ? null : json(res.override()));
        out.put("overrideSkipped", res.overrideSkipped());
        out.put("reverted", res.reverted());
        api.reply(x, 202, out);
    }

    // ---- /api/overrides ------------------------------------------------------------------------------------

    void overrides(HttpExchange x) throws IOException {
        ApiSupport.Principal who = api.authenticate(x);
        if (who == null) {
            return;
        }
        if (overrides == null) {
            api.error(x, 503, "overrides are not available");
            return;
        }
        String id = x.getRequestURI().getPath().substring(OVERRIDES_PREFIX.length()).replaceAll("^/+|/+$", "");
        String method = x.getRequestMethod();
        if (id.isEmpty() && "GET".equals(method)) {
            List<Map<String, Object>> list = new ArrayList<>();
            overrides.list().forEach(o -> list.add(json(o)));
            Map<String, Object> out = new LinkedHashMap<>();
            out.put("overrides", list);
            out.put("adminOnly", api.config().overridesAdminOnly());
            api.reply(x, 200, out);
        } else if (id.isEmpty() && "POST".equals(method)) {
            create(x, who);
        } else if (!id.isEmpty() && !id.contains("/") && "DELETE".equals(method)) {
            remove(x, who, id);
        } else {
            api.error(x, 405, "method not allowed");
        }
    }

    private void create(HttpExchange x, ApiSupport.Principal who) throws IOException {
        if (!mayOverride(who)) {
            api.error(x, 403, "admin access required");
            return;
        }
        Optional<JsonNode> body = api.readJson(x);
        if (body.isEmpty()) {
            return;
        }
        JsonNode n = body.get();
        if (!n.isObject() || !textOrAbsent(n, "mode") || !textOrAbsent(n, "source") || !textOrAbsent(n, "eventType")
                || !textOrAbsent(n, "repository") || !textOrAbsent(n, "note")) {
            api.error(x, 400, "invalid request payload");
            return;
        }
        Overrides.Override saved;
        try {
            saved = overrides.upsert(new Overrides.Override("", text(n, "source"), text(n, "eventType"),
                    text(n, "repository"), text(n, "mode"), who.actor(), text(n, "note"), null));
        } catch (Overrides.InvalidException e) {
            api.error(x, 400, e.getMessage());
            return;
        } catch (RuntimeException e) {
            LOG.error("override request failed error={}", e.getMessage());
            api.error(x, 500, "internal error");
            return;
        }
        audit(x, who, "override." + saved.mode(), Overrides.describe(saved));
        api.reply(x, 201, json(saved));
    }

    private void remove(HttpExchange x, ApiSupport.Principal who, String id) throws IOException {
        if (!mayOverride(who)) {
            api.error(x, 403, "admin access required");
            return;
        }
        try {
            overrides.delete(id);
        } catch (Overrides.NotFoundException e) {
            api.error(x, 404, e.getMessage());
            return;
        } catch (RuntimeException e) {
            LOG.error("override request failed error={}", e.getMessage());
            api.error(x, 500, "internal error");
            return;
        }
        audit(x, who, "override.removed", id);
        api.replyEmpty(x, 204);
    }

    // ---- helpers -------------------------------------------------------------------------------------------

    /** Writes a security-relevant change to the audit trail, never failing the request. */
    private void audit(HttpExchange x, ApiSupport.Principal who, String action, String target) {
        if (accounts != null) {
            InetSocketAddress a = x.getRemoteAddress();
            String ip = a == null || a.getAddress() == null ? "unknown" : a.getAddress().getHostAddress();
            accounts.auditEvent(who.actor(), action, target, ip, "");
        }
    }

    private static Map<String, Object> json(Overrides.Override o) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", o.id());
        putIfSet(m, "source", o.source());
        putIfSet(m, "eventType", o.eventType());
        putIfSet(m, "repository", o.repository());
        m.put("mode", o.mode());
        m.put("createdBy", o.createdBy());
        putIfSet(m, "note", o.note());
        m.put("createdAt", o.createdAt() == null ? null : o.createdAt().toString());
        return m;
    }

    private static void putIfSet(Map<String, Object> m, String key, String value) {
        if (!value.isEmpty()) {
            m.put(key, value);
        }
    }

    private static boolean textOrAbsent(JsonNode n, String field) {
        return !n.has(field) || n.get(field).isNull() || n.get(field).isTextual();
    }

    private static String text(JsonNode n, String field) {
        return n.hasNonNull(field) ? n.get(field).asText() : "";
    }
}
