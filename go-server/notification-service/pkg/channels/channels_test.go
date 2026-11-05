package channels_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/channels"
	"github.com/example/notification-service/pkg/notification"
)

const slackURL = "https://hooks.slack.com/services/T000/B000/SECRETSECRET"

func full() channels.Settings {
	return channels.Settings{
		SMTPHost: "smtp.example.com", SMTPSender: "alerts@example.com", EmailRecipients: []string{"alice@example.com", "bob@corp.io"},
		SlackWebhookURL: slackURL, TeamsWebhookURL: "https://acme.webhook.office.com/webhookb2/SECRET",
	}
}

func byID(list []channels.Channel, id string) channels.Channel {
	for _, c := range list {
		if c.ID == id {
			return c
		}
	}
	return channels.Channel{}
}

func TestDescribeMasksEveryTarget(t *testing.T) {
	list := channels.Describe(full())
	if len(list) != 3 {
		t.Fatalf("channels = %d", len(list))
	}
	email := byID(list, channels.IDEmail)
	if !email.Configured || email.Target != "a***@example.com, b***@corp.io" {
		t.Fatalf("email = %+v", email)
	}
	if got := byID(list, channels.IDSlack).Target; got != "hooks.slack.com/…" {
		t.Fatalf("slack target = %q", got)
	}
	for _, c := range list {
		for _, secret := range []string{"SECRET", "alice", "T000"} {
			if strings.Contains(c.Target, secret) {
				t.Errorf("%s leaks %q in %q", c.ID, secret, c.Target)
			}
		}
		if len(c.Receives) != 1 || c.Receives[0] != "critical" || c.Setup == "" {
			t.Errorf("%s = %+v", c.ID, c)
		}
	}
}

func TestDescribeUnconfigured(t *testing.T) {
	for _, c := range channels.Describe(channels.Settings{SMTPHost: "smtp.example.com"}) {
		if c.Configured || c.Target != "" {
			t.Errorf("%s should be unconfigured: %+v", c.ID, c)
		}
	}
	// Email needs host, sender and at least one recipient.
	partial := channels.Settings{SMTPHost: "h", SMTPSender: "s@x.io"}
	if byID(channels.Describe(partial), channels.IDEmail).Configured {
		t.Fatal("email without recipients must not count as configured")
	}
}

type fakeSender struct {
	got []notification.NotificationRequest
	err error
}

func (f *fakeSender) Send(_ context.Context, r notification.NotificationRequest) error {
	f.got = append(f.got, r)
	return f.err
}

func TestTestSendsOnlyThroughTheChosenChannel(t *testing.T) {
	sender := &fakeSender{}
	tester := channels.NewTester(sender, full(), 0)
	ctx := context.Background()

	if err := tester.Test(ctx, channels.IDSlack); err != nil {
		t.Fatal(err)
	}
	req := sender.got[0]
	if len(req.SlackWebhookURLs) != 1 || len(req.EmailRecipients) != 0 || len(req.TeamsWebhookURLs) != 0 {
		t.Fatalf("slack test touched other channels: %+v", req)
	}
	if !strings.HasPrefix(req.Title, "[TEST]") {
		t.Fatalf("test message must be labelled: %q", req.Title)
	}

	if err := tester.Test(ctx, channels.IDEmail); err != nil {
		t.Fatal(err)
	}
	if r := sender.got[1]; len(r.EmailRecipients) != 2 || len(r.SlackWebhookURLs) != 0 {
		t.Fatalf("email test = %+v", r)
	}
}

func TestTestErrors(t *testing.T) {
	sender := &fakeSender{}
	tester := channels.NewTester(sender, channels.Settings{SlackWebhookURL: slackURL}, 0)
	ctx := context.Background()
	if err := tester.Test(ctx, "pager"); !errors.Is(err, channels.ErrUnknown) {
		t.Fatalf("unknown = %v", err)
	}
	for _, id := range []string{channels.IDEmail, channels.IDTeams} {
		if err := tester.Test(ctx, id); !errors.Is(err, channels.ErrNotConfigured) {
			t.Fatalf("%s = %v", id, err)
		}
	}
	if len(sender.got) != 0 {
		t.Fatal("nothing should be sent for rejected tests")
	}
	boom := errors.New("smtp down")
	sender.err = boom
	if err := tester.Test(ctx, channels.IDSlack); !errors.Is(err, boom) {
		t.Fatalf("send failure = %v", err)
	}
}

func TestTestIsRateLimitedPerChannel(t *testing.T) {
	sender := &fakeSender{}
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	tester := channels.NewTester(sender, full(), 10*time.Second).WithClock(func() time.Time { return clock })
	ctx := context.Background()

	if err := tester.Test(ctx, channels.IDSlack); err != nil {
		t.Fatal(err)
	}
	if err := tester.Test(ctx, channels.IDSlack); !errors.Is(err, channels.ErrTooSoon) {
		t.Fatalf("second test = %v", err)
	}
	if err := tester.Test(ctx, channels.IDTeams); err != nil {
		t.Fatalf("another channel must not be limited: %v", err)
	}
	clock = clock.Add(11 * time.Second)
	if err := tester.Test(ctx, channels.IDSlack); err != nil {
		t.Fatalf("after the gap: %v", err)
	}
	if len(sender.got) != 3 {
		t.Fatalf("sent %d", len(sender.got))
	}
}
