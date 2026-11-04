package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/pipeline"
	"github.com/example/notification-service/pkg/policy"
)

func TestNormalizeGitHubEvent(t *testing.T) {
	s := &Server{}
	payload := map[string]interface{}{
		"repository":   map[string]interface{}{"full_name": "octo/repo"},
		"pull_request": map[string]interface{}{"number": 42},
		"sender":       map[string]interface{}{"login": "octocat"},
	}

	e := s.normalizeGitHubEvent("pull_request", "72d3162e-cc78-11e3-81ab-4c9367dc0958", payload)
	if e.ID != "github-pull_request-72d3162e-cc78-11e3-81ab-4c9367dc0958" {
		t.Fatalf("event id must embed the delivery id, got %s", e.ID)
	}

	if e.Source != "github" {
		t.Fatalf("expected source github, got %s", e.Source)
	}
	if e.EventType != "pull_request" {
		t.Fatalf("expected event type pull_request, got %s", e.EventType)
	}
	if e.Repository != "octo/repo" {
		t.Fatalf("expected repository octo/repo, got %s", e.Repository)
	}
}

func TestGitHubWebhookHandlerRunsPipeline(t *testing.T) {
	repo := event.NewInMemoryRepository()
	engine := policy.NewEngine([]policy.Rule{{
		Name: "default-github",
		When: policy.RuleCondition{Source: "github"},
		Then: policy.RuleOutcome{AttentionClass: "standard", Reason: "default routing"},
	}})
	s := &Server{
		cfg:       &config.Config{Address: ":8080"},
		eventRepo: repo,
		eventPipeline: pipeline.New(
			engine,
			delivery.NewService(&delivery.StubProvider{}),
			repo,
			pipeline.Config{},
		),
	}

	payload := []byte(`{"repository":{"full_name":"octo/repo"},"pull_request":{"number":42},"sender":{"login":"octocat"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "pull_request")
	rr := httptest.NewRecorder()

	s.githubWebhookHandler(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rr.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["attentionClass"] != "standard" {
		t.Fatalf("expected standard attention class, got %v", resp["attentionClass"])
	}

	stored, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(stored))
	}
	if stored[0].DecisionState == nil || stored[0].DecisionState.Status != event.DecisionQueued {
		t.Fatalf("expected queued decision status")
	}
}

func TestGitHubWebhookHandlerDeliversCriticalEvents(t *testing.T) {
	repo := event.NewInMemoryRepository()
	engine := policy.NewEngine([]policy.Rule{{
		Name: "security",
		When: policy.RuleCondition{Source: "github", EventType: "security_advisory"},
		Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "security vulnerability"},
	}})
	s := &Server{
		cfg:       &config.Config{Address: ":8080"},
		eventRepo: repo,
		eventPipeline: pipeline.New(
			engine,
			delivery.NewService(&delivery.StubProvider{}),
			repo,
			pipeline.Config{},
		),
	}

	payload := []byte(`{"repository":{"full_name":"octo/repo"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "security_advisory")
	rr := httptest.NewRecorder()

	s.githubWebhookHandler(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rr.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["attentionClass"] != "critical" {
		t.Fatalf("expected critical attention class, got %v", resp["attentionClass"])
	}

	decisionState, ok := resp["decisionState"].(map[string]interface{})
	if !ok {
		t.Fatal("expected decisionState in response")
	}
	if decisionState["status"] != event.DecisionSent {
		t.Fatalf("expected sent status, got %v", decisionState["status"])
	}
}
