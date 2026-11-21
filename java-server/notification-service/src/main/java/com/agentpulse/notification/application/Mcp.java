package com.agentpulse.notification.application;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * A Model Context Protocol server: JSON-RPC 2.0 messages that let an AI client use the platform through four tools.
 * Ported from the Go service's {@code pkg/mcp} with the same methods, tools, schemas, limits and messages, held to the
 * shared contract file {@code 17-mcp.json}.
 *
 * <p>It is transport-free: {@link Server#handle} turns one message into one answer (or none, for a notification).
 * The tools act through a {@link Backend}, so a client can only do what the REST API already lets an API-key holder
 * do. Critical events are decided by rules, never by a tool.
 */
public final class Mcp {
    /** The MCP revision this server speaks. A client proposing any other revision is answered with this one. */
    public static final String PROTOCOL_VERSION = "2024-11-05";

    public static final String TOOL_PUBLISH_EVENT = "publish_event";
    public static final String TOOL_GET_RECENT_EVENTS = "get_recent_events";
    public static final String TOOL_NOTIFY_USER = "notify_user";
    public static final String TOOL_EXPLAIN_DECISION = "explain_decision";

    static final int CODE_PARSE_ERROR = -32700;
    static final int CODE_INVALID_REQUEST = -32600;
    static final int CODE_METHOD_NOT_FOUND = -32601;
    static final int CODE_INVALID_PARAMS = -32602;

    static final int DEFAULT_RECENT_LIMIT = 20;
    static final int MAX_RECENT_LIMIT = 50;
    static final int MAX_NOTIFY_TITLE = 200;
    static final int MAX_NOTIFY_MESSAGE = 4000;
    static final int MAX_NOTIFY_RECIPIENTS = 10;
    static final int MAX_EVENT_ID_LENGTH = 200;
    static final int MAX_ARGUMENTS_BYTES = 64 * 1024;

    private static final String JSONRPC = "2.0";
    private static final String NOTIFICATIONS_PREFIX = "notifications/";
    private static final String SERVER_NAME = "agentpulse";
    private static final String INVALID_PARAMS = "invalid params";
    private static final String INVALID = "invalid request: ";
    private static final String INSTRUCTIONS = "AgentPulse decides whether people should be interrupted. Publish each agent step with "
            + "publish_event, read runs with get_recent_events, and ask why with explain_decision.";
    /** Tool results are written as Go writes a map: keys in order, so the same answer is the same text. */
    private static final ObjectMapper OUT = new ObjectMapper().configure(SerializationFeature.ORDER_MAP_ENTRIES_BY_KEYS, true);
    private static final TypeReference<Map<String, Object>> MAP = new TypeReference<>() {};

    private Mcp() {}

    /** The input of notify_user. */
    public record Notification(String title, String message, String agent, List<String> emailRecipients,
                               List<String> slackWebhookUrls, List<String> teamsWebhookUrls) {}

    /** What the tools act on. Each method returns an object written to the client as JSON. */
    public interface Backend {
        Object publishEvent(AgentEvents.Input in);

        Object recentEvents(String traceId, int limit);

        Object notify(Notification n);

        Object explain(String eventId);
    }

    /** A tool failure whose message is shown to the client, such as {@code invalid request: event not found}. */
    public static final class ToolException extends RuntimeException {
        public ToolException(String message) {
            super(message);
        }

        /** A refusal of the caller's input, prefixed as Go prefixes its ErrInvalid. */
        public static ToolException invalid(String detail) {
            return new ToolException(INVALID + detail);
        }
    }

    /** A JSON-RPC error: code and message. */
    private record RpcError(int code, String message) {}

    /** Answers MCP requests. */
    public static final class Server {
        private final Backend backend;
        private final String version;

        /** {@code version} is reported to clients; empty means {@code dev}, as in Go. */
        public Server(Backend backend, String version) {
            this.backend = backend;
            this.version = version == null || version.isEmpty() ? "dev" : version;
        }

        /** Processes one JSON-RPC message: the answer's bytes, or null when the message was a notification. */
        public byte[] handle(byte[] raw) {
            String trimmed = AgentEvents.goTrimSpace(new String(raw, StandardCharsets.UTF_8));
            if (trimmed.startsWith("[")) {
                return error("null", new RpcError(CODE_INVALID_REQUEST, "batches are not supported; send one request at a time"));
            }
            byte[] body = trimmed.getBytes(StandardCharsets.UTF_8);
            JsonNode parsed = RawJson.parse(body);
            if (parsed == null || !(parsed.isObject() || parsed.isNull())) {
                return error("null", new RpcError(CODE_PARSE_ERROR, "the message is not valid JSON"));
            }
            List<RawJson.Member> members = parsed.isNull() ? List.of() : RawJson.members(body);
            String jsonrpc;
            String method;
            try {
                jsonrpc = text(RawJson.field(members, "jsonrpc"));
                method = text(RawJson.field(members, "method"));
            } catch (IllegalArgumentException e) {
                return error("null", new RpcError(CODE_PARSE_ERROR, "the message is not valid JSON")); // Go's decoder fails on a wrong type
            }
            byte[] id = RawJson.field(members, "id");
            byte[] params = RawJson.field(members, "params");
            String idText = id == null ? "null" : RawJson.compact(id);
            if (!JSONRPC.equals(jsonrpc) || method.isEmpty()) {
                return error(idText, new RpcError(CODE_INVALID_REQUEST, "not a JSON-RPC 2.0 request"));
            }
            boolean notification = id == null;
            if (notification && method.startsWith(NOTIFICATIONS_PREFIX)) {
                return null;
            }
            Object result;
            RpcError rpcError = null;
            try {
                result = dispatch(method, params);
            } catch (RpcFailure f) {
                result = null;
                rpcError = f.error;
            }
            if (notification) {
                return null; // a request without an id still runs, as in Go, but is never answered
            }
            return rpcError != null ? error(idText, rpcError) : success(idText, result);
        }

        private Object dispatch(String method, byte[] params) {
            return switch (method) {
                case "initialize" -> {
                    Map<String, Object> out = new LinkedHashMap<>();
                    out.put("protocolVersion", PROTOCOL_VERSION);
                    out.put("capabilities", Map.of("tools", Map.of()));
                    out.put("serverInfo", Map.of("name", SERVER_NAME, "version", version));
                    out.put("instructions", INSTRUCTIONS);
                    yield out;
                }
                case "ping" -> Map.of();
                case "tools/list" -> Map.of("tools", Tools.list());
                case "tools/call" -> callTool(params);
                default -> throw new RpcFailure(CODE_METHOD_NOT_FOUND, "unknown method " + method);
            };
        }

        private Object callTool(byte[] params) {
            String name;
            byte[] arguments;
            try {
                JsonNode p = params == null ? null : RawJson.parse(params);
                if (p == null || !(p.isObject() || p.isNull())) {
                    throw new IllegalArgumentException();
                }
                List<RawJson.Member> members = p.isNull() ? List.of() : RawJson.members(params);
                name = text(RawJson.field(members, "name"));
                arguments = RawJson.field(members, "arguments");
            } catch (IllegalArgumentException e) {
                throw new RpcFailure(CODE_INVALID_PARAMS, INVALID_PARAMS + ": a tool name is required");
            }
            if (name.isEmpty()) {
                throw new RpcFailure(CODE_INVALID_PARAMS, INVALID_PARAMS + ": a tool name is required");
            }
            if (arguments != null && arguments.length > MAX_ARGUMENTS_BYTES) {
                throw new RpcFailure(CODE_INVALID_PARAMS, INVALID_PARAMS + ": the arguments are too large");
            }
            if (arguments == null) {
                arguments = "{}".getBytes(StandardCharsets.UTF_8);
            }
            Object out;
            try {
                out = switch (name) {
                    case TOOL_PUBLISH_EVENT -> {
                        Map<String, Object> a = Arguments.decode(arguments, Arguments.PUBLISH);
                        yield backend.publishEvent(new AgentEvents.Input(str(a, "id"), str(a, "source"), str(a, "eventType"),
                                str(a, "repository"), str(a, "traceId"), str(a, "parentEventId"), str(a, "agentName"),
                                str(a, "severity"), map(a, "metadata"), map(a, "payload")));
                    }
                    case TOOL_GET_RECENT_EVENTS -> {
                        Map<String, Object> a = Arguments.decode(arguments, Arguments.RECENT);
                        long limit = a.get("limit") instanceof Long l ? l : 0;
                        if (limit == 0) {
                            limit = DEFAULT_RECENT_LIMIT;
                        }
                        if (limit < 1 || limit > MAX_RECENT_LIMIT) {
                            throw ToolException.invalid("limit must be between 1 and " + MAX_RECENT_LIMIT);
                        }
                        yield backend.recentEvents(str(a, "traceId"), (int) limit);
                    }
                    case TOOL_NOTIFY_USER -> {
                        Map<String, Object> a = Arguments.decode(arguments, Arguments.NOTIFY);
                        Notification n = new Notification(str(a, "title"), str(a, "message"), str(a, "agent"),
                                list(a, "emailRecipients"), list(a, "slackWebhookUrls"), list(a, "teamsWebhookUrls"));
                        checkNotification(n);
                        yield backend.notify(n);
                    }
                    case TOOL_EXPLAIN_DECISION -> {
                        Map<String, Object> a = Arguments.decode(arguments, Arguments.EXPLAIN);
                        String eventId = str(a, "eventId");
                        if (eventId.isEmpty() || bytes(eventId) > MAX_EVENT_ID_LENGTH) {
                            throw ToolException.invalid("eventId is required");
                        }
                        yield backend.explain(eventId);
                    }
                    default -> throw new RpcFailure(CODE_INVALID_PARAMS, INVALID_PARAMS + ": unknown tool " + name);
                };
            } catch (ToolException e) {
                return toolResult(e.getMessage(), true);
            }
            try {
                return toolResult(OUT.writeValueAsString(out), false);
            } catch (JsonProcessingException e) {
                return toolResult("the result could not be encoded", true);
            }
        }
    }

    private static void checkNotification(Notification n) {
        if (n.title().isEmpty() || bytes(n.title()) > MAX_NOTIFY_TITLE) {
            throw ToolException.invalid("title is required (at most " + MAX_NOTIFY_TITLE + " characters)");
        }
        if (n.message().isEmpty() || bytes(n.message()) > MAX_NOTIFY_MESSAGE) {
            throw ToolException.invalid("message is required (at most " + MAX_NOTIFY_MESSAGE + " characters)");
        }
        for (List<String> list : List.of(n.emailRecipients(), n.slackWebhookUrls(), n.teamsWebhookUrls())) {
            if (list.size() > MAX_NOTIFY_RECIPIENTS) {
                throw ToolException.invalid("at most " + MAX_NOTIFY_RECIPIENTS + " recipients per channel");
            }
        }
    }

    private static Map<String, Object> toolResult(String text, boolean isError) {
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("content", List.of(Map.of("type", "text", "text", text)));
        out.put("isError", isError);
        return out;
    }

    private static byte[] success(String id, Object result) {
        return envelope(id, "\"result\":" + write(result));
    }

    private static byte[] error(String id, RpcError e) {
        Map<String, Object> err = new LinkedHashMap<>();
        err.put("code", e.code());
        err.put("message", e.message());
        return envelope(id, "\"error\":" + write(err));
    }

    private static byte[] envelope(String id, String member) {
        return ("{\"jsonrpc\":\"2.0\",\"id\":" + id + "," + member + "}").getBytes(StandardCharsets.UTF_8);
    }

    private static String write(Object value) {
        try {
            return OUT.writeValueAsString(value);
        } catch (JsonProcessingException e) {
            throw new IllegalStateException("encode answer", e);
        }
    }

    /** A text field as Go decodes it: absent or null is empty, a string is itself, anything else is a type error. */
    private static String text(byte[] raw) {
        if (raw == null || RawJson.isNull(raw)) {
            return "";
        }
        JsonNode node = RawJson.parse(raw);
        if (node == null || !node.isTextual()) {
            throw new IllegalArgumentException("not text");
        }
        return node.textValue();
    }

    private static int bytes(String s) {
        return s.getBytes(StandardCharsets.UTF_8).length; // Go's len counts bytes
    }

    private static String str(Map<String, Object> a, String key) {
        return a.get(key) instanceof String s ? s : "";
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> map(Map<String, Object> a, String key) {
        return (Map<String, Object>) a.get(key);
    }

    @SuppressWarnings("unchecked")
    private static List<String> list(Map<String, Object> a, String key) {
        return a.get(key) instanceof List<?> l ? (List<String>) l : List.of();
    }

    /** Ends dispatch with a JSON-RPC error. */
    private static final class RpcFailure extends RuntimeException {
        final RpcError error;

        RpcFailure(int code, String message) {
            super(message, null, false, false);
            this.error = new RpcError(code, message);
        }
    }

    /**
     * Strict decoding of a tool's arguments with Go's rules and the Go service's words: an unknown key is named, a
     * value of the wrong type names its argument, the first problem in document order wins, keys match exactly first
     * and then without regard to case.
     */
    static final class Arguments {
        enum Kind {
            TEXT("text"), WHOLE_NUMBER("a whole number"), OBJECT("an object"), TEXT_LIST("a list of text");

            final String words;

            Kind(String words) {
                this.words = words;
            }
        }

        static final Map<String, Kind> PUBLISH = ordered("id", Kind.TEXT, "source", Kind.TEXT, "eventType", Kind.TEXT,
                "repository", Kind.TEXT, "traceId", Kind.TEXT, "parentEventId", Kind.TEXT, "agentName", Kind.TEXT,
                "severity", Kind.TEXT, "metadata", Kind.OBJECT, "payload", Kind.OBJECT);
        static final Map<String, Kind> RECENT = ordered("traceId", Kind.TEXT, "limit", Kind.WHOLE_NUMBER);
        static final Map<String, Kind> NOTIFY = ordered("title", Kind.TEXT, "message", Kind.TEXT, "agent", Kind.TEXT,
                "emailRecipients", Kind.TEXT_LIST, "slackWebhookUrls", Kind.TEXT_LIST, "teamsWebhookUrls", Kind.TEXT_LIST);
        static final Map<String, Kind> EXPLAIN = ordered("eventId", Kind.TEXT);

        private Arguments() {}

        static Map<String, Object> decode(byte[] raw, Map<String, Kind> schema) {
            Map<String, Object> out = new LinkedHashMap<>();
            if (RawJson.isNull(raw)) {
                return out;
            }
            List<RawJson.Member> members = RawJson.members(raw);
            if (members == null) {
                throw ToolException.invalid("arguments must be an object");
            }
            for (RawJson.Member m : members) {
                String name = match(m.key(), schema);
                if (name == null) {
                    throw ToolException.invalid("unknown argument " + goQuote(m.key()));
                }
                Kind kind = schema.get(name);
                JsonNode v = RawJson.parse(m.value());
                if (v == null || v.isNull()) {
                    if (kind == Kind.OBJECT || kind == Kind.TEXT_LIST) {
                        out.put(name, null); // Go sets a map or slice to nil; text and numbers are left alone
                    }
                    continue;
                }
                out.put(name, value(name, kind, v, out.get(name)));
            }
            return out;
        }

        private static Object value(String name, Kind kind, JsonNode v, Object existing) {
            switch (kind) {
                case TEXT -> {
                    if (v.isTextual()) {
                        return v.textValue();
                    }
                }
                case WHOLE_NUMBER -> {
                    if (v.isIntegralNumber() && v.canConvertToLong()) {
                        return v.longValue();
                    }
                }
                case OBJECT -> {
                    if (v.isObject()) {
                        Map<String, Object> map = existing instanceof Map<?, ?> ? cast(existing) : new LinkedHashMap<>();
                        map.putAll(RawJson.STRICT.convertValue(v, MAP)); // Go decodes into the existing map
                        return map;
                    }
                }
                case TEXT_LIST -> {
                    if (v.isArray()) {
                        List<String> items = new ArrayList<>();
                        for (JsonNode item : v) {
                            if (item.isTextual()) {
                                items.add(item.textValue());
                            } else if (item.isNull()) {
                                items.add("");
                            } else {
                                throw wrongType(name, kind);
                            }
                        }
                        return items;
                    }
                }
            }
            throw wrongType(name, kind);
        }

        private static ToolException wrongType(String name, Kind kind) {
            return ToolException.invalid("argument " + name + " must be " + kind.words);
        }

        private static String match(String key, Map<String, Kind> schema) {
            if (schema.containsKey(key)) {
                return key;
            }
            for (String name : schema.keySet()) {
                if (name.equalsIgnoreCase(key)) {
                    return name;
                }
            }
            return null;
        }

        /** Go's %q for a key: quoted, with quotes, backslashes and control characters escaped. */
        static String goQuote(String s) {
            StringBuilder b = new StringBuilder("\"");
            for (int i = 0; i < s.length(); i++) {
                char c = s.charAt(i);
                switch (c) {
                    case '"' -> b.append("\\\"");
                    case '\\' -> b.append("\\\\");
                    case '\n' -> b.append("\\n");
                    case '\t' -> b.append("\\t");
                    case '\r' -> b.append("\\r");
                    default -> {
                        if (c < 0x20 || c == 0x7f) {
                            b.append(String.format("\\x%02x", (int) c));
                        } else {
                            b.append(c);
                        }
                    }
                }
            }
            return b.append('"').toString();
        }

        private static Map<String, Kind> ordered(Object... pairs) {
            Map<String, Kind> m = new LinkedHashMap<>();
            for (int i = 0; i < pairs.length; i += 2) {
                m.put((String) pairs[i], (Kind) pairs[i + 1]);
            }
            return m;
        }

        @SuppressWarnings("unchecked")
        private static Map<String, Object> cast(Object o) {
            return (Map<String, Object>) o;
        }
    }

    /** The tools/list answer: Go's descriptions and schemas, word for word. */
    static final class Tools {
        private Tools() {}

        static List<Map<String, Object>> list() {
            Map<String, Object> severity = new LinkedHashMap<>();
            severity.put("type", "string");
            severity.put("enum", List.of("info", "low", "medium", "high", "critical"));
            severity.put("description", "How serious the agent thinks this is. Data for rules only.");
            Map<String, Object> publishProps = new LinkedHashMap<>();
            publishProps.put("eventType", str("What happened, such as agent.plan, tool.call.failed, agent.retry or agent.complete."));
            publishProps.put("id", str("Optional idempotency key. Sending the same id twice stores one event."));
            publishProps.put("traceId", str("Identifies the run. Every step of one run shares it."));
            publishProps.put("parentEventId", str("The step that led to this one. Needs traceId."));
            publishProps.put("agentName", str("Which agent is reporting."));
            publishProps.put("source", str("Where the event comes from. Defaults to agent."));
            publishProps.put("repository", str("Optional repository or project the run is about."));
            publishProps.put("severity", severity);
            publishProps.put("metadata", Map.of("type", "object", "description",
                    "Telemetry: step_kind (plan, tool_call, retry, reflect, human_approval, complete), tool_name, attempt, duration_ms, "
                            + "tokens_in, tokens_out, cost_usd, confidence (0 to 1)."));
            publishProps.put("payload", Map.of("type", "object", "description", "Any other details to keep with the event."));

            Map<String, Object> recentProps = new LinkedHashMap<>();
            recentProps.put("traceId", str("Only events of this run."));
            recentProps.put("limit", Map.of("type", "integer", "minimum", 1, "maximum", MAX_RECENT_LIMIT,
                    "description", "How many to return. Defaults to 20."));

            Map<String, Object> notifyProps = new LinkedHashMap<>();
            notifyProps.put("title", str("A short headline."));
            notifyProps.put("message", str("The message body."));
            notifyProps.put("agent", str("Which agent is sending it."));
            notifyProps.put("emailRecipients", strings("Email addresses."));
            notifyProps.put("slackWebhookUrls", strings("Slack incoming webhook URLs on an allowed host."));
            notifyProps.put("teamsWebhookUrls", strings("Teams webhook URLs on an allowed host."));

            return List.of(
                    tool(TOOL_PUBLISH_EVENT, "Publish one step of an agent run into AgentPulse. Rules decide how important it is and whether "
                            + "anyone is interrupted; the severity you give is only data for those rules and cannot make an event critical. "
                            + "Chain steps with traceId and parentEventId.", object(List.of("eventType"), publishProps)),
                    tool(TOOL_GET_RECENT_EVENTS, "Read the most recent events, newest first, with the attention class and decision for each. "
                            + "Give traceId to read one agent run.", object(List.of(), recentProps)),
                    tool(TOOL_NOTIFY_USER, "Send a notification through the platform. It is validated like any other: only allowed hosts "
                            + "and recipient domains are accepted. Use publish_event instead when you want the rules to decide whether anyone "
                            + "should be interrupted.", object(List.of("title", "message"), notifyProps)),
                    tool(TOOL_EXPLAIN_DECISION, "Say in plain words why an event was handled the way it was: which rule applied, whether a "
                            + "person's choice or a budget held it, and whether a model took part.",
                            object(List.of("eventId"), Map.of("eventId", str("The event to explain.")))));
        }

        private static Map<String, Object> tool(String name, String description, Map<String, Object> schema) {
            Map<String, Object> t = new LinkedHashMap<>();
            t.put("name", name);
            t.put("description", description);
            t.put("inputSchema", schema);
            return t;
        }

        private static Map<String, Object> object(List<String> required, Map<String, Object> properties) {
            Map<String, Object> schema = new LinkedHashMap<>();
            schema.put("type", "object");
            schema.put("properties", properties);
            schema.put("additionalProperties", false);
            if (!required.isEmpty()) {
                schema.put("required", required);
            }
            return schema;
        }

        private static Map<String, Object> str(String description) {
            return Map.of("type", "string", "description", description);
        }

        private static Map<String, Object> strings(String description) {
            return Map.of("type", "array", "items", Map.of("type", "string"), "description", description);
        }
    }
}
