package traces

import (
	"testing"
	"time"

	"github.com/example/notification-service/pkg/event"
)

var base = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func mk(id, parent, typ, kind string, at int, class string, md map[string]any) event.Event {
	if md == nil {
		md = map[string]any{}
	}
	md["step_kind"] = kind
	return event.Event{
		ID: id, TraceID: "run-1", ParentEventID: parent, AgentName: "researcher", Source: "agent", EventType: typ,
		AttentionClass: class, CreatedAt: base.Add(time.Duration(at) * time.Second),
		DecisionState: &event.DecisionState{Status: event.DecisionQueued, Reason: "rule " + id},
		Payload:       map[string]any{"metadata": md},
	}
}

func failingRun() []event.Event {
	return []event.Event{
		mk("s1", "", "agent.plan", StepPlan, 0, "background", map[string]any{"duration_ms": 120, "tokens_in": 500, "tokens_out": 80, "cost_usd": 0.004}),
		mk("s2", "s1", "tool.call.failed", StepToolCall, 1, "standard", map[string]any{"tool_name": "search", "attempt": 1, "duration_ms": 30000, "confidence": 0.6}),
		mk("s3", "s2", "agent.retry", StepRetry, 2, "background", map[string]any{"attempt": 2, "duration_ms": 30000}),
		mk("s4", "s3", "agent.retry", StepRetry, 3, "background", map[string]any{"attempt": 3, "duration_ms": 30000}),
		mk("s5", "s4", "agent.reflect", StepReflect, 4, "background", map[string]any{"tokens_in": "300", "tokens_out": 60, "cost_usd": "0.002"}),
		mk("s6", "s5", "agent.complete", StepComplete, 5, "background", nil),
	}
}

func TestATraceSummarisesItsStepsAndTotals(t *testing.T) {
	got, ok := Find(failingRun(), "run-1")
	if !ok {
		t.Fatal("trace not found")
	}
	if got.Steps != 6 || got.Retries != 2 || got.Failures != 1 || got.AgentName != "researcher" {
		t.Fatalf("counts = %+v", got.Summary)
	}
	if got.TokensIn != 800 || got.TokensOut != 140 || got.CostUSD != 0.006 {
		t.Fatalf("totals tokens %d/%d cost %v", got.TokensIn, got.TokensOut, got.CostUSD)
	}
	if got.DurationMs != 90120 {
		t.Fatalf("duration = %d, want the sum of step durations", got.DurationMs)
	}
	if got.WorstClass != event.AttentionStandard || got.Status != StatusComplete {
		t.Fatalf("worst %s status %s", got.WorstClass, got.Status)
	}
	if !got.StartedAt.Equal(base) || !got.EndedAt.Equal(base.Add(5*time.Second)) {
		t.Fatalf("window = %v to %v", got.StartedAt, got.EndedAt)
	}
}

func TestStepsAreOrderedWithTheirParentChainAndDecisions(t *testing.T) {
	events := failingRun()
	events[0], events[3] = events[3], events[0] // arrival order must not matter
	got, _ := Find(events, "run-1")
	var ids []string
	for _, s := range got.StepList {
		ids = append(ids, s.EventID)
	}
	if want := "s1,s2,s3,s4,s5,s6"; join(ids) != want {
		t.Fatalf("order = %s, want %s", join(ids), want)
	}
	for i, s := range got.StepList {
		if s.Depth != i {
			t.Fatalf("step %s depth = %d, want %d (a straight chain)", s.EventID, s.Depth, i)
		}
	}
	s2 := got.StepList[1]
	if s2.ToolName != "search" || s2.Attempt != 1 || s2.Confidence == nil || *s2.Confidence != 0.6 || s2.DecisionReason != "rule s2" || s2.DecisionStatus != event.DecisionQueued {
		t.Fatalf("step detail = %+v", s2)
	}
	if got.StepList[0].Confidence != nil {
		t.Fatal("a step with no confidence must not invent one")
	}
}

func join(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}

func TestStatusFollowsTheLastStep(t *testing.T) {
	cases := map[string]struct {
		last event.Event
		want string
	}{
		"complete":   {mk("x", "", "agent.complete", StepComplete, 9, "background", nil), StatusComplete},
		"approval":   {mk("x", "", "agent.approval", StepHumanApproval, 9, "standard", nil), StatusWaiting},
		"failed":     {mk("x", "", "tool.call.failed", StepToolCall, 9, "standard", nil), StatusFailed},
		"errored":    {mk("x", "", "run.error", StepToolCall, 9, "standard", nil), StatusFailed},
		"in flight":  {mk("x", "", "tool.call.started", StepToolCall, 9, "background", nil), StatusRunning},
		"retrying":   {mk("x", "", "agent.retry", StepRetry, 9, "background", nil), StatusRunning},
		"underscore": {mk("x", "", "job_failed", StepToolCall, 9, "standard", nil), StatusFailed},
	}
	for name, c := range cases {
		got, _ := Find([]event.Event{c.last}, "run-1")
		if got.Status != c.want {
			t.Errorf("%s: status %s, want %s", name, got.Status, c.want)
		}
	}
}

func TestWorstClassIsCriticalWhenAnyStepIs(t *testing.T) {
	events := failingRun()
	events[2].AttentionClass = event.AttentionCritical
	got, _ := Find(events, "run-1")
	if got.WorstClass != event.AttentionCritical {
		t.Fatalf("worst = %s", got.WorstClass)
	}
	onlyQuiet, _ := Find([]event.Event{mk("a", "", "agent.plan", StepPlan, 0, "background", nil)}, "run-1")
	if onlyQuiet.WorstClass != event.AttentionBackground {
		t.Fatalf("worst = %s", onlyQuiet.WorstClass)
	}
}

func TestGroupSeparatesTracesIgnoresOtherEventsAndSortsNewestFirst(t *testing.T) {
	old := mk("o1", "", "agent.plan", StepPlan, 0, "background", nil)
	old.TraceID = "old-run"
	recent := mk("r1", "", "agent.plan", StepPlan, 100, "background", nil)
	recent.TraceID = "new-run"
	webhook := event.Event{ID: "w", Source: "github", EventType: "push", CreatedAt: base.Add(500 * time.Second)}
	got := Group([]event.Event{old, webhook, recent})
	if len(got) != 2 || got[0].TraceID != "new-run" || got[1].TraceID != "old-run" {
		t.Fatalf("traces = %+v", got)
	}
	if none := Group([]event.Event{webhook}); len(none) != 0 {
		t.Fatalf("an event with no trace ID is not a trace: %+v", none)
	}
}

func TestFindReportsAMissingTrace(t *testing.T) {
	if _, ok := Find(failingRun(), "nope"); ok {
		t.Fatal("unknown trace found")
	}
	if _, ok := Find(failingRun(), ""); ok {
		t.Fatal("an empty ID must not match the events that have no trace")
	}
}

func TestDurationFallsBackToTheTimeSpanWhenStepsReportNone(t *testing.T) {
	a := mk("a", "", "agent.plan", StepPlan, 0, "background", nil)
	b := mk("b", "a", "agent.complete", StepComplete, 7, "background", nil)
	got, _ := Find([]event.Event{a, b}, "run-1")
	if got.DurationMs != 7000 {
		t.Fatalf("duration = %d", got.DurationMs)
	}
}

func TestAMalformedParentChainNeitherLoopsNorFails(t *testing.T) {
	a := mk("a", "b", "agent.plan", StepPlan, 0, "background", nil)
	b := mk("b", "a", "agent.reflect", StepReflect, 1, "background", nil)
	orphan := mk("c", "purged-event", "agent.retry", StepRetry, 2, "background", nil)
	got, _ := Find([]event.Event{a, b, orphan}, "run-1")
	if got.Steps != 3 {
		t.Fatalf("steps = %d", got.Steps)
	}
	if got.StepList[2].Depth != 0 {
		t.Fatalf("a step whose parent is not in the trace is a root, depth = %d", got.StepList[2].Depth)
	}
	for _, s := range got.StepList[:2] {
		if s.Depth > maxDepth {
			t.Fatalf("depth %d beyond the cap", s.Depth)
		}
	}
}

func TestTelemetryAcceptsNumbersAsStringsAndIgnoresJunk(t *testing.T) {
	e := mk("a", "", "agent.plan", StepPlan, 0, "background", map[string]any{"tokens_in": "12", "tokens_out": "lots", "attempt": int64(2), "duration_ms": 5, "cost_usd": nil})
	got, _ := Find([]event.Event{e}, "run-1")
	s := got.StepList[0]
	if s.TokensIn != 12 || s.TokensOut != 0 || s.Attempt != 2 || s.DurationMs != 5 || s.CostUSD != 0 {
		t.Fatalf("step = %+v", s)
	}
	bare := event.Event{ID: "b", TraceID: "run-2", CreatedAt: base}
	if got, _ := Find([]event.Event{bare}, "run-2"); got.Steps != 1 || got.StepList[0].StepKind != "" {
		t.Fatalf("an event with no metadata still forms a trace: %+v", got)
	}
}
