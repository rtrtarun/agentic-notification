package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.PolicySummary;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.Config;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpExchange;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * The rules and policies endpoints. Handlers only decode, call {@link RulesService} and encode.
 *
 * <pre>
 * GET    /api/rules              list in evaluation order
 * POST   /api/rules              create (appended after existing rules)
 * POST   /api/rules/reorder      {ids: [...]} every id exactly once, locked rules first
 * POST   /api/rules/test         dry run a sample event; nothing is stored or delivered
 * PUT    /api/rules/{id}         replace an unlocked rule
 * DELETE /api/rules/{id}         remove an unlocked rule
 * GET    /api/policies           the effective policy, read only
 * </pre>
 */
final class RulesApi {
    static final String PREFIX = "/api/rules";
    private static final Logger LOG = LoggerFactory.getLogger(RulesApi.class);

    private final ApiSupport api;
    private final RulesService rules;

    RulesApi(ApiSupport api, RulesService rules) {
        this.api = api;
        this.rules = rules;
    }

    void rules(HttpExchange x) throws IOException {
        // Anyone signed in may read rules and dry-run one; changing them needs an admin.
        boolean change = !"GET".equals(x.getRequestMethod()) && !x.getRequestURI().getPath().replaceAll("/+$", "").endsWith("/test");
        if (!(change ? api.authorisedAdmin(x) : api.authorised(x))) {
            return;
        }
        String rest = x.getRequestURI().getPath().substring(PREFIX.length()).replaceAll("^/+|/+$", "");
        try {
            if (rest.isEmpty()) {
                collection(x);
            } else if (rest.equals("reorder")) {
                reorder(x);
            } else if (rest.equals("test")) {
                test(x);
            } else if (!rest.contains("/")) {
                item(x, rest);
            } else {
                api.error(x, 404, "not found");
            }
        } catch (RulesService.NotFoundException e) {
            api.error(x, 404, e.getMessage());
        } catch (RulesService.LockedException e) {
            api.error(x, 409, e.getMessage());
        } catch (RulesService.InvalidException e) {
            api.error(x, 400, e.getMessage());
        } catch (RuntimeException e) {
            LOG.error("rules request failed path={} error={}", x.getRequestURI().getPath(), e.getMessage());
            api.error(x, 500, "internal error");
        }
    }

    void policies(HttpExchange x, Config config) throws IOException {
        if (!api.authorised(x)) {
            return;
        }
        if (!"GET".equals(x.getRequestMethod())) {
            api.methodNotAllowed(x, "GET");
            return;
        }
        try {
            api.reply(x, 200, PolicySummary.describe(config.dedupWindow().isZero()
                    ? com.agentpulse.notification.application.MemoryDeduper.DEFAULT_WINDOW : config.dedupWindow(),
                    config.eventRetention(), rules.list()));
        } catch (RuntimeException e) {
            LOG.error("list rules failed error={}", e.getMessage());
            api.error(x, 500, "internal error");
        }
    }

    private void collection(HttpExchange x) throws IOException {
        switch (x.getRequestMethod()) {
            case "GET" -> api.reply(x, 200, Map.of("rules", rules.list()));
            case "POST" -> {
                Optional<Rule> in = readRule(x);
                if (in.isPresent()) {
                    api.reply(x, 201, rules.create(in.get()));
                }
            }
            default -> api.methodNotAllowed(x, "GET, POST");
        }
    }

    private void item(HttpExchange x, String id) throws IOException {
        switch (x.getRequestMethod()) {
            case "PUT" -> {
                Optional<Rule> in = readRule(x);
                if (in.isPresent()) {
                    api.reply(x, 200, rules.update(id, in.get()));
                }
            }
            case "DELETE" -> {
                rules.delete(id);
                api.replyEmpty(x, 204);
            }
            default -> api.methodNotAllowed(x, "PUT, DELETE");
        }
    }

    private void reorder(HttpExchange x) throws IOException {
        if (!"POST".equals(x.getRequestMethod())) {
            api.methodNotAllowed(x, "POST");
            return;
        }
        Optional<JsonNode> body = api.readJson(x);
        if (body.isEmpty()) {
            return;
        }
        List<String> ids = new ArrayList<>();
        JsonNode list = body.get().path("ids");
        if (!body.get().isObject() || (!list.isArray() && !list.isMissingNode() && !list.isNull())) {
            api.error(x, 400, "invalid request payload");
            return;
        }
        for (JsonNode id : list) {
            if (!id.isTextual()) {
                api.error(x, 400, "invalid request payload");
                return;
            }
            ids.add(id.asText());
        }
        rules.reorder(ids);
        api.reply(x, 200, Map.of("rules", rules.list()));
    }

    private void test(HttpExchange x) throws IOException {
        if (!"POST".equals(x.getRequestMethod())) {
            api.methodNotAllowed(x, "POST");
            return;
        }
        Optional<JsonNode> body = api.readJson(x);
        if (body.isEmpty()) {
            return;
        }
        if (!body.get().isObject() && !body.get().isNull()) {
            api.error(x, 400, "invalid request payload");
            return;
        }
        JsonNode n = body.get();
        Map<String, Object> payload = null;
        if (n.path("payload").isObject()) {
            payload = ApiSupport.JSON.convertValue(n.get("payload"), new com.fasterxml.jackson.core.type.TypeReference<Map<String, Object>>() {});
        }
        RulesService.Result r = rules.test(new RulesService.Sample(text(n, "source"), text(n, "eventType"), text(n, "repository"), payload));
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("matched", r.matched());
        out.put("attentionClass", r.attentionClass());
        out.put("reason", r.reason());
        if (!r.matchedRule().isEmpty()) {
            out.put("matchedRule", r.matchedRule());
        }
        if (!r.channels().isEmpty()) {
            out.put("channels", r.channels());
        }
        out.put("aiBypassed", r.aiBypassed());
        List<Map<String, Object>> steps = new ArrayList<>();
        for (RulesService.Step s : r.steps()) {
            Map<String, Object> step = new LinkedHashMap<>();
            step.put("id", s.id());
            step.put("name", s.name());
            if (s.disabled()) {
                step.put("disabled", true);
            }
            step.put("matched", s.matched());
            if (s.winner()) {
                step.put("winner", true);
            }
            steps.add(step);
        }
        out.put("steps", steps);
        api.reply(x, 200, out);
    }

    private static String text(JsonNode n, String field) {
        return n.path(field).isTextual() ? n.get(field).asText() : "";
    }

    /** Reads a rule from the request, including its payload predicates and channel override. */
    private Optional<Rule> readRule(HttpExchange x) throws IOException {
        Optional<JsonNode> body = api.readJson(x);
        if (body.isEmpty()) {
            return Optional.empty();
        }
        JsonNode n = body.get();
        if (!n.isObject()) {
            api.error(x, 400, "invalid request payload");
            return Optional.empty();
        }
        try {
            return Optional.of(ApiSupport.JSON.treeToValue(n, Rule.class));
        } catch (JsonProcessingException e) {
            api.error(x, 400, "invalid request payload");
            return Optional.empty();
        }
    }
}
