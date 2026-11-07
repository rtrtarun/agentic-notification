package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/example/notification-service/pkg/notification"
)

type triggerLog struct {
	mu     sync.Mutex
	bodies []map[string]any
	auth   string
	path   string
	method string
}

func novuServer(t *testing.T, status int) (*NovuProvider, *triggerLog, *httptest.Server) {
	t.Helper()
	log := &triggerLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		log.mu.Lock()
		log.bodies = append(log.bodies, body)
		log.auth, log.path, log.method = r.Header.Get("Authorization"), r.URL.Path, r.Method
		log.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":{"acknowledged":true}}`))
	}))
	t.Cleanup(srv.Close)
	p, err := NewNovuProvider(srv.URL+"/", "novu-secret", "alerts")
	if err != nil {
		t.Fatal(err)
	}
	return p, log, srv
}

func emailRequest(to ...string) notification.NotificationRequest {
	return notification.NotificationRequest{
		Agent: notification.AgentInfo{Name: "researcher", Group: "github"}, Title: "Deploy failed", Message: "details",
		EmailRecipients: to, SlackWebhookURLs: []string{"https://hooks.slack.com/x"},
	}
}

func TestNewNovuProviderValidatesAndDefaults(t *testing.T) {
	for name, c := range map[string][2]string{
		"no key":    {"https://api.novu.co", ""},
		"blank key": {"https://api.novu.co", "  "},
		"ftp":       {"ftp://novu.example.com", "k"},
		"no host":   {"https://", "k"},
		"not a url": {"http://[::1", "k"},
	} {
		if _, err := NewNovuProvider(c[0], c[1], ""); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	p, err := NewNovuProvider("", "k", "")
	if err != nil || p.baseURL != DefaultNovuURL || p.workflow != DefaultNovuWorkflow || p.Name() != "novu" {
		t.Fatalf("defaults wrong: %+v %v", p, err)
	}
}

func TestNovuTriggersOneWorkflowPerEmailRecipient(t *testing.T) {
	p, log, _ := novuServer(t, 201)
	if err := p.Send(context.Background(), emailRequest("a@corp.com", "b@corp.com")); err != nil {
		t.Fatal(err)
	}
	if len(log.bodies) != 2 || log.method != "POST" || log.path != "/v1/events/trigger" || log.auth != "ApiKey novu-secret" {
		t.Fatalf("calls = %+v (%s %s %s)", log.bodies, log.method, log.path, log.auth)
	}
	first := log.bodies[0]
	to := first["to"].(map[string]any)
	payload := first["payload"].(map[string]any)
	if first["name"] != "alerts" || to["subscriberId"] != "a@corp.com" || to["email"] != "a@corp.com" || payload["title"] != "Deploy failed" || payload["agent"] != "researcher" {
		t.Fatalf("trigger = %+v", first)
	}
}

func TestNovuIgnoresRequestsWithoutEmailRecipients(t *testing.T) {
	p, log, _ := novuServer(t, 201)
	if err := p.Send(context.Background(), emailRequest()); err != nil || len(log.bodies) != 0 {
		t.Fatalf("a Slack-only request is not Novu's: %v %d", err, len(log.bodies))
	}
}

func TestNovuFailuresAreClassifiedForTheRetryQueue(t *testing.T) {
	for status, wantPermanent := range map[int]bool{400: true, 401: true, 403: true, 404: true, 422: true, 429: false, 500: false, 503: false} {
		p, _, _ := novuServer(t, status)
		err := p.Send(context.Background(), emailRequest("a@corp.com"))
		if err == nil {
			t.Fatalf("status %d must be an error", status)
		}
		if errors.Is(err, ErrPermanent) != wantPermanent {
			t.Errorf("status %d: permanent = %v, want %v (%v)", status, errors.Is(err, ErrPermanent), wantPermanent, err)
		}
		if strings.Contains(err.Error(), "novu-secret") {
			t.Errorf("status %d: the API key leaked: %v", status, err)
		}
	}
}

func TestNovuOneFailingRecipientDoesNotStopTheOthers(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	p, _ := NewNovuProvider(srv.URL, "k", "")
	if err := p.Send(context.Background(), emailRequest("a@corp.com", "b@corp.com")); err == nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestNovuDoesNotFollowRedirectsWithTheKey(t *testing.T) {
	var elsewhereHits int
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhereHits++ }))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	p, _ := NewNovuProvider(srv.URL, "novu-secret", "")
	_ = p.Send(context.Background(), emailRequest("a@corp.com"))
	if elsewhereHits != 0 {
		t.Fatal("a redirect would have carried the API key to another host")
	}
}

func TestNovuUnreachableGivesAGenericError(t *testing.T) {
	p, _ := NewNovuProvider("http://127.0.0.1:1", "k", "")
	if err := p.Send(context.Background(), emailRequest("a@corp.com")); err == nil || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("err = %v", err)
	}
}

type fallbackStub struct {
	name  string
	err   error
	calls int
}

func (s *fallbackStub) Name() string { return s.name }
func (s *fallbackStub) Send(context.Context, notification.NotificationRequest) error {
	s.calls++
	return s.err
}

func TestFallbackUsesTheSecondProviderOnlyWhenTheFirstFails(t *testing.T) {
	ctx := context.Background()
	ok, backup := &fallbackStub{name: "novu"}, &fallbackStub{name: "smtp"}
	f := NewFallbackProvider(ok, backup)
	if err := f.Send(ctx, emailRequest("a@corp.com")); err != nil || backup.calls != 0 {
		t.Fatalf("a healthy primary must not trigger the fallback: %v %d", err, backup.calls)
	}
	if f.Name() != "novu>smtp" {
		t.Fatalf("name = %s", f.Name())
	}

	down, backup := &fallbackStub{name: "novu", err: errors.New("novu down")}, &fallbackStub{name: "smtp"}
	if err := NewFallbackProvider(down, backup).Send(ctx, emailRequest("a@corp.com")); err != nil || backup.calls != 1 {
		t.Fatalf("the fallback should have delivered: %v %d", err, backup.calls)
	}

	down, broken := &fallbackStub{name: "novu", err: errors.New("novu down")}, &fallbackStub{name: "smtp", err: errors.New("smtp down")}
	err := NewFallbackProvider(down, broken).Send(ctx, emailRequest("a@corp.com"))
	if err == nil || !strings.Contains(err.Error(), "novu down") || !strings.Contains(err.Error(), "smtp down") {
		t.Fatalf("both failures must be reported: %v", err)
	}
}

func TestFallbackDoesNotRetryAfterTheCallerGaveUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	primary, backup := &fallbackStub{name: "novu", err: context.Canceled}, &fallbackStub{name: "smtp"}
	if err := NewFallbackProvider(primary, backup).Send(ctx, emailRequest("a@corp.com")); err == nil || backup.calls != 0 {
		t.Fatalf("a cancelled request must stop: %v %d", err, backup.calls)
	}
}
