package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/notification"
	"github.com/example/notification-service/pkg/sms"
)

type scriptedProvider struct {
	mu    sync.Mutex
	reqs  []notification.NotificationRequest
	errs  []error // returned in order, then nil
	calls int
}

func (f *scriptedProvider) Name() string { return "fake" }
func (f *scriptedProvider) Send(_ context.Context, r notification.NotificationRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
	f.calls++
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup(p Provider) (*Dispatcher, *Outbox, *MemoryStore, *clock) {
	store := NewMemoryStore()
	targets := Targets{Slack: []string{"https://hooks.slack.com/services/SECRET"}, SMS: []string{"+14155550100"}}
	clk := &clock{t: t0}
	out := NewOutbox(store, targets, map[string][]string{"standard": {ChannelSlack, ChannelSMS}})
	out.now = clk.now
	d := NewDispatcher(store, targets, map[string]Provider{ChannelSlack: p, ChannelSMS: p}, nil).
		WithClock(clk.now).WithRetryPolicy(3, time.Minute)
	return d, out, store, clk
}

func msg(event, class string) Message {
	return Message{EventID: event, Class: class, Title: "push", Body: "body", Group: "github"}
}

func TestDispatcherSendsToOnlyTheJobsChannel(t *testing.T) {
	p := &scriptedProvider{}
	d, out, store, _ := setup(p)
	if n, err := out.Enqueue(context.Background(), msg("e1", "standard")); err != nil || n != 2 {
		t.Fatalf("enqueue = %d, %v", n, err)
	}
	if n, _ := d.RunOnce(context.Background()); n != 2 {
		t.Fatalf("handled %d", n)
	}
	for _, r := range p.reqs {
		slack, sms := len(r.SlackWebhookURLs) > 0, len(r.SMSRecipients) > 0
		if slack == sms || len(r.EmailRecipients) > 0 {
			t.Fatalf("a request reached more than one channel: %+v", r)
		}
	}
	if c, _ := store.Counts(context.Background()); c[StatusSent] != 2 {
		t.Fatalf("counts = %v", c)
	}
}

func TestOutboxIsIdempotentAndSkipsUnroutedAndCritical(t *testing.T) {
	_, out, _, _ := setup(&scriptedProvider{})
	ctx := context.Background()
	out.Enqueue(ctx, msg("e1", "standard"))
	if n, _ := out.Enqueue(ctx, msg("e1", "standard")); n != 0 {
		t.Fatalf("a redelivered event queued %d more jobs", n)
	}
	if n, _ := out.Enqueue(ctx, msg("e2", "background")); n != 0 {
		t.Fatalf("an unrouted class queued %d jobs", n)
	}
	out.routes["critical"] = []string{ChannelSlack}
	if n, _ := out.Enqueue(ctx, msg("e3", "critical")); n != 0 {
		t.Fatal("critical events are never queued; they are delivered inline")
	}
	out.routes["standard"] = []string{ChannelEmail} // not configured
	if n, _ := out.Enqueue(ctx, msg("e4", "standard")); n != 0 {
		t.Fatal("a channel without targets must be skipped")
	}
}

func TestDispatcherRetriesWithBackoffThenDeadLetters(t *testing.T) {
	p := &scriptedProvider{errs: []error{errors.New("down"), errors.New("down"), errors.New("down")}}
	d, out, store, clk := setup(p)
	out.routes["standard"] = []string{ChannelSlack}
	out.Enqueue(context.Background(), msg("e1", "standard"))

	d.RunOnce(context.Background()) // attempt 1 fails, retry in 1m
	if n, _ := d.RunOnce(context.Background()); n != 0 {
		t.Fatal("retried before the backoff elapsed")
	}
	clk.t = clk.t.Add(time.Minute)
	d.RunOnce(context.Background()) // attempt 2 fails, retry in 2m
	clk.t = clk.t.Add(time.Minute)
	if n, _ := d.RunOnce(context.Background()); n != 0 {
		t.Fatal("backoff must double")
	}
	clk.t = clk.t.Add(time.Minute)
	d.RunOnce(context.Background()) // attempt 3 fails: dead

	list, _ := store.List(context.Background(), 10)
	if len(list) != 1 || list[0].Status != StatusDead || list[0].Attempts != 3 || list[0].LastError != "down" {
		t.Fatalf("job = %+v", list)
	}

	// A person retries it from the console and it succeeds.
	if err := store.Requeue(context.Background(), list[0].ID, clk.t); err != nil {
		t.Fatal(err)
	}
	d.RunOnce(context.Background())
	list, _ = store.List(context.Background(), 10)
	if list[0].Status != StatusSent {
		t.Fatalf("after retry = %+v", list[0])
	}
}

func TestDispatcherPermanentErrorsDeadLetterAtOnce(t *testing.T) {
	p := &scriptedProvider{errs: []error{fmt.Errorf("%w: bad number", ErrPermanent)}}
	d, out, store, _ := setup(p)
	out.routes["standard"] = []string{ChannelSMS}
	out.Enqueue(context.Background(), msg("e1", "standard"))
	d.RunOnce(context.Background())
	list, _ := store.List(context.Background(), 10)
	if list[0].Status != StatusDead || list[0].Attempts != 1 {
		t.Fatalf("job = %+v", list[0])
	}
}

func TestDispatcherDeadLettersAJobWhoseChannelIsGone(t *testing.T) {
	d, out, store, _ := setup(&scriptedProvider{})
	out.Enqueue(context.Background(), msg("e1", "standard"))
	d.targets = Targets{} // configuration changed after queuing
	d.RunOnce(context.Background())
	list, _ := store.List(context.Background(), 10)
	for _, j := range list {
		if j.Status != StatusDead || !strings.Contains(j.LastError, "not configured") {
			t.Fatalf("job = %+v", j)
		}
	}
}

func TestDispatcherDailyCapAppliesToQueuedJobsOnly(t *testing.T) {
	p := &scriptedProvider{}
	d, out, store, _ := setup(p)
	d.WithDailyCap(ChannelSMS, 1)
	out.routes["standard"] = []string{ChannelSMS}
	for _, id := range []string{"e1", "e2"} {
		out.Enqueue(context.Background(), msg(id, "standard"))
	}
	d.RunOnce(context.Background())
	c, _ := store.Counts(context.Background())
	if c[StatusSent] != 1 || c[StatusDead] != 1 {
		t.Fatalf("counts = %v, want one sent and one over the cap", c)
	}
	if p.calls != 1 {
		t.Fatalf("provider called %d times", p.calls)
	}
}

func TestRecordedErrorsNeverContainWebhookSecrets(t *testing.T) {
	err := fmt.Errorf(`post webhook: Post "https://hooks.slack.com/services/T0/B0/SECRETTOKEN?x=1": dial tcp: refused`)
	text := RedactError(err)
	if strings.Contains(text, "SECRETTOKEN") || !strings.Contains(text, "https://hooks.slack.com/…") {
		t.Fatalf("redacted = %q", text)
	}
	if RedactError(nil) != "" {
		t.Fatal("nil error")
	}
	if got := RedactError(errors.New(strings.Repeat("x", 1000))); len([]rune(got)) > maxErrorChars+1 {
		t.Fatalf("error text not bounded: %d", len(got))
	}
}

func TestRecordDirectLogsCriticalDelivery(t *testing.T) {
	_, out, store, _ := setup(&scriptedProvider{})
	m := msg("e1", "critical")
	if err := out.RecordDirect(context.Background(), m, "smtp+webhook", nil); err != nil {
		t.Fatal(err)
	}
	if err := out.RecordDirect(context.Background(), m, "smtp+webhook", errors.New("slack down")); err != nil {
		t.Fatal(err)
	}
	list, _ := store.List(context.Background(), 10)
	if len(list) != 1 || list[0].Status != StatusDead || !list[0].Direct || list[0].LastError != "slack down" {
		t.Fatalf("log = %+v", list)
	}
}

func TestParseChannels(t *testing.T) {
	got, err := ParseChannels([]string{" Slack", "", "SMS"})
	if err != nil || len(got) != 2 || got[0] != "slack" || got[1] != "sms" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := ParseChannels([]string{"pager"}); err == nil {
		t.Fatal("an unknown channel must be rejected")
	}
}

func TestTargetsChannelsAreOrderedAndOnlyConfigured(t *testing.T) {
	got := Targets{SMS: []string{"+1"}, Email: []string{"a@b.c"}}.Channels()
	if len(got) != 2 || got[0] != ChannelEmail || got[1] != ChannelSMS {
		t.Fatalf("channels = %v", got)
	}
}

func TestSMSProviderSendsValidatedShortText(t *testing.T) {
	gw := sms.NewMock(nil)
	p := NewSMSProvider(gw)
	long := strings.Repeat("word ", 200)
	err := p.Send(context.Background(), notification.NotificationRequest{
		Title: "[CRITICAL] x", Message: "line one\nline two " + long, SMSRecipients: []string{"+14155550100"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sent := gw.Sent()
	if len(sent) != 1 || strings.Contains(sent[0].Text, "\n") || len([]rune(sent[0].Text)) > maxSMSChars {
		t.Fatalf("text = %q", sent[0].Text)
	}

	if p.Send(context.Background(), notification.NotificationRequest{}) != nil {
		t.Fatal("no numbers is a no-op")
	}
	err = p.Send(context.Background(), notification.NotificationRequest{SMSRecipients: []string{"12345"}})
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("a bad number must be a permanent error, got %v", err)
	}
	err = NewSMSProvider(nil).Send(context.Background(), notification.NotificationRequest{SMSRecipients: []string{"+14155550100"}})
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("a missing gateway must be a permanent error, got %v", err)
	}
	if p.Name() != "sms" {
		t.Fatal("name")
	}
}
