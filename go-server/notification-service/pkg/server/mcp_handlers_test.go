package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func rpcCall(t *testing.T, s *Server, body string) (int, map[string]any) {
	t.Helper()
	rr := call(s, http.MethodPost, "/api/mcp", body, withKey)
	var out map[string]any
	if rr.Body.Len() > 0 {
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("not JSON: %s", rr.Body.String())
		}
	}
	return rr.Code, out
}

func tool(name, args string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

func toolOutput(t *testing.T, out map[string]any) (string, bool) {
	t.Helper()
	res, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", out)
	}
	return res["content"].([]any)[0].(map[string]any)["text"].(string), res["isError"].(bool)
}

func TestTheMCPEndpointPublishesReadsAndExplainsThroughTheSamePipeline(t *testing.T) {
	s := agentServer(t)
	code, out := rpcCall(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if code != http.StatusOK || len(out["result"].(map[string]any)["tools"].([]any)) != 4 {
		t.Fatalf("tools/list = %d %v", code, out)
	}

	text, isErr := toolOutput(t, func() map[string]any {
		_, o := rpcCall(t, s, tool("publish_event", `{"id":"m1","traceId":"mcp-run","eventType":"tool.call.failed","severity":"critical","metadata":{"step_kind":"tool_call","attempt":5}}`))
		return o
	}())
	if isErr || !strings.Contains(text, `"eventId":"m1"`) || strings.Contains(text, `"attentionClass":"critical"`) {
		t.Fatalf("publish = %s %v", text, isErr)
	}
	// The retry rule from agentServer fired, exactly as it would for REST: the event went through the same pipeline.
	if !strings.Contains(text, "an agent keeps retrying") {
		t.Fatalf("the rules must apply to events published over MCP: %s", text)
	}
	if text, _ := toolOutput(t, func() map[string]any {
		_, o := rpcCall(t, s, tool("publish_event", `{"id":"m1","traceId":"mcp-run","eventType":"tool.call.failed"}`))
		return o
	}()); !strings.Contains(text, `"status":"duplicate"`) {
		t.Fatalf("a repeated id must be a duplicate: %s", text)
	}

	_, o := rpcCall(t, s, tool("get_recent_events", `{"traceId":"mcp-run","limit":5}`))
	text, isErr = toolOutput(t, o)
	if isErr || !strings.Contains(text, `"traceId":"mcp-run"`) || !strings.Contains(text, `"m1"`) {
		t.Fatalf("recent = %s", text)
	}
	_, o = rpcCall(t, s, tool("get_recent_events", `{"traceId":"other-run"}`))
	if text, _ := toolOutput(t, o); strings.Contains(text, `"m1"`) {
		t.Fatalf("the trace filter must apply: %s", text)
	}

	_, o = rpcCall(t, s, tool("explain_decision", `{"eventId":"m1"}`))
	text, isErr = toolOutput(t, o)
	if isErr || !strings.Contains(text, "explanation") || !strings.Contains(text, `"model":"heuristic-v1"`) {
		t.Fatalf("explain = %s %v", text, isErr)
	}
	_, o = rpcCall(t, s, tool("explain_decision", `{"eventId":"missing"}`))
	if text, isErr := toolOutput(t, o); !isErr || !strings.Contains(text, "event not found") {
		t.Fatalf("explain missing = %s %v", text, isErr)
	}
}

func TestTheMCPNotifyToolIsValidatedLikeTheRESTEndpoint(t *testing.T) {
	s := agentServer(t)
	_, o := rpcCall(t, s, tool("notify_user", `{"title":"x","message":"y","slackWebhookUrls":["https://evil.example.com/hook"]}`))
	text, isErr := toolOutput(t, o)
	if !isErr || strings.Contains(strings.ToLower(text), "could not be sent") {
		t.Fatalf("a host outside the allow-list must be refused with a reason: %s %v", text, isErr)
	}
	if rest := call(s, http.MethodPost, "/notify", `{"title":"x","message":"y","slackWebhookUrls":["https://evil.example.com/hook"]}`, withKey); rest.Code != http.StatusBadRequest {
		t.Fatalf("the REST endpoint refuses it too: %d", rest.Code)
	}
}

func TestTheMCPEndpointNeedsAuthAnswersNotificationsQuietlyAndRefusesBadMessages(t *testing.T) {
	s := agentServer(t)
	if rr := call(s, http.MethodPost, "/api/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rr.Code)
	}
	if rr := call(s, http.MethodGet, "/api/mcp", "", withKey); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", rr.Code)
	}
	if code, _ := rpcCall(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != http.StatusAccepted {
		t.Fatalf("a notification = %d, want 202", code)
	}
	if code, out := rpcCall(t, s, `{`); code != http.StatusOK || out["error"].(map[string]any)["code"] != float64(-32700) {
		t.Fatalf("parse error = %d %v", code, out)
	}
	if _, out := rpcCall(t, s, tool("publish_event", `{"eventType":"x","metadata":{"step_kind":"dance"}}`)); out["result"] == nil {
		t.Fatalf("a bad step kind is a tool error: %v", out)
	} else if _, isErr := toolOutput(t, out); !isErr {
		t.Fatal("a bad step kind must be flagged as an error")
	}
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":"` + strings.Repeat("a", int(s.cfg.MaxBodyBytes)+10) + `"}`
	if rr := call(s, http.MethodPost, "/api/mcp", big, withKey); rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized body = %d", rr.Code)
	}
}
