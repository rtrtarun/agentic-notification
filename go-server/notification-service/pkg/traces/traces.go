// Package traces turns stored agent events into traces: one run of an agent, with
// its ordered steps, totals and final status. It only reads; it decides nothing and
// calls no model.
//
// An event belongs to a trace when it carries a trace ID. Its telemetry lives in the
// payload's "metadata" object: step_kind (plan, tool_call, retry, reflect,
// human_approval, complete), tool_name, attempt, duration_ms, tokens_in, tokens_out,
// cost_usd and confidence.
package traces

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
)

// Step kinds an agent may report.
const (
	StepPlan          = "plan"
	StepToolCall      = "tool_call"
	StepRetry         = "retry"
	StepReflect       = "reflect"
	StepHumanApproval = "human_approval"
	StepComplete      = "complete"
)

// Trace statuses.
const (
	StatusRunning  = "running"
	StatusWaiting  = "waiting"
	StatusComplete = "complete"
	StatusFailed   = "failed"
)

// maxDepth stops a malformed parent chain (a cycle) from looping.
const maxDepth = 32

// Step is one event in a trace, with the platform's decision for it.
type Step struct {
	EventID        string    `json:"eventId"`
	ParentEventID  string    `json:"parentEventId,omitempty"`
	EventType      string    `json:"eventType"`
	StepKind       string    `json:"stepKind,omitempty"`
	ToolName       string    `json:"toolName,omitempty"`
	Attempt        int       `json:"attempt,omitempty"`
	DurationMs     int64     `json:"durationMs,omitempty"`
	TokensIn       int       `json:"tokensIn,omitempty"`
	TokensOut      int       `json:"tokensOut,omitempty"`
	CostUSD        float64   `json:"costUsd,omitempty"`
	Confidence     *float64  `json:"confidence,omitempty"`
	AttentionClass string    `json:"attentionClass"`
	DecisionStatus string    `json:"decisionStatus,omitempty"`
	DecisionReason string    `json:"decisionReason,omitempty"`
	Depth          int       `json:"depth"`
	At             time.Time `json:"at"`
}

// Summary is a trace without its steps, for lists.
type Summary struct {
	TraceID    string    `json:"traceId"`
	AgentName  string    `json:"agentName,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt"`
	DurationMs int64     `json:"durationMs"`
	Steps      int       `json:"steps"`
	Retries    int       `json:"retries"`
	Failures   int       `json:"failures"`
	TokensIn   int       `json:"tokensIn"`
	TokensOut  int       `json:"tokensOut"`
	CostUSD    float64   `json:"costUsd"`
	// WorstClass is the most serious attention class of any step.
	WorstClass string `json:"worstClass"`
	Status     string `json:"status"`
}

// Trace is a summary and its steps in the order they happened.
type Trace struct {
	Summary
	StepList []Step `json:"stepList"`
}

// Group builds every trace found in the events, most recently active first.
func Group(events []event.Event) []Trace {
	byTrace := map[string][]event.Event{}
	for _, e := range events {
		if e.TraceID != "" {
			byTrace[e.TraceID] = append(byTrace[e.TraceID], e)
		}
	}
	out := make([]Trace, 0, len(byTrace))
	for id, list := range byTrace {
		out = append(out, build(id, list))
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].EndedAt.Equal(out[b].EndedAt) {
			return out[a].EndedAt.After(out[b].EndedAt)
		}
		return out[a].TraceID < out[b].TraceID
	})
	return out
}

// Find builds one trace; the second result is false when no event carries the ID.
func Find(events []event.Event, traceID string) (Trace, bool) {
	var list []event.Event
	for _, e := range events {
		if e.TraceID == traceID && traceID != "" {
			list = append(list, e)
		}
	}
	if len(list) == 0 {
		return Trace{}, false
	}
	return build(traceID, list), true
}

func build(id string, list []event.Event) Trace {
	sort.SliceStable(list, func(a, b int) bool { return list[a].CreatedAt.Before(list[b].CreatedAt) })
	parent := make(map[string]string, len(list))
	for _, e := range list {
		parent[e.ID] = e.ParentEventID
	}
	t := Trace{Summary: Summary{TraceID: id, StartedAt: list[0].CreatedAt, EndedAt: list[len(list)-1].CreatedAt, WorstClass: event.AttentionBackground}}
	var spent int64
	for _, e := range list {
		s := step(e, depth(e.ID, parent))
		t.StepList = append(t.StepList, s)
		if t.AgentName == "" {
			t.AgentName = e.AgentName
		}
		t.Steps++
		spent += s.DurationMs
		t.TokensIn += s.TokensIn
		t.TokensOut += s.TokensOut
		t.CostUSD += s.CostUSD
		if s.StepKind == StepRetry {
			t.Retries++
		}
		if failed(e.EventType) {
			t.Failures++
		}
		if rank(e.AttentionClass) > rank(t.WorstClass) {
			t.WorstClass = e.AttentionClass
		}
	}
	t.CostUSD = math.Round(t.CostUSD*1e6) / 1e6
	t.DurationMs = spent
	if span := t.EndedAt.Sub(t.StartedAt).Milliseconds(); spent == 0 {
		t.DurationMs = span
	}
	t.Status = status(t.StepList)
	return t
}

func step(e event.Event, depth int) Step {
	md, _ := e.Payload["metadata"].(map[string]any)
	s := Step{
		EventID: e.ID, ParentEventID: e.ParentEventID, EventType: e.EventType, AttentionClass: e.AttentionClass,
		Depth: depth, At: e.CreatedAt,
		StepKind:   str(md, "step_kind"),
		ToolName:   str(md, "tool_name"),
		Attempt:    int(num(md, "attempt")),
		DurationMs: int64(num(md, "duration_ms")),
		TokensIn:   int(num(md, "tokens_in")),
		TokensOut:  int(num(md, "tokens_out")),
		CostUSD:    num(md, "cost_usd"),
	}
	if _, ok := md["confidence"]; ok {
		c := num(md, "confidence")
		s.Confidence = &c
	}
	if e.DecisionState != nil {
		s.DecisionStatus, s.DecisionReason = e.DecisionState.Status, e.DecisionState.Reason
	}
	return s
}

// status reads the last step: complete, waiting for a person, failed, or still running.
func status(steps []Step) string {
	last := steps[len(steps)-1]
	switch {
	case last.StepKind == StepComplete:
		return StatusComplete
	case last.StepKind == StepHumanApproval:
		return StatusWaiting
	case failed(last.EventType):
		return StatusFailed
	}
	return StatusRunning
}

func failed(eventType string) bool {
	t := strings.ToLower(eventType)
	return strings.HasSuffix(t, ".failed") || strings.HasSuffix(t, ".failure") || strings.HasSuffix(t, ".error") || strings.HasSuffix(t, "_failed")
}

func depth(id string, parent map[string]string) int {
	d := 0
	for cur := parent[id]; cur != "" && d < maxDepth; cur = parent[cur] {
		if _, known := parent[cur]; !known {
			break // the parent is not in this trace (or was purged): this step is a root
		}
		d++
	}
	return d
}

func rank(class string) int {
	switch class {
	case event.AttentionCritical:
		return 3
	case event.AttentionStandard:
		return 2
	}
	return 1
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func num(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	}
	return 0
}
