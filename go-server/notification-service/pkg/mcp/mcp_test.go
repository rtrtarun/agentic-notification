package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/agentevents"
)

type fakeBackend struct {
	published []agentevents.Input
	recent    []string
	notified  []Notification
	explained []string
	err       error
}

func (f *fakeBackend) PublishEvent(_ context.Context, in agentevents.Input) (any, error) {
	f.published = append(f.published, in)
	return map[string]any{"eventId": "e1", "attentionClass": "standard"}, f.err
}

func (f *fakeBackend) RecentEvents(_ context.Context, traceID string, limit int) (any, error) {
	f.recent = append(f.recent, traceID+":"+string(rune('0'+limit/10))+string(rune('0'+limit%10)))
	return []string{"e1"}, f.err
}

func (f *fakeBackend) Notify(_ context.Context, n Notification) (any, error) {
	f.notified = append(f.notified, n)
	return map[string]string{"status": "accepted"}, f.err
}

func (f *fakeBackend) Explain(_ context.Context, id string) (any, error) {
	f.explained = append(f.explained, id)
	return map[string]string{"explanation": "because"}, f.err
}

func rpc(t *testing.T, s *Server, msg string) map[string]any {
	t.Helper()
	out := s.Handle(context.Background(), []byte(msg))
	if out == nil {
		t.Fatalf("no response to %s", msg)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("response is not JSON: %s", out)
	}
	if m["jsonrpc"] != "2.0" {
		t.Fatalf("not a JSON-RPC 2.0 response: %s", out)
	}
	return m
}

func errorCode(m map[string]any) float64 {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return c
}

func toolText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	content := res["content"].([]any)[0].(map[string]any)
	return content["text"].(string), res["isError"].(bool)
}

func call(name, args string) string {
	return `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

func TestInitializeAndPing(t *testing.T) {
	s := New(&fakeBackend{}, "1.2.3")
	m := rpc(t, s, `{"jsonrpc":"2.0","id":"abc","method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	res := m["result"].(map[string]any)
	info := res["serverInfo"].(map[string]any)
	if m["id"] != "abc" || res["protocolVersion"] != ProtocolVersion || info["name"] != "agentpulse" || info["version"] != "1.2.3" {
		t.Fatalf("initialize = %v", m)
	}
	if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatal("tools capability missing")
	}
	if p := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); p["id"] != float64(1) || p["error"] != nil {
		t.Fatalf("ping = %v", p)
	}
	if New(&fakeBackend{}, "").version != "dev" {
		t.Fatal("version should default")
	}
}

func TestNotificationsGetNoAnswer(t *testing.T) {
	s := New(&fakeBackend{}, "")
	for _, msg := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}`,
		`{"jsonrpc":"2.0","method":"ping"}`,
	} {
		if out := s.Handle(context.Background(), []byte(msg)); out != nil {
			t.Fatalf("%s must not be answered: %s", msg, out)
		}
	}
}

func TestToolsListHasTheFourToolsWithStrictSchemas(t *testing.T) {
	m := rpc(t, New(&fakeBackend{}, ""), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := m["result"].(map[string]any)["tools"].([]any)
	got := map[string]map[string]any{}
	for _, raw := range tools {
		tl := raw.(map[string]any)
		got[tl["name"].(string)] = tl
	}
	for _, name := range []string{"publish_event", "get_recent_events", "notify_user", "explain_decision"} {
		tl, ok := got[name]
		if !ok {
			t.Fatalf("tool %s is missing", name)
		}
		schema := tl["inputSchema"].(map[string]any)
		if tl["description"] == "" || schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("%s: %v", name, tl)
		}
	}
	if len(got) != 4 {
		t.Fatalf("exactly four tools expected, got %d", len(got))
	}
	if req := got["publish_event"]["inputSchema"].(map[string]any)["required"].([]any); req[0] != "eventType" {
		t.Fatalf("required = %v", req)
	}
	if !strings.Contains(got["publish_event"]["description"].(string), "cannot make an event critical") {
		t.Fatal("the description must say a severity cannot make an event critical")
	}
}

func TestPublishEventPassesTheStepToTheBackend(t *testing.T) {
	b := &fakeBackend{}
	m := rpc(t, New(b, ""), call("publish_event", `{"eventType":"tool.call.failed","traceId":"run-1","parentEventId":"p","severity":"high","metadata":{"attempt":2}}`))
	text, isErr := toolText(t, m)
	if isErr || !strings.Contains(text, `"eventId":"e1"`) {
		t.Fatalf("result = %s %v", text, isErr)
	}
	if len(b.published) != 1 || b.published[0].TraceID != "run-1" || b.published[0].ParentEventID != "p" || b.published[0].Severity != "high" || b.published[0].Metadata["attempt"] != float64(2) {
		t.Fatalf("published = %+v", b.published)
	}
	if m["id"] != float64(7) {
		t.Fatalf("the request id must be echoed: %v", m["id"])
	}
}

func TestGetRecentEventsDefaultsAndBoundsTheLimit(t *testing.T) {
	b := &fakeBackend{}
	s := New(b, "")
	rpc(t, s, call("get_recent_events", `{}`))
	rpc(t, s, call("get_recent_events", `{"traceId":"run-1","limit":5}`))
	if len(b.recent) != 2 || b.recent[0] != ":20" || b.recent[1] != "run-1:05" {
		t.Fatalf("calls = %v", b.recent)
	}
	for _, limit := range []string{"-1", "51", "1000"} {
		text, isErr := toolText(t, rpc(t, s, call("get_recent_events", `{"limit":`+limit+`}`)))
		if !isErr || !strings.Contains(text, "limit must be between") {
			t.Fatalf("limit %s: %s %v", limit, text, isErr)
		}
	}
	if len(b.recent) != 2 {
		t.Fatal("a refused call must not reach the backend")
	}
}

func TestNotifyUserValidatesBeforeTheBackendSeesIt(t *testing.T) {
	b := &fakeBackend{}
	s := New(b, "")
	text, isErr := toolText(t, rpc(t, s, call("notify_user", `{"title":"Deploy done","message":"ok","emailRecipients":["a@corp.com"]}`)))
	if isErr || !strings.Contains(text, "accepted") || len(b.notified) != 1 || b.notified[0].EmailRecipients[0] != "a@corp.com" {
		t.Fatalf("%s %v %+v", text, isErr, b.notified)
	}
	many := `["a@corp.com","b@corp.com","c@corp.com","d@corp.com","e@corp.com","f@corp.com","g@corp.com","h@corp.com","i@corp.com","j@corp.com","k@corp.com"]`
	for name, args := range map[string]string{
		"no title":        `{"message":"x"}`,
		"no message":      `{"title":"x"}`,
		"long title":      `{"title":"` + strings.Repeat("t", 201) + `","message":"x"}`,
		"long message":    `{"title":"x","message":"` + strings.Repeat("m", 4001) + `"}`,
		"too many emails": `{"title":"x","message":"y","emailRecipients":` + many + `}`,
		"unknown field":   `{"title":"x","message":"y","smsRecipients":["+1"]}`,
	} {
		if _, isErr := toolText(t, rpc(t, s, call("notify_user", args))); !isErr {
			t.Errorf("%s: expected a tool error", name)
		}
	}
	if len(b.notified) != 1 {
		t.Fatalf("refused calls reached the backend: %d", len(b.notified))
	}
}

func TestExplainDecisionNeedsAnEventID(t *testing.T) {
	b := &fakeBackend{}
	s := New(b, "")
	if text, isErr := toolText(t, rpc(t, s, call("explain_decision", `{"eventId":"e9"}`))); isErr || !strings.Contains(text, "because") || b.explained[0] != "e9" {
		t.Fatalf("%s %v", text, isErr)
	}
	for _, args := range []string{`{}`, `{"eventId":""}`, `{"eventId":"` + strings.Repeat("x", 201) + `"}`} {
		if _, isErr := toolText(t, rpc(t, s, call("explain_decision", args))); !isErr {
			t.Errorf("%s: expected a tool error", args)
		}
	}
}

func TestMisspeltArgumentsAreToolErrorsNotSilentlyIgnored(t *testing.T) {
	b := &fakeBackend{}
	text, isErr := toolText(t, rpc(t, New(b, ""), call("publish_event", `{"eventTpye":"x"}`)))
	if !isErr || !strings.Contains(text, "unknown field") || len(b.published) != 0 {
		t.Fatalf("%s %v %d", text, isErr, len(b.published))
	}
}

func TestABackendFailureIsAToolErrorTheClientCanRead(t *testing.T) {
	b := &fakeBackend{err: errors.New("invalid request: eventType is required")}
	text, isErr := toolText(t, rpc(t, New(b, ""), call("publish_event", `{"eventType":"x"}`)))
	if !isErr || !strings.Contains(text, "eventType is required") {
		t.Fatalf("%s %v", text, isErr)
	}
}

func TestProtocolErrors(t *testing.T) {
	s := New(&fakeBackend{}, "")
	cases := map[string]struct {
		msg  string
		code float64
	}{
		"not json":        {`{`, CodeParseError},
		"a batch":         {`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, CodeInvalidRequest},
		"wrong version":   {`{"jsonrpc":"1.0","id":1,"method":"ping"}`, CodeInvalidRequest},
		"no method":       {`{"jsonrpc":"2.0","id":1}`, CodeInvalidRequest},
		"unknown method":  {`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, CodeMethodNotFound},
		"no tool name":    {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`, CodeInvalidParams},
		"params not json": {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"x"}`, CodeInvalidParams},
		"unknown tool":    {call("delete_everything", `{}`), CodeInvalidParams},
		"huge arguments":  {call("publish_event", `{"eventType":"x","payload":{"a":"`+strings.Repeat("a", 70000)+`"}}`), CodeInvalidParams},
	}
	for name, c := range cases {
		m := rpc(t, s, c.msg)
		if errorCode(m) != c.code {
			t.Errorf("%s: code %v, want %v (%v)", name, errorCode(m), c.code, m)
		}
	}
}

func TestMissingArgumentsMeanAnEmptyObject(t *testing.T) {
	b := &fakeBackend{}
	if _, isErr := toolText(t, rpc(t, New(b, ""), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_recent_events"}}`)); isErr || len(b.recent) != 1 {
		t.Fatal("a tool with no required arguments works without them")
	}
}
