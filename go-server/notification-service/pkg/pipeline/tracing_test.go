package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/attention"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/telemetry"
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
	if _, err := p.Handle(context.Background(), noisy("dup")); err != ErrDuplicateDelivery {
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
