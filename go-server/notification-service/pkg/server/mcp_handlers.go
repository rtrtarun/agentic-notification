package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/agentpulse/notification-service/pkg/agentevents"
	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/mcp"
	"github.com/agentpulse/notification-service/pkg/notification"
	"github.com/agentpulse/notification-service/pkg/pipeline"
	"github.com/agentpulse/notification-service/pkg/util"
)

const mcpPath = "/api/mcp"

// mcpBackend runs the MCP tools inside the service, through the same pipeline, validating
// notification service and explainer the REST API uses. cmd/mcp-server reaches the same
// behaviour over HTTP with mcp.HTTPBackend.
type mcpBackend struct{ s *Server }

// PublishEvent implements mcp.Backend.
func (b mcpBackend) PublishEvent(ctx context.Context, in agentevents.Input) (any, error) {
	evt, err := agentevents.Normalize(in)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", mcp.ErrInvalid, err.Error())
	}
	result, err := b.s.eventPipeline.Handle(ctx, evt)
	if errors.Is(err, pipeline.ErrDuplicateDelivery) {
		return map[string]string{"status": "duplicate", "eventId": evt.ID}, nil
	}
	if err != nil {
		b.s.logger.Error("pipeline failed", "event_id", evt.ID, "error", err)
		return nil, errors.New("the event could not be processed")
	}
	return map[string]any{
		"status": "accepted", "eventId": result.ID, "traceId": result.TraceID,
		"attentionClass": result.AttentionClass, "decisionState": result.DecisionState,
	}, nil
}

// RecentEvents implements mcp.Backend.
func (b mcpBackend) RecentEvents(ctx context.Context, traceID string, limit int) (any, error) {
	stored, err := b.s.eventRepo.List(ctx)
	if err != nil {
		return nil, errors.New("the events could not be read")
	}
	out := make([]eventSummary, 0, limit)
	for i := len(stored) - 1; i >= 0 && len(out) < limit; i-- {
		e := stored[i]
		if traceID != "" && e.TraceID != traceID {
			continue
		}
		out = append(out, eventSummary{
			ID: e.ID, Source: e.Source, EventType: e.EventType, Repository: e.Repository,
			AttentionClass: e.AttentionClass, DecisionState: e.DecisionState,
			ThreadID: e.ThreadID, MergedInto: e.MergedInto, CreatedAt: e.CreatedAt,
			TraceID: e.TraceID, ParentEventID: e.ParentEventID, AgentName: e.AgentName,
		})
	}
	return map[string]any{"events": out}, nil
}

// Notify implements mcp.Backend. It uses the validating service, so the host and
// recipient allow-lists apply exactly as they do to POST /notify.
func (b mcpBackend) Notify(ctx context.Context, n mcp.Notification) (any, error) {
	agent := n.Agent
	if agent == "" {
		agent = "mcp"
	}
	err := b.s.service.Send(ctx, notification.NotificationRequest{
		Agent: notification.AgentInfo{Name: agent}, Title: n.Title, Message: n.Message,
		EmailRecipients: n.EmailRecipients, SlackWebhookURLs: n.SlackWebhookURLs, TeamsWebhookURLs: n.TeamsWebhookURLs,
	})
	if errors.Is(err, notification.ErrInvalidRequest) {
		return nil, fmt.Errorf("%w: %s", mcp.ErrInvalid, err.Error())
	}
	if err != nil {
		b.s.logger.Error("send notification failed", "error", err)
		return nil, errors.New("the notification could not be sent")
	}
	return map[string]string{"status": "accepted"}, nil
}

// Explain implements mcp.Backend.
func (b mcpBackend) Explain(ctx context.Context, eventID string) (any, error) {
	evt, err := b.s.lookupEvent(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("%w: event not found", mcp.ErrInvalid)
	}
	text, model := "", attention.ModelHeuristic
	if se, ok := b.s.explainer.(attention.SourcedExplainer); ok {
		text, model, err = se.ExplainSourced(ctx, evt)
	} else {
		text, err = b.s.explainer.Explain(ctx, evt)
	}
	if err != nil {
		return nil, errors.New("the explanation could not be made")
	}
	return map[string]string{"eventId": evt.ID, "explanation": text, "model": model}, nil
}

// mcpHandler is the HTTP transport of the MCP server: one JSON-RPC message per POST.
//
//	POST /api/mcp   200 with the JSON-RPC response, or 202 with no body for a notification
func (s *Server) mcpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
	if err != nil {
		util.WriteJSON(w, http.StatusRequestEntityTooLarge, util.ErrorResponse{Error: errTooLarge})
		return
	}
	reply := s.mcp.Handle(r.Context(), raw)
	if reply == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(reply)
}
