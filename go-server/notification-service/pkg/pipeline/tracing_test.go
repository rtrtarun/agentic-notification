package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/policy"
	"github.com/agentpulse/notification-service/pkg/telemetry"
)

func spanNames(log string) []string {
	var names []string
	for _, m := range regexp.MustCompile(`name=(\S+)`).FindAllStringSubmatch(log, -1) {
		names = append(names, m[1])
	}
	return names
}

func tracedPipeline(rules string) (*Pipeline, *bytes.Buffer) {
	var buf bytes.Buffer
	tracer := telemetry.NewTracer(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	p, _, _ := newTestPipeline(adaptiveRules(true))
	_ = rules
	return p.WithTracer(tracer).WithClassifier(attention.NewHeuristic()).WithLogger(quiet), &buf
}

func TestEachStepOfAnEventIsASpanInTheCallersTrace(t *testing.T) {
	p, buf := tracedPipeline("")
	caller := telemetry.SpanContext{TraceID: telemetry.TraceID{9}, SpanID: telemetry.SpanID{7}, Sampled: true}
	ctx := telemetry.WithSpanContext(context.Background(), caller)
	if _, err := p.Handle(ctx, noisy("e1")); err != nil {
		t.Fatal(err)
	}
	names := strings.Join(spanNames(buf.String()), ",")
	for _, want := range []string{"policy.Evaluate", "dedup.Check", "attention.Classify", "pipeline.Handle"} {
		if !strings.Contains(names, want) {
			t.Errorf("missing span %s in %s", want, names)
		}
	}
	traceHex := "09000000000000000000000000000000"
	if got := strings.Count(buf.String(), "trace_id="+traceHex); got != len(spanNames(buf.String())) {
		t.Fatalf("every span must join the caller's trace, %d of %d did:\n%s", got, len(spanNames(buf.String())), buf.String())
	}
}

func TestCriticalDeliveryIsASpanAndAClassifierIsNotCalledForIt(t *testing.T) {
	p, buf := tracedPipeline("")
	if _, err := p.Handle(context.Background(), event.Event{ID: "sec-1", Source: "github", EventType: "security_advisory"}); err != nil {
		t.Fatal(err)
	}
	names := strings.Join(spanNames(buf.String()), ",")
	if !strings.Contains(names, "delivery.Send") {
		t.Fatalf("critical delivery should be traced: %s", names)
	}
	if strings.Contains(names, "attention.Classify") {
		t.Fatalf("a critical event must never reach attention, even as a span: %s", names)
	}
}

func TestARedeliveryIsNotRecordedAsAFailedSpan(t *testing.T) {
	p, buf := tracedPipeline("")
	if _, err := p.Handle(context.Background(), noisy("dup")); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if _, err := p.Handle(context.Background(), noisy("dup")); !errors.Is(err, ErrDuplicateDelivery) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(buf.String(), "error=") {
		t.Fatalf("a duplicate is answered, not failed:\n%s", buf.String())
	}
}

func TestAPipelineWithoutATracerWorksAsBefore(t *testing.T) {
	p, _, _ := newTestPipeline(adaptiveRules(true))
	if _, err := p.Handle(context.Background(), noisy("e1")); err != nil {
		t.Fatal(err)
	}
}

// exportCapture keeps exported spans in memory.
type exportCapture struct{ spans []telemetry.SpanData }

func (c *exportCapture) Export(s telemetry.SpanData) { c.spans = append(c.spans, s) }

// TestExportedSpansNameTheDecisionButNeverThePayload (milestone 39c): the pipeline span carries
// the source, type, class, status, rule and agent trace, and nothing from the payload.
func TestExportedSpansNameTheDecisionButNeverThePayload(t *testing.T) {
	out := &exportCapture{}
	p, _ := tracedPipeline("")
	p.WithTracer(telemetry.NewTracer(quiet).WithExporter(out))
	evt := noisy("e-secret")
	evt.TraceID = "run-7"
	evt.Payload = map[string]any{"token": "ghp_supersecret", "pusher": map[string]any{"email": "dev@example.com"}}
	if _, err := p.Handle(context.Background(), evt); err != nil {
		t.Fatal(err)
	}
	var handle *telemetry.SpanData
	for i := range out.spans {
		if out.spans[i].Name == "pipeline.Handle" {
			handle = &out.spans[i]
		}
		for _, a := range out.spans[i].Attributes {
			if strings.Contains(a.Value, "ghp_supersecret") || strings.Contains(a.Value, "dev@example.com") {
				t.Fatalf("payload leaked into span %s: %+v", out.spans[i].Name, a)
			}
		}
	}
	if handle == nil {
		t.Fatal("no pipeline span exported")
	}
	got := map[string]string{}
	for _, a := range handle.Attributes {
		got[a.Key] = a.Value
	}
	if got[telemetry.AttrEventSource] != evt.Source || got[telemetry.AttrEventType] != evt.EventType ||
		got[telemetry.AttrAttentionClass] == "" || got[telemetry.AttrDecisionStatus] == "" || got[telemetry.AttrAgentTraceID] != "run-7" {
		t.Fatalf("attributes %v", got)
	}
}

// TestACollectorThatHangsNeverDelaysACriticalEvent (milestone 39c invariant): spans go to a
// bounded buffer, so a collector that never answers cannot hold up inline critical delivery.
func TestACollectorThatHangsNeverDelaysACriticalEvent(t *testing.T) {
	release := make(chan struct{})
	collector := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer collector.Close()
	defer close(release)
	exporter, err := telemetry.NewOTLPExporter(telemetry.OTLPConfig{Endpoint: collector.URL, BatchSize: 1, BufferSize: 4, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	exporter.Start()
	p, _, provider := newTestPipeline([]policy.Rule{{
		Name: "security", When: policy.RuleCondition{Source: "github", EventType: "security_advisory"},
		Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "security vulnerability"},
	}})
	p.WithTracer(telemetry.NewTracer(quiet).WithExporter(exporter))
	start := time.Now()
	for i := 0; i < 20; i++ {
		out, err := p.Handle(context.Background(), event.Event{ID: fmt.Sprintf("sec-%d", i), Source: "github", EventType: "security_advisory"})
		if err != nil || out.DecisionState.Status != event.DecisionSent {
			t.Fatalf("%v %+v", err, out.DecisionState)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("20 critical events took %s with a hanging collector", elapsed)
	}
	if len(provider.sent) != 20 {
		t.Fatalf("delivered %d of 20", len(provider.sent))
	}
	if _, dropped, _ := exporter.Stats(); dropped == 0 {
		t.Fatal("with the collector stuck, the full buffer must drop spans rather than wait")
	}
}
