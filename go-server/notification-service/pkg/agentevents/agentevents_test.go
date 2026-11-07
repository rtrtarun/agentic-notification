package agentevents

import (
	"errors"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/event"
)

func valid() Input {
	return Input{
		ID: "run-1-step-2", EventType: "tool.call.failed", TraceID: "run-1", ParentEventID: "run-1-step-1", AgentName: "researcher",
		Severity: "High", Repository: "acme/api",
		Metadata: map[string]any{"step_kind": "tool_call", "tool_name": "search", "attempt": float64(2), "duration_ms": float64(30000), "confidence": 0.6},
		Payload:  map[string]any{"error": "timeout"},
	}
}

func TestNormalizeBuildsTheEventAndKeepsTelemetryWhereRulesCanSeeIt(t *testing.T) {
	e, err := Normalize(valid())
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "run-1-step-2" || e.Source != "agent" || e.EventType != "tool.call.failed" || e.TraceID != "run-1" || e.ParentEventID != "run-1-step-1" || e.AgentName != "researcher" || e.Repository != "acme/api" {
		t.Fatalf("event = %+v", e)
	}
	md, _ := e.Payload["metadata"].(map[string]any)
	if md["tool_name"] != "search" || md["attempt"] != float64(2) {
		t.Fatalf("metadata = %v", e.Payload["metadata"])
	}
	if e.Payload["severity"] != "high" || e.Payload["agentName"] != "researcher" || e.Payload["error"] != "timeout" {
		t.Fatalf("payload = %v", e.Payload)
	}
}

func TestNormalizeNeverSetsTheAttentionClass(t *testing.T) {
	in := valid()
	in.Severity = "critical"
	e, err := Normalize(in)
	if err != nil {
		t.Fatal(err)
	}
	if e.AttentionClass != "" || e.AttentionClass == event.AttentionCritical {
		t.Fatalf("an agent cannot declare its own event critical; only a rule decides the class, got %q", e.AttentionClass)
	}
}

func TestNormalizeDoesNotShareMapsWithTheCaller(t *testing.T) {
	in := valid()
	e, _ := Normalize(in)
	e.Payload["error"] = "changed"
	e.Payload["metadata"].(map[string]any)["tool_name"] = "changed"
	if in.Payload["error"] != "timeout" || in.Metadata["tool_name"] != "search" {
		t.Fatal("the caller's maps were modified")
	}
}

func TestNormalizeDefaultsAndGeneratesIDs(t *testing.T) {
	a, err := Normalize(Input{EventType: "agent.plan"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Normalize(Input{EventType: "agent.plan"})
	if a.Source != "agent" || !strings.HasPrefix(a.ID, "agent-") || a.ID == b.ID {
		t.Fatalf("ids must be generated and unique: %q %q", a.ID, b.ID)
	}
	if c, _ := Normalize(Input{EventType: "x", Source: "  Claude-Code "}); c.Source != "claude-code" {
		t.Fatalf("source = %q", c.Source)
	}
}

func TestNormalizeRejectsBadInput(t *testing.T) {
	cases := map[string]func(*Input){
		"no event type":              func(i *Input) { i.EventType = "" },
		"event type with spaces":     func(i *Input) { i.EventType = "tool call" },
		"source with capitals gone":  func(i *Input) { i.Source = "bad source!" },
		"id with a slash":            func(i *Input) { i.ID = "a/b" },
		"trace id too long":          func(i *Input) { i.TraceID = strings.Repeat("a", 201) },
		"parent without a trace":     func(i *Input) { i.TraceID = "" },
		"its own parent":             func(i *Input) { i.ParentEventID = i.ID },
		"unknown severity":           func(i *Input) { i.Severity = "apocalyptic" },
		"unknown step kind":          func(i *Input) { i.Metadata["step_kind"] = "dance" },
		"step kind not text":         func(i *Input) { i.Metadata["step_kind"] = 3 },
		"negative attempt":           func(i *Input) { i.Metadata["attempt"] = float64(-1) },
		"attempt as text":            func(i *Input) { i.Metadata["attempt"] = "many" },
		"negative cost":              func(i *Input) { i.Metadata["cost_usd"] = -0.5 },
		"confidence above one":       func(i *Input) { i.Metadata["confidence"] = 1.5 },
		"confidence as text":         func(i *Input) { i.Metadata["confidence"] = "high" },
		"a very long metadata value": func(i *Input) { i.Metadata["note"] = strings.Repeat("x", 501) },
		"too much metadata": func(i *Input) {
			for k := 0; k < 31; k++ {
				i.Metadata[strings.Repeat("k", k+1)] = 1
			}
		},
		"agent name too long": func(i *Input) { i.AgentName = strings.Repeat("a", 101) },
	}
	for name, mutate := range cases {
		in := valid()
		mutate(&in)
		if _, err := Normalize(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestNormalizeAcceptsEveryStepKindAndIntegerTelemetry(t *testing.T) {
	for _, kind := range []string{"plan", "tool_call", "retry", "reflect", "human_approval", "complete"} {
		in := Input{EventType: "agent.step", Metadata: map[string]any{"step_kind": kind, "tokens_in": 12, "tokens_out": int64(7), "confidence": 0}}
		if _, err := Normalize(in); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
}
