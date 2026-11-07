package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/config"
)

const retryRule = `{
  "name": "agent-retries",
  "when": {"source": "agent", "payloadMatch": [{"field": "metadata.attempt", "op": "gt", "value": 3}]},
  "then": {"attentionClass": "standard", "reason": "an agent keeps retrying", "channels": ["sms"]}
}`

func agentServer(t *testing.T) *Server {
	t.Helper()
	// No class route and no budget: only the rule's own channel can deliver anything.
	s := newDeliveryServer(t, func(c *config.Config) { c.StandardChannels = nil; c.AttentionBudgetPerHour = 0 })
	if rr := call(s, http.MethodPost, "/api/rules", retryRule, withKey); rr.Code != http.StatusCreated {
		t.Fatalf("create rule = %d %s", rr.Code, rr.Body.String())
	}
	return s
}

func agentStep(id, parent, typ, kind string, md string) string {
	parentField := ""
	if parent != "" {
		parentField = `"parentEventId":"` + parent + `",`
	}
	return fmt.Sprintf(`{"id":"%s",%s"traceId":"run-7","agentName":"researcher","eventType":"%s","metadata":{"step_kind":"%s"%s}}`, id, parentField, typ, kind, md)
}

func post(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(s, http.MethodPost, "/api/events", body, withKey)
}

func TestAnAgentRunFlowsThroughTheSamePipelineAndAThresholdRuleFiresAnAlert(t *testing.T) {
	s := agentServer(t)
	steps := []string{
		agentStep("a1", "", "agent.plan", "plan", `,"duration_ms":100,"tokens_in":500,"tokens_out":80,"cost_usd":0.004`),
		agentStep("a2", "a1", "tool.call.failed", "tool_call", `,"tool_name":"search","attempt":1,"duration_ms":30000`),
		agentStep("a3", "a2", "agent.retry", "retry", `,"attempt":2,"duration_ms":30000`),
		agentStep("a4", "a3", "agent.retry", "retry", `,"attempt":3,"duration_ms":30000`),
		agentStep("a5", "a4", "agent.retry", "retry", `,"attempt":4,"duration_ms":30000`),
		agentStep("a6", "a5", "agent.complete", "complete", ``),
	}
	for _, body := range steps {
		if rr := post(t, s, body); rr.Code != http.StatusAccepted {
			t.Fatalf("ingest = %d %s", rr.Code, rr.Body.String())
		}
	}
	jobs, _ := s.deliveryStore.List(context.Background(), 20)
	if len(jobs) != 1 || jobs[0].Channel != "sms" || jobs[0].EventID != "a5" {
		t.Fatalf("only the step past the attempt threshold should alert, to the rule's channel: %+v", jobs)
	}

	list := call(s, http.MethodGet, "/api/traces", "", withKey)
	var got struct {
		Traces []struct {
			TraceID, AgentName, Status, WorstClass string
			Steps, Retries, Failures               int
			TokensIn                               int
			CostUSD                                float64 `json:"costUsd"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &got); err != nil || len(got.Traces) != 1 {
		t.Fatalf("traces = %s (%v)", list.Body.String(), err)
	}
	tr := got.Traces[0]
	if tr.TraceID != "run-7" || tr.AgentName != "researcher" || tr.Steps != 6 || tr.Retries != 3 || tr.Failures != 1 || tr.Status != "complete" || tr.TokensIn != 500 || tr.CostUSD != 0.004 {
		t.Fatalf("summary = %+v", tr)
	}

	detail := call(s, http.MethodGet, "/api/traces/run-7", "", withKey)
	var d struct {
		StepList []struct {
			EventID, StepKind, DecisionReason string
			Depth                             int
			Attempt                           int
		} `json:"stepList"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &d); err != nil || len(d.StepList) != 6 {
		t.Fatalf("detail = %s (%v)", detail.Body.String(), err)
	}
	if d.StepList[0].EventID != "a1" || d.StepList[5].EventID != "a6" || d.StepList[4].Depth != 4 || d.StepList[4].Attempt != 4 {
		t.Fatalf("steps = %+v", d.StepList)
	}
	if !strings.Contains(d.StepList[4].DecisionReason, "an agent keeps retrying") {
		t.Fatalf("each step carries the decision made for it: %+v", d.StepList[4])
	}
}

func TestAnAgentCannotMakeItsOwnEventCritical(t *testing.T) {
	s := agentServer(t)
	rr := post(t, s, `{"id":"loud","eventType":"agent.panic","severity":"critical","traceId":"run-9"}`)
	if rr.Code != http.StatusAccepted || strings.Contains(rr.Body.String(), `"attentionClass":"critical"`) {
		t.Fatalf("severity is data, not a class: %d %s", rr.Code, rr.Body.String())
	}
}

func TestRepeatedAgentEventsAreRecognisedAndBadOnesRefused(t *testing.T) {
	s := agentServer(t)
	body := `{"id":"same","eventType":"agent.plan","traceId":"run-1"}`
	if rr := post(t, s, body); rr.Code != http.StatusAccepted {
		t.Fatalf("first = %d", rr.Code)
	}
	if rr := post(t, s, body); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"duplicate"`) {
		t.Fatalf("a repeated id must be acknowledged as a duplicate: %d %s", rr.Code, rr.Body.String())
	}
	for name, bad := range map[string]string{
		"no event type":   `{"id":"x"}`,
		"bad step kind":   `{"eventType":"a","metadata":{"step_kind":"dance"}}`,
		"parent no trace": `{"eventType":"a","parentEventId":"p"}`,
		"not json":        `{`,
	} {
		if rr := post(t, s, bad); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d %s, want 400", name, rr.Code, rr.Body.String())
		}
	}
}

func TestTraceAndEventEndpointsFilterValidateAndRequireAccess(t *testing.T) {
	s := agentServer(t)
	post(t, s, `{"id":"t1","eventType":"agent.plan","traceId":"run-A"}`)
	post(t, s, `{"id":"t2","eventType":"agent.plan","traceId":"run-B"}`)
	webhook(s, "star", "star-1", `{"action":"created","repository":{"full_name":"acme/web"}}`)

	one := call(s, http.MethodGet, "/api/events?traceId=run-A", "", withKey).Body.String()
	if !strings.Contains(one, `"t1"`) || strings.Contains(one, `"t2"`) || strings.Contains(one, "star") {
		t.Fatalf("traceId filter = %s", one)
	}
	if all := call(s, http.MethodGet, "/api/events", "", withKey).Body.String(); !strings.Contains(all, `"traceId":"run-B"`) {
		t.Fatalf("trace fields must show in the event list: %s", all)
	}
	for _, c := range []struct {
		method, path string
		edit         func(*http.Request)
		want         int
	}{
		{http.MethodGet, "/api/traces", nil, http.StatusUnauthorized},
		{http.MethodPost, "/api/events", nil, http.StatusUnauthorized},
		{http.MethodGet, "/api/traces/nope", withKey, http.StatusNotFound},
		{http.MethodGet, "/api/traces/run-A/extra", withKey, http.StatusNotFound},
		{http.MethodGet, "/api/traces?limit=0", withKey, http.StatusBadRequest},
		{http.MethodGet, "/api/traces?limit=500", withKey, http.StatusBadRequest},
		{http.MethodPost, "/api/traces", withKey, http.StatusMethodNotAllowed},
		{http.MethodDelete, "/api/events", withKey, http.StatusMethodNotAllowed},
	} {
		if rr := call(s, c.method, c.path, "", c.edit); rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
	if lim := call(s, http.MethodGet, "/api/traces?limit=1", "", withKey).Body.String(); strings.Count(lim, `"traceId"`) != 1 {
		t.Fatalf("limit not honoured: %s", lim)
	}
}
