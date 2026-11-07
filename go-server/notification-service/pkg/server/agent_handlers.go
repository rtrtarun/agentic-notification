package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/example/notification-service/pkg/agentevents"
	"github.com/example/notification-service/pkg/pipeline"
	"github.com/example/notification-service/pkg/traces"
	"github.com/example/notification-service/pkg/util"
)

const (
	apiEventsPath  = "/api/events"
	tracesPrefix   = "/api/traces"
	defaultTraces  = 30
	maxTracesLimit = 100
)

// apiEventsHandler is the front door for events that do not come from GitHub, such as the
// steps of an AI agent. It goes through exactly the same pipeline as a webhook: the same
// rules, deduplication, preferences, budget and delivery.
//
//	POST /api/events   one step (see agentevents.Input); 202 when accepted, 200 {"status":"duplicate"} for a repeated id
//	GET  /api/events   the most recent events, newest first; ?traceId= narrows to one run, ?limit= bounds the list
func (s *Server) apiEventsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.eventsHandler(w, r)
	case http.MethodPost:
		var in agentevents.Input
		if !s.decodeJSON(w, r, &in) {
			return
		}
		s.ingestAgentEvent(w, r, in)
	default:
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
	}
}

func (s *Server) ingestAgentEvent(w http.ResponseWriter, r *http.Request, in agentevents.Input) {
	evt, err := agentevents.Normalize(in)
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
		return
	}
	result, err := s.eventPipeline.Handle(r.Context(), evt)
	if errors.Is(err, pipeline.ErrDuplicateDelivery) {
		util.WriteJSON(w, http.StatusOK, map[string]string{"status": "duplicate", "eventId": evt.ID})
		return
	}
	if err != nil {
		s.logFor(r.Context()).Error("pipeline failed", "event_id", evt.ID, "event_type", evt.EventType, "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		return
	}
	s.logFor(r.Context()).Info("agent event processed", "event_id", result.ID, "trace_id", result.TraceID,
		"attention_class", result.AttentionClass, "decision_status", result.DecisionState.Status)
	util.WriteJSON(w, http.StatusAccepted, map[string]any{
		"status": "accepted", "eventId": result.ID, "traceId": result.TraceID,
		"attentionClass": result.AttentionClass, "decisionState": result.DecisionState,
	})
}

// tracesHandler reads agent runs.
//
//	GET /api/traces          recent runs with duration, steps, retries, tokens, cost, worst attention class and status
//	GET /api/traces/{id}     one run: its ordered steps, each with the decision the platform made for it
func (s *Server) tracesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	events, err := s.eventRepo.List(r.Context())
	if err != nil {
		s.logFor(r.Context()).Error("list events for traces failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, tracesPrefix), "/")
	if rest != "" {
		if strings.Contains(rest, "/") {
			util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
			return
		}
		t, ok := traces.Find(events, rest)
		if !ok {
			util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "trace not found"})
			return
		}
		util.WriteJSON(w, http.StatusOK, t)
		return
	}
	limit := defaultTraces
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxTracesLimit {
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "limit must be between 1 and " + strconv.Itoa(maxTracesLimit)})
			return
		}
		limit = n
	}
	all := traces.Group(events)
	summaries := make([]traces.Summary, 0, min(limit, len(all)))
	for _, t := range all {
		if len(summaries) == limit {
			break
		}
		summaries = append(summaries, t.Summary)
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"traces": summaries})
}
