// Package mcp implements a Model Context Protocol server: JSON-RPC 2.0 messages
// that let an AI client (Claude Desktop, Cursor) use AgentPulse through four tools.
//
// It is transport-free. A Server turns one request into one response; cmd/mcp-server
// feeds it from stdin and the main service feeds it from POST /api/mcp, so the tool
// list and every tool's behaviour are the same on both. The tools do their work through
// a Backend, which keeps this package free of HTTP and storage.
//
//	publish_event      publish one agent step into the pipeline
//	get_recent_events  read recent events, or one run by trace id
//	notify_user        send a validated notification through the platform
//	explain_decision   say in plain words why an event was handled as it was
//
// A client can only do through these tools what the REST API already lets an API-key
// holder do. Critical events are decided by rules, never by a tool.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/example/notification-service/pkg/agentevents"
)

// ProtocolVersion is the MCP revision this server speaks.
const ProtocolVersion = "2024-11-05"

// Tool names.
const (
	ToolPublishEvent     = "publish_event"
	ToolGetRecentEvents  = "get_recent_events"
	ToolNotifyUser       = "notify_user"
	ToolExplainDecision  = "explain_decision"
	defaultRecentLimit   = 20
	maxRecentLimit       = 50
	maxNotifyTitle       = 200
	maxNotifyMessage     = 4000
	maxNotifyRecipients  = 10
	maxEventIDLength     = 200
	jsonRPCVersion       = "2.0"
	notificationsPrefix  = "notifications/"
	methodInitialize     = "initialize"
	methodPing           = "ping"
	methodToolsList      = "tools/list"
	methodToolsCall      = "tools/call"
	maxArgumentsBytes    = 64 * 1024
	serverName           = "agentpulse"
	invalidParamsMessage = "invalid params"
)

// JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
)

// ErrInvalid is what a Backend returns for input it refuses; the text is shown to the client.
var ErrInvalid = errors.New("invalid request")

// Notification is the input of notify_user.
type Notification struct {
	Title            string   `json:"title"`
	Message          string   `json:"message"`
	Agent            string   `json:"agent"`
	EmailRecipients  []string `json:"emailRecipients"`
	SlackWebhookURLs []string `json:"slackWebhookUrls"`
	TeamsWebhookURLs []string `json:"teamsWebhookUrls"`
}

// Backend is what the tools act on. Results are marshalled to JSON for the client.
type Backend interface {
	PublishEvent(ctx context.Context, in agentevents.Input) (any, error)
	RecentEvents(ctx context.Context, traceID string, limit int) (any, error)
	Notify(ctx context.Context, n Notification) (any, error)
	Explain(ctx context.Context, eventID string) (any, error)
}

// Server answers MCP requests.
type Server struct {
	backend Backend
	version string
}

// New returns a server whose tools act through backend. version is reported to clients.
func New(backend Backend, version string) *Server {
	if version == "" {
		version = "dev"
	}
	return &Server{backend: backend, version: version}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Handle processes one JSON-RPC message. It returns the response bytes, or nil when
// the message was a notification and needs no answer.
func (s *Server) Handle(ctx context.Context, raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return marshal(response{JSONRPC: jsonRPCVersion, ID: json.RawMessage("null"), Error: &rpcError{CodeInvalidRequest, "batches are not supported; send one request at a time"}})
	}
	var req request
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return marshal(response{JSONRPC: jsonRPCVersion, ID: json.RawMessage("null"), Error: &rpcError{CodeParseError, "the message is not valid JSON"}})
	}
	if req.JSONRPC != jsonRPCVersion || req.Method == "" {
		return marshal(response{JSONRPC: jsonRPCVersion, ID: idOrNull(req.ID), Error: &rpcError{CodeInvalidRequest, "not a JSON-RPC 2.0 request"}})
	}
	isNotification := len(req.ID) == 0
	if isNotification && len(req.Method) >= len(notificationsPrefix) && req.Method[:len(notificationsPrefix)] == notificationsPrefix {
		return nil
	}
	result, rerr := s.dispatch(ctx, req)
	if isNotification {
		return nil
	}
	if rerr != nil {
		return marshal(response{JSONRPC: jsonRPCVersion, ID: req.ID, Error: rerr})
	}
	return marshal(response{JSONRPC: jsonRPCVersion, ID: req.ID, Result: result})
}

func (s *Server) dispatch(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case methodInitialize:
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": s.version},
			"instructions":    "AgentPulse decides whether people should be interrupted. Publish each agent step with publish_event, read runs with get_recent_events, and ask why with explain_decision.",
		}, nil
	case methodPing:
		return map[string]any{}, nil
	case methodToolsList:
		return map[string]any{"tools": toolList()}, nil
	case methodToolsCall:
		return s.callTool(ctx, req.Params)
	}
	return nil, &rpcError{CodeMethodNotFound, "unknown method " + req.Method}
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, &rpcError{CodeInvalidParams, invalidParamsMessage + ": a tool name is required"}
	}
	if len(p.Arguments) > maxArgumentsBytes {
		return nil, &rpcError{CodeInvalidParams, invalidParamsMessage + ": the arguments are too large"}
	}
	if len(p.Arguments) == 0 {
		p.Arguments = json.RawMessage("{}")
	}
	var (
		out any
		err error
	)
	switch p.Name {
	case ToolPublishEvent:
		var in agentevents.Input
		if err = decodeStrict(p.Arguments, &in); err == nil {
			out, err = s.backend.PublishEvent(ctx, in)
		}
	case ToolGetRecentEvents:
		var in struct {
			TraceID string `json:"traceId"`
			Limit   int    `json:"limit"`
		}
		if err = decodeStrict(p.Arguments, &in); err == nil {
			if in.Limit == 0 {
				in.Limit = defaultRecentLimit
			}
			if in.Limit < 1 || in.Limit > maxRecentLimit {
				err = fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, maxRecentLimit)
			} else {
				out, err = s.backend.RecentEvents(ctx, in.TraceID, in.Limit)
			}
		}
	case ToolNotifyUser:
		var in Notification
		if err = decodeStrict(p.Arguments, &in); err == nil {
			if err = checkNotification(in); err == nil {
				out, err = s.backend.Notify(ctx, in)
			}
		}
	case ToolExplainDecision:
		var in struct {
			EventID string `json:"eventId"`
		}
		if err = decodeStrict(p.Arguments, &in); err == nil {
			if in.EventID == "" || len(in.EventID) > maxEventIDLength {
				err = fmt.Errorf("%w: eventId is required", ErrInvalid)
			} else {
				out, err = s.backend.Explain(ctx, in.EventID)
			}
		}
	default:
		return nil, &rpcError{CodeInvalidParams, invalidParamsMessage + ": unknown tool " + p.Name}
	}
	if err != nil {
		return toolResult(err.Error(), true), nil
	}
	body, mErr := json.Marshal(out)
	if mErr != nil {
		return toolResult("the result could not be encoded", true), nil
	}
	return toolResult(string(body), false), nil
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isError}
}

// decodeStrict refuses unknown fields, so a misspelt argument is an error the client can fix.
func decodeStrict(raw json.RawMessage, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	return nil
}

func checkNotification(n Notification) error {
	if n.Title == "" || len(n.Title) > maxNotifyTitle {
		return fmt.Errorf("%w: title is required (at most %d characters)", ErrInvalid, maxNotifyTitle)
	}
	if n.Message == "" || len(n.Message) > maxNotifyMessage {
		return fmt.Errorf("%w: message is required (at most %d characters)", ErrInvalid, maxNotifyMessage)
	}
	for _, list := range [][]string{n.EmailRecipients, n.SlackWebhookURLs, n.TeamsWebhookURLs} {
		if len(list) > maxNotifyRecipients {
			return fmt.Errorf("%w: at most %d recipients per channel", ErrInvalid, maxNotifyRecipients)
		}
	}
	return nil
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func marshal(r response) []byte {
	out, err := json.Marshal(r)
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"internal error"}}`)
	}
	return out
}
