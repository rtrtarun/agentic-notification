package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/sms"
)

func starEvents(s *Server, n int) {
	for i := 0; i < n; i++ {
		repo := []string{"acme/web", "acme/api", "acme/docs"}[i%3]
		webhook(s, "star", "star-"+string(rune('a'+i)), `{"action":"created","repository":{"full_name":"`+repo+`"},"sender":{"login":"fan`+string(rune('a'+i))+`"}}`)
	}
}

type digestResponse struct {
	Generated bool `json:"generated"`
	Digest    struct {
		ID         string `json:"id"`
		Count      int    `json:"count"`
		Title      string `json:"title"`
		Summary    string `json:"summary"`
		Model      string `json:"model"`
		Highlights []any  `json:"highlights"`
		Groups     []struct {
			Title    string   `json:"title"`
			EventIDs []string `json:"eventIds"`
		} `json:"groups"`
	} `json:"digest"`
}

func TestHeldEventsBecomeADigestThatIsStoredListedAndDelivered(t *testing.T) {
	s := newDeliveryServer(t, func(c *config.Config) { c.DigestChannels = []string{"sms"}; c.StandardChannels = nil })
	starEvents(s, 5) // five stars across three repositories: the repeats merge, three remain
	webhook(s, "security_advisory", "sec-1", securityPayload)

	rr := call(s, http.MethodPost, "/api/digests/generate", "", withKey)
	if rr.Code != http.StatusCreated {
		t.Fatalf("generate = %d %s", rr.Code, rr.Body.String())
	}
	var got digestResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	d := got.Digest
	if !got.Generated || d.Count != 3 || d.Model != "heuristic-v1" || len(d.Groups) != 3 || !strings.Contains(d.Title, "3 notifications") {
		t.Fatalf("digest = %+v", got)
	}
	for _, g := range d.Groups {
		if strings.Contains(strings.Join(g.EventIDs, ","), "security") {
			t.Fatal("a critical event must never be in a digest")
		}
	}

	// A second request has nothing new to say.
	again := call(s, http.MethodPost, "/api/digests/generate", "", withKey)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"generated":false`) {
		t.Fatalf("second generate = %d %s", again.Code, again.Body.String())
	}

	list := call(s, http.MethodGet, "/api/digests", "", withKey)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), d.ID) {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	one := call(s, http.MethodGet, "/api/digests/"+d.ID, "", withKey)
	if one.Code != http.StatusOK || !strings.Contains(one.Body.String(), "acme/web") {
		t.Fatalf("get = %d %s", one.Code, one.Body.String())
	}

	// The digest was queued for the sms channel and goes out through the same dispatcher as everything else.
	mock := s.smsGateway.(*sms.Mock)
	before := len(mock.Sent())
	if n, err := s.dispatcher.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("dispatch = %d %v", n, err)
	}
	sent := mock.Sent()
	if len(sent) != before+1 || !strings.Contains(sent[len(sent)-1].Text, "Attention digest") {
		t.Fatalf("the digest was not delivered: %+v", sent)
	}
}

func TestDigestEndpointsValidateAndRequireTheRightAccess(t *testing.T) {
	s := newAuthServer(t, nil)
	starEvents(s, 2)
	m := addMember(t, s, "member@corp.com")

	if rr := call(s, http.MethodPost, "/api/digests/generate", "", m.apply(true)); rr.Code != http.StatusForbidden {
		t.Fatalf("a member generating = %d, want 403", rr.Code)
	}
	if rr := call(s, http.MethodGet, "/api/digests", "", m.apply(false)); rr.Code != http.StatusOK {
		t.Fatalf("a member reading = %d", rr.Code)
	}
	checks := []struct {
		method, path string
		edit         func(*http.Request)
		want         int
	}{
		{http.MethodGet, "/api/digests", nil, http.StatusUnauthorized},
		{http.MethodGet, "/api/digests?limit=0", withKey, http.StatusBadRequest},
		{http.MethodGet, "/api/digests?limit=500", withKey, http.StatusBadRequest},
		{http.MethodGet, "/api/digests/nope", withKey, http.StatusNotFound},
		{http.MethodPost, "/api/digests", withKey, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/digests/generate", withKey, http.StatusMethodNotAllowed},
		{http.MethodDelete, "/api/digests/x", withKey, http.StatusMethodNotAllowed},
	}
	for _, c := range checks {
		if rr := call(s, c.method, c.path, "", c.edit); rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
	if rr := call(s, http.MethodPost, "/api/digests/generate", "", withKey); rr.Code != http.StatusCreated {
		t.Fatalf("an admin generating = %d %s", rr.Code, rr.Body.String())
	}
	if audit := call(s, http.MethodGet, "/api/audit", "", withKey).Body.String(); !strings.Contains(audit, "digest.generated") {
		t.Fatalf("generating a digest must be audited: %s", audit)
	}
}

func TestNoiseIsMovedToTheDigestWhenTheRuleAllowsIt(t *testing.T) {
	s := newAuthServer(t, nil)
	// A bot labelling a pull request matches default-github, which is adaptive.
	rr := webhook(s, "pull_request", "bot-1", `{"action":"labeled","repository":{"full_name":"acme/web"},"pull_request":{"number":4,"draft":true},"sender":{"login":"renovate[bot]","type":"Bot"}}`)
	body := rr.Body.String()
	if !strings.Contains(body, `"attentionClass":"background"`) || !strings.Contains(body, `"status":"digest"`) || !strings.Contains(body, "attention scored") {
		t.Fatalf("noise from a bot must be held for the digest, with the reason: %s", body)
	}
	// A real request for review stays.
	rr = webhook(s, "pull_request", "rev-1", `{"action":"review_requested","repository":{"full_name":"acme/web"},"pull_request":{"number":5}}`)
	if !strings.Contains(rr.Body.String(), `"attentionClass":"standard"`) {
		t.Fatalf("a review request must stay standard: %s", rr.Body.String())
	}
}

func TestAttentionCanBeSwitchedOff(t *testing.T) {
	s := newAuthServer(t, func(c *config.Config) { c.AttentionClassify = false })
	rr := webhook(s, "pull_request", "bot-1", `{"action":"labeled","repository":{"full_name":"acme/web"},"pull_request":{"number":4},"sender":{"login":"renovate[bot]","type":"Bot"}}`)
	if !strings.Contains(rr.Body.String(), `"attentionClass":"standard"`) || strings.Contains(rr.Body.String(), "attention scored") {
		t.Fatalf("with attention off rules alone decide: %s", rr.Body.String())
	}
}

func TestExplainSaysWhyInPlainLanguage(t *testing.T) {
	s := newAuthServer(t, nil)
	starEvents(s, 1)
	crit := eventID(t, webhook(s, "security_advisory", "sec-1", securityPayload))

	rr := call(s, http.MethodGet, "/api/explain/"+crit, "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "critical") || !strings.Contains(rr.Body.String(), "nothing can mute it") {
		t.Fatalf("explain critical = %d %s", rr.Code, rr.Body.String())
	}
	held := call(s, http.MethodGet, "/api/explain/github-star-star-a", "", withKey)
	if held.Code != http.StatusOK || !strings.Contains(held.Body.String(), "digest") {
		t.Fatalf("explain held = %d %s", held.Code, held.Body.String())
	}
	for path, want := range map[string]int{"/api/explain/nope": http.StatusNotFound, "/api/explain/": http.StatusNotFound, "/api/explain/a/b": http.StatusNotFound} {
		if rr := call(s, http.MethodGet, path, "", withKey); rr.Code != want {
			t.Errorf("%s = %d, want %d", path, rr.Code, want)
		}
	}
	if rr := call(s, http.MethodPost, "/api/explain/"+crit, "", withKey); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rr.Code)
	}
	if rr := call(s, http.MethodGet, "/api/explain/"+crit, "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rr.Code)
	}
}

func TestACriticalRuleCannotBeMadeAdaptiveThroughTheAPI(t *testing.T) {
	s := newAuthServer(t, nil)
	rr := call(s, http.MethodPost, "/api/rules", `{"name":"x","when":{"source":"jira"},"then":{"attentionClass":"critical","adaptive":true}}`, withKey)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "cannot be adaptive") {
		t.Fatalf("critical adaptive = %d %s", rr.Code, rr.Body.String())
	}
	rr = call(s, http.MethodPost, "/api/rules", `{"name":"y","when":{"source":"jira"},"then":{"attentionClass":"standard","adaptive":true}}`, withKey)
	if rr.Code != http.StatusCreated || !strings.Contains(rr.Body.String(), `"adaptive":true`) {
		t.Fatalf("standard adaptive = %d %s", rr.Code, rr.Body.String())
	}
}

func TestStartupRejectsABadDigestChannel(t *testing.T) {
	cfg, _ := config.LoadConfig()
	cfg.DigestChannels = []string{"pager"}
	if _, err := NewServer(context.Background(), cfg, quietLogger()); err == nil {
		t.Fatal("an unknown digest channel must stop start-up")
	}
}
