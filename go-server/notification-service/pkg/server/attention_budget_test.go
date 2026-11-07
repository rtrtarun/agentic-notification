package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/agentpulse/notification-service/pkg/config"
)

func pullRequest(s *Server, n int) string {
	body := fmt.Sprintf(`{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":%d},"sender":{"login":"alice"}}`, n)
	return eventID2(s, "pr-"+fmt.Sprint(n), body)
}

func eventID2(s *Server, delivery, body string) string {
	rr := webhook(s, "pull_request", delivery, body)
	i := strings.Index(rr.Body.String(), `"eventId":"`)
	if i < 0 {
		return ""
	}
	rest := rr.Body.String()[i+len(`"eventId":"`):]
	return rest[:strings.Index(rest, `"`)]
}

func jobCount(t *testing.T, s *Server) int {
	t.Helper()
	list, err := s.deliveryStore.List(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestBudgetHoldsStandardEventsForTheDigestOverHTTP(t *testing.T) {
	s := newDeliveryServer(t, func(c *config.Config) { c.AttentionBudgetPerHour = 2 })
	ids := []string{pullRequest(s, 1), pullRequest(s, 2), pullRequest(s, 3)}
	if jobCount(t, s) != 2 {
		t.Fatalf("only two of three may interrupt, got %d deliveries", jobCount(t, s))
	}
	why := call(s, http.MethodGet, "/api/explain/"+ids[2], "", withKey).Body.String()
	if !strings.Contains(why, "attention budget") || !strings.Contains(why, "not dropped") {
		t.Fatalf("explain = %s", why)
	}
	gen := call(s, http.MethodPost, "/api/digests/generate", "", withKey)
	if gen.Code != http.StatusCreated || !strings.Contains(gen.Body.String(), `"count":1`) {
		t.Fatalf("the held event must be in the next digest: %d %s", gen.Code, gen.Body.String())
	}
}

func TestTheBudgetCanBeTurnedOff(t *testing.T) {
	s := newDeliveryServer(t, func(c *config.Config) { c.AttentionBudgetPerHour = 0 })
	for i := 1; i <= 15; i++ {
		pullRequest(s, i)
	}
	if jobCount(t, s) != 15 {
		t.Fatalf("with no budget every event interrupts, got %d", jobCount(t, s))
	}
}

func TestCriticalEventsIgnoreAnExhaustedBudgetOverHTTP(t *testing.T) {
	s := newDeliveryServer(t, func(c *config.Config) { c.AttentionBudgetPerHour = 1 })
	pullRequest(s, 1)
	pullRequest(s, 2) // over budget
	rr := webhook(s, "security_advisory", "sec-1", securityPayload)
	if rr.Code >= 300 || !strings.Contains(rr.Body.String(), `"attentionClass":"critical"`) || !strings.Contains(rr.Body.String(), `"status":"sent"`) {
		t.Fatalf("a critical event must be sent whatever the budget: %d %s", rr.Code, rr.Body.String())
	}
}

func TestAsyncAttentionDefersTheDecisionToTheWorker(t *testing.T) {
	s := newDeliveryServer(t, func(c *config.Config) { c.AttentionAsync, c.AttentionBudgetPerHour = true, 1 })
	if s.attentionWorker == nil {
		t.Fatal("the worker should exist")
	}
	ids := []string{pullRequest(s, 1), pullRequest(s, 2)}
	if jobCount(t, s) != 0 {
		t.Fatalf("nothing may be delivered before the worker runs, got %d", jobCount(t, s))
	}
	crit := webhook(s, "security_advisory", "sec-1", securityPayload)
	if !strings.Contains(crit.Body.String(), `"status":"sent"`) {
		t.Fatalf("a critical event is never deferred: %s", crit.Body.String())
	}

	n, err := s.attentionWorker.RunOnce(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("worker decided %d events, %v", n, err)
	}
	if got := jobCount(t, s); got != 2 { // one standard delivery, plus the critical event's direct log entry
		t.Fatalf("deliveries = %d, want 1 queued plus the critical log entry", got)
	}
	first := call(s, http.MethodGet, "/api/explain/"+ids[0], "", withKey).Body.String()
	second := call(s, http.MethodGet, "/api/explain/"+ids[1], "", withKey).Body.String()
	if strings.Contains(first, "attention budget") || !strings.Contains(second, "attention budget") {
		t.Fatalf("first should have gone out and second been held:\n%s\n%s", first, second)
	}
	if n, _ := s.attentionWorker.RunOnce(context.Background()); n != 0 {
		t.Fatalf("a decided event must not be processed again, got %d", n)
	}
}

func TestStartupRejectsAnInvalidBudgetSetting(t *testing.T) {
	t.Setenv("ATTENTION_BUDGET_PER_HOUR", "-1")
	if _, err := config.LoadConfig(); err == nil {
		t.Fatal("a negative budget must be refused")
	}
}
