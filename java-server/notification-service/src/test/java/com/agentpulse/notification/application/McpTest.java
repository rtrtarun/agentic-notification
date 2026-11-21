package com.agentpulse.notification.application;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** The dispatcher against a fake backend: Go's JSON-RPC and argument rules that the contract cannot reach. */
class McpTest {
    private static final ObjectMapper JSON = new ObjectMapper();

    /** Records what reached it, and answers with what it was given. */
    private static class FakeBackend implements Mcp.Backend {
        final List<Object> calls = new ArrayList<>();
        RuntimeException fail;

        @Override public Object publishEvent(AgentEvents.Input in) { return record(in); }
        @Override public Object recentEvents(String traceId, int limit) { return record(Map.of("traceId", traceId, "limit", limit)); }
        @Override public Object notify(Mcp.Notification n) { return record(n); }
        @Override public Object explain(String eventId) { return record(Map.of("eventId", eventId)); }

        private Object record(Object o) {
            calls.add(o);
            if (fail != null) {
                throw fail;
            }
            return Map.of("ok", true);
        }
    }

    private final FakeBackend backend = new FakeBackend();
    private final Mcp.Server server = new Mcp.Server(backend, "");

    private String raw(String message) {
        byte[] out = server.handle(message.getBytes(StandardCharsets.UTF_8));
        return out == null ? null : new String(out, StandardCharsets.UTF_8);
    }

    private JsonNode rpc(String message) throws Exception {
        return JSON.readTree(raw(message));
    }

    private String call(String tool, String args) {
        return "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"" + tool + "\",\"arguments\":" + args + "}}";
    }

    private String toolText(String tool, String args) throws Exception {
        return rpc(call(tool, args)).at("/result/content/0/text").asText();
    }

    private boolean toolError(String tool, String args) throws Exception {
        return rpc(call(tool, args)).at("/result/isError").asBoolean();
    }

    @Test
    void theIdIsEchoedAsGoWritesARawMessage() {
        assertEquals("{\"jsonrpc\":\"2.0\",\"id\":1.50,\"result\":{}}", raw("{\"jsonrpc\":\"2.0\",\"id\":1.50,\"method\":\"ping\"}"),
                "the number keeps its written form");
        assertEquals("{\"jsonrpc\":\"2.0\",\"id\":[1,{\"a\":\"b c\"}],\"result\":{}}",
                raw("{\"jsonrpc\":\"2.0\",\"id\":[ 1 , { \"a\" : \"b c\" } ],\"method\":\"ping\"}"), "compacted, spaces in strings kept");
        assertEquals("{\"jsonrpc\":\"2.0\",\"id\":\"\\u003cx\\u003e\\u0026\\u0041\",\"result\":{}}",
                raw("{\"jsonrpc\":\"2.0\",\"id\":\"<x>&\\u0041\",\"method\":\"ping\"}"), "HTML characters escaped, escapes kept");
    }

    @Test
    void initializeAnswersOurProtocolWhateverTheClientProposesAndAnEmptyVersionIsDev() throws Exception {
        JsonNode init = JSON.readTree(new Mcp.Server(backend, null).handle(
                "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2099-01-01\"}}".getBytes(StandardCharsets.UTF_8)));
        assertEquals("2024-11-05", init.at("/result/protocolVersion").asText());
        assertEquals("dev", init.at("/result/serverInfo/version").asText());
        assertEquals(4, rpc("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}").at("/result/tools").size());
    }

    @Test
    void keysMatchWithoutCaseAndTheLastOneWins() throws Exception {
        JsonNode answer = rpc("{\"JSONRPC\":\"2.0\",\"Id\":7,\"METHOD\":\"nope\",\"method\":\"ping\"}");
        assertEquals(7, answer.get("id").asInt());
        assertTrue(answer.has("result"), answer.toString());
    }

    @Test
    void malformedMessagesGetGosErrors() throws Exception {
        assertEquals(-32700, rpc("{} trailing").at("/error/code").asInt(), "trailing data is not valid JSON, as in Go");
        assertEquals(-32700, rpc("5").at("/error/code").asInt(), "a value that is not an object");
        assertEquals(-32700, rpc("{\"jsonrpc\":2,\"method\":\"ping\",\"id\":1}").at("/error/code").asInt());
        assertEquals(-32600, rpc("null").at("/error/code").asInt(), "null decodes to an empty request");
        assertEquals(-32600, rpc(" \n [1]").at("/error/code").asInt(), "a batch after Go whitespace");
        assertEquals(-32600, rpc("{\"jsonrpc\":null,\"method\":\"ping\",\"id\":1}").at("/error/code").asInt());
        JsonNode noParams = rpc("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":null}");
        assertEquals("invalid params: a tool name is required", noParams.at("/error/message").asText());
        for (String params : List.of("5", "[]", "{\"name\":5}")) {
            JsonNode a = rpc("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":" + params + "}");
            assertEquals(-32602, a.at("/error/code").asInt(), params);
        }
    }

    @Test
    void notificationsAreNeverAnsweredButOtherMethodsStillRun() {
        assertNull(raw("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/cancelled\",\"params\":{}}"));
        assertNull(raw(call("explain_decision", "{\"eventId\":\"e1\"}").replace("\"id\":1,", "")));
        assertEquals(1, backend.calls.size(), "the tool ran even though no answer was sent, as in Go");
        assertNull(raw("{\"jsonrpc\":\"2.0\",\"method\":\"tools/call\",\"params\":{\"name\":\"nope\"}}"), "even an error is not answered");
    }

    @Test
    void argumentErrorsMatchTheGoServiceWordForWord() throws Exception {
        assertEquals("invalid request: argument traceId must be text", toolText("get_recent_events", "{\"traceId\":5}"));
        assertEquals("invalid request: argument limit must be a whole number", toolText("get_recent_events", "{\"LIMIT\":1.5}"));
        assertEquals("invalid request: argument limit must be a whole number", toolText("get_recent_events", "{\"limit\":99999999999999999999}"));
        assertEquals("invalid request: arguments must be an object", toolText("get_recent_events", "[1]"));
        assertEquals("invalid request: argument metadata must be an object", toolText("publish_event", "{\"eventType\":\"a\",\"metadata\":[1]}"));
        assertEquals("invalid request: argument emailRecipients must be a list of text",
                toolText("notify_user", "{\"title\":\"t\",\"message\":\"m\",\"emailRecipients\":\"a@b.c\"}"));
        assertEquals("invalid request: argument emailRecipients must be a list of text",
                toolText("notify_user", "{\"title\":\"t\",\"message\":\"m\",\"emailRecipients\":[\"a@b.c\",5]}"));
        assertEquals("invalid request: unknown argument \"x\"", toolText("explain_decision", "{\"eventId\":\"e\",\"x\":1}"));
        assertEquals("invalid request: unknown argument \"a\\\"b\\n\"", toolText("explain_decision", "{\"a\\\"b\\n\":1}"));
        assertEquals("invalid request: unknown argument \"x\"", toolText("get_recent_events", "{\"x\":1,\"limit\":\"bad\"}"),
                "the first problem in document order wins");
        assertTrue(backend.calls.isEmpty(), "refused arguments never reach the backend");
        assertEquals("\"\\x01\\t\\\\\"", Mcp.Arguments.goQuote("\u0001\t\\"));
    }

    @Test
    void argumentsDecodeAsGoDecodesThem() throws Exception {
        assertFalse(toolError("publish_event", "{\"EventType\":\"a\",\"metadata\":{\"x\":1},\"Metadata\":{\"y\":2},\"severity\":null}"));
        AgentEvents.Input in = (AgentEvents.Input) backend.calls.get(0);
        assertEquals("a", in.eventType());
        assertEquals(Map.of("x", 1, "y", 2), in.metadata(), "a repeated object is merged into the first, as Go decodes into an existing map");
        assertFalse(toolError("publish_event", "{\"eventType\":\"a\",\"metadata\":{\"x\":1},\"metadata\":null}"));
        assertEquals(Map.of(), ((AgentEvents.Input) backend.calls.get(1)).metadata(), "null clears it");
        assertFalse(toolError("publish_event", "null"), "null arguments are an empty step for the backend to refuse");
        assertFalse(toolError("notify_user", "{\"title\":\"t\",\"message\":\"m\",\"emailRecipients\":[\"a@b.c\",null]}"));
        assertEquals(List.of("a@b.c", ""), ((Mcp.Notification) backend.calls.get(3)).emailRecipients(), "a null element is empty text");
        rpc("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"get_recent_events\"}}");
        assertEquals(Map.of("traceId", "", "limit", 20), backend.calls.get(4), "no arguments and limit 0 mean the default");
        assertFalse(toolError("get_recent_events", "{\"limit\":-0}"));
        assertEquals(20, ((Map<?, ?>) backend.calls.get(5)).get("limit"));
        assertEquals("invalid request: limit must be between 1 and 50", toolText("get_recent_events", "{\"limit\":-1}"));
    }

    @Test
    void notificationAndExplainLimitsCountBytesAsGoDoes() throws Exception {
        String title200Bytes = "é".repeat(100); // 100 characters, 200 bytes
        assertFalse(toolError("notify_user", "{\"title\":\"" + title200Bytes + "\",\"message\":\"m\"}"));
        assertEquals("invalid request: title is required (at most 200 characters)",
                toolText("notify_user", "{\"title\":\"" + title200Bytes + "x\",\"message\":\"m\"}"));
        assertEquals("invalid request: message is required (at most 4000 characters)", toolText("notify_user", "{\"title\":\"t\"}"));
        String eleven = "[" + "\"a@b.c\",".repeat(10) + "\"a@b.c\"]";
        assertEquals("invalid request: at most 10 recipients per channel",
                toolText("notify_user", "{\"title\":\"t\",\"message\":\"m\",\"teamsWebhookUrls\":" + eleven + "}"));
        assertEquals("invalid request: eventId is required", toolText("explain_decision", "{\"eventId\":\"" + "é".repeat(101) + "\"}"));
        assertEquals(1, backend.calls.size());
    }

    @Test
    void argumentsAreBoundedAndBackendFailuresAreToolErrors() throws Exception {
        String big = "{\"eventType\":\"a\",\"payload\":{\"x\":\"" + "y".repeat(Mcp.MAX_ARGUMENTS_BYTES) + "\"}}";
        assertEquals("invalid params: the arguments are too large", rpc(call("publish_event", big)).at("/error/message").asText());
        backend.fail = new Mcp.ToolException("the events could not be read");
        assertEquals("the events could not be read", toolText("get_recent_events", "{}"));
        assertTrue(toolError("get_recent_events", "{}"));
    }

    @Test
    void toolResultsAreWrittenWithSortedKeysAsGoWritesAMap() throws Exception {
        Mcp.Server s = new Mcp.Server(new FakeBackend() {
            @Override public Object explain(String eventId) {
                return new java.util.LinkedHashMap<>(Map.of("model", "m", "eventId", eventId, "explanation", "x"));
            }
        }, "v");
        String text = JSON.readTree(s.handle(call("explain_decision", "{\"eventId\":\"e\"}").getBytes(StandardCharsets.UTF_8)))
                .at("/result/content/0/text").asText();
        assertEquals("{\"eventId\":\"e\",\"explanation\":\"x\",\"model\":\"m\"}", text);
    }

    @Test
    void rawJsonHelpers() {
        assertNull(RawJson.members("[1]".getBytes(StandardCharsets.UTF_8)));
        assertNull(RawJson.parse("{".getBytes(StandardCharsets.UTF_8)));
        assertNull(RawJson.field(List.of(), "x"));
        assertEquals("{\"a\":\"\\u2028\\u2029\"}", RawJson.compact("{ \"a\" : \"  \" }".getBytes(StandardCharsets.UTF_8)));
    }
}
