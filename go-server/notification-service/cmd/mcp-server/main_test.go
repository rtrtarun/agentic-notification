package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/agentevents"
	"github.com/agentpulse/notification-service/pkg/mcp"
)

type stubBackend struct{}

func (stubBackend) PublishEvent(context.Context, agentevents.Input) (any, error) { return "ok", nil }
func (stubBackend) RecentEvents(context.Context, string, int) (any, error)       { return "ok", nil }
func (stubBackend) Notify(context.Context, mcp.Notification) (any, error)        { return "ok", nil }
func (stubBackend) Explain(context.Context, string) (any, error)                 { return "ok", nil }

func TestServeAnswersEachLineAndSkipsNotificationsAndBlanks(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		``,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`not json`,
	}, "\n")
	var out bytes.Buffer
	if err := serve(context.Background(), strings.NewReader(in), &out, mcp.New(stubBackend{}, "test")); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 responses (initialize, tools/list, parse error), got %d:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"protocolVersion"`) || !strings.Contains(lines[1], `"publish_event"`) || !strings.Contains(lines[2], `-32700`) {
		t.Fatalf("responses = %v", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "\n") {
			t.Fatalf("each response must be one line: %q", l)
		}
	}
}

func TestServeStopsWhenCancelledAndReportsWriteFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := serve(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out, mcp.New(stubBackend{}, "")); err != nil || out.Len() != 0 {
		t.Fatalf("a cancelled serve must stop quietly: %v %q", err, out.String())
	}
	if err := serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), failingWriter{}, mcp.New(stubBackend{}, "")); err == nil {
		t.Fatal("a failed write must be reported")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

func TestServeRefusesAMessageLongerThanTheLimit(t *testing.T) {
	huge := `{"jsonrpc":"2.0","id":1,"method":"ping","params":"` + strings.Repeat("x", maxLine) + `"}`
	if err := serve(context.Background(), strings.NewReader(huge+"\n"), &bytes.Buffer{}, mcp.New(stubBackend{}, "")); err == nil {
		t.Fatal("an oversized line must be an error, not unbounded memory")
	}
}
