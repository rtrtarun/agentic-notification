package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/dedup"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/notification"
	"github.com/example/notification-service/pkg/policy"
)

var criticalPRRule = policy.Rule{
	Name: "all-prs",
	When: policy.RuleCondition{Source: "github", EventType: "pull_request"},
	Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "always page"},
}

// flakyRepo wraps a repository and can fail Save or Exists.
type flakyRepo struct {
	event.Repository
	saveErr   error
	existsErr error
	saves     int
}

func (f *flakyRepo) Save(ctx context.Context, evt event.Event) error {
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	return f.Repository.Save(ctx, evt)
}

func (f *flakyRepo) Exists(ctx context.Context, id string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	return f.Repository.Exists(ctx, id)
}

type failingProvider struct{ err error }

func (f failingProvider) Send(context.Context, notification.NotificationRequest) error { return f.err }
func (f failingProvider) Name() string                                                 { return "failing" }

func newPipelineWith(repo event.Repository, provider delivery.Provider, rules []policy.Rule) *Pipeline {
	return New(policy.NewEngine(rules), delivery.NewService(provider), repo, Config{}).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestPipelineIgnoresRedeliveredEvent(t *testing.T) {
	p, repo, provider := newTestPipeline([]policy.Rule{criticalPRRule})
	evt := event.Event{ID: "github-pull_request-guid-1", Source: "github", EventType: "pull_request"}

	if _, err := p.Handle(context.Background(), evt); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	_, err := p.Handle(context.Background(), evt)
	if !errors.Is(err, ErrDuplicateDelivery) {
		t.Fatalf("expected ErrDuplicateDelivery, got %v", err)
	}
	if len(provider.sent) != 1 {
		t.Fatalf("redelivery must not notify again, got %d deliveries", len(provider.sent))
	}
	if stored, _ := repo.List(context.Background()); len(stored) != 1 {
		t.Fatalf("redelivery must not be stored twice, got %d", len(stored))
	}
}

func TestPipelineSurfacesExistsFailure(t *testing.T) {
	boom := errors.New("db down")
	repo := &flakyRepo{Repository: event.NewInMemoryRepository(), existsErr: boom}
	p := newPipelineWith(repo, &recordingProvider{}, nil)

	_, err := p.Handle(context.Background(), event.Event{ID: "e1", Source: "github", EventType: "star"})
	if !errors.Is(err, boom) {
		t.Fatalf("expected db error, got %v", err)
	}
	if repo.saves != 0 {
		t.Fatal("must not save when the duplicate check failed")
	}
}

func TestPipelineForgetsDedupEntryWhenSaveFails(t *testing.T) {
	repo := &flakyRepo{Repository: event.NewInMemoryRepository(), saveErr: errors.New("disk full")}
	p := newPipelineWith(repo, &recordingProvider{}, nil).WithDeduper(dedup.NewMemoryDeduper(time.Minute))

	if _, err := p.Handle(context.Background(), prEvt("evt-1")); err == nil {
		t.Fatal("expected save error")
	}

	// GitHub retries; the retry must be processed, not merged into the failed attempt.
	repo.saveErr = nil
	retry, err := p.Handle(context.Background(), prEvt("evt-1-retry"))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retry.DecisionState.Status != event.DecisionQueued || retry.MergedInto != "" {
		t.Fatalf("retry was suppressed as a duplicate: %+v", retry)
	}
}

func TestPipelineForgetsDedupEntryWhenCriticalDeliveryFails(t *testing.T) {
	repo := event.NewInMemoryRepository()
	p := newPipelineWith(repo, failingProvider{err: errors.New("smtp down")}, []policy.Rule{criticalPRRule}).
		WithDeduper(dedup.NewMemoryDeduper(time.Minute))

	if _, err := p.Handle(context.Background(), prEvt("evt-1")); err == nil {
		t.Fatal("expected delivery error")
	}
	if stored, _ := repo.List(context.Background()); len(stored) != 0 {
		t.Fatal("an undelivered critical event must not be stored as handled")
	}

	// The retry must be attempted again rather than recognised as already processed.
	p2 := newPipelineWith(repo, &recordingProvider{}, []policy.Rule{criticalPRRule})
	if _, err := p2.Handle(context.Background(), prEvt("evt-1")); err != nil {
		t.Fatalf("retry after failed delivery must run: %v", err)
	}
}

type erroringDeduper struct{}

func (erroringDeduper) Check(context.Context, event.Event) (dedup.Result, error) {
	return dedup.Result{DedupKey: "k", ThreadID: "t"}, errors.New("redis down")
}
func (erroringDeduper) Forget(context.Context, string, string) error { return errors.New("redis down") }

func TestPipelineFailsOpenWhenDedupStoreIsDown(t *testing.T) {
	p, _, _ := newTestPipeline(nil)
	p.WithDeduper(erroringDeduper{}).WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := p.Handle(context.Background(), prEvt("evt-1"))
	if err != nil {
		t.Fatalf("a dedup outage must not fail the event: %v", err)
	}
	if result.DecisionState.Status != event.DecisionQueued || result.MergedInto != "" {
		t.Fatalf("event must be treated as unique: %+v", result)
	}
}

func TestPipelineCriticalDeliveryFailureIsNotSwallowed(t *testing.T) {
	boom := errors.New("every channel failed")
	p := newPipelineWith(event.NewInMemoryRepository(), failingProvider{err: boom}, []policy.Rule{criticalPRRule})
	_, err := p.Handle(context.Background(), prEvt("evt-1"))
	if !errors.Is(err, boom) {
		t.Fatalf("expected delivery error, got %v", err)
	}
}

func TestPipelineSendsTeamsWebhooksForCritical(t *testing.T) {
	repo := event.NewInMemoryRepository()
	provider := &recordingProvider{}
	p := New(policy.NewEngine([]policy.Rule{criticalPRRule}), delivery.NewService(provider), repo,
		Config{TeamsWebhookURLs: []string{"https://x.webhook.office.com/a"}, SlackWebhookURLs: []string{"https://hooks.slack.com/b"}})

	if _, err := p.Handle(context.Background(), prEvt("evt-1")); err != nil {
		t.Fatalf("handle: %v", err)
	}
	got := provider.sent[0]
	if len(got.TeamsWebhookURLs) != 1 || len(got.SlackWebhookURLs) != 1 {
		t.Fatalf("critical request must carry both webhook channels: %+v", got)
	}
}

func TestWithLoggerIgnoresNil(t *testing.T) {
	p, _, _ := newTestPipeline(nil)
	before := p.logger
	p.WithLogger(nil)
	if p.logger != before {
		t.Fatal("nil logger must not replace the existing one")
	}
}
