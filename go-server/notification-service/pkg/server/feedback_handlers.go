package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/feedback"
	"github.com/agentpulse/notification-service/pkg/overrides"
	"github.com/agentpulse/notification-service/pkg/util"
)

const overridesPrefix = "/api/overrides"

// initFeedback builds the overrides and feedback services on the event store and, with a database, on PostgreSQL.
func (s *Server) initFeedback(ctx context.Context) error {
	var (
		ovStore overrides.Store = overrides.NewMemoryStore()
		fbStore feedback.Store  = feedback.NewMemoryStore()
	)
	if s.sqlDB != nil {
		ovStore, fbStore = overrides.NewPostgresStore(s.sqlDB), feedback.NewPostgresStore(s.sqlDB)
	}
	ov, err := overrides.NewService(ctx, ovStore)
	if err != nil {
		return err
	}
	s.overrides, s.feedbackStore = ov, fbStore
	s.feedback = feedback.NewService(fbStore, s.lookupEvent, ov)
	return nil
}

// lookupEvent finds an event among the recent ones, the same window the inbox reads.
func (s *Server) lookupEvent(ctx context.Context, id string) (event.Event, error) {
	events, err := s.eventRepo.List(ctx)
	if err != nil {
		return event.Event{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].ID == id {
			return events[i], nil
		}
	}
	return event.Event{}, feedback.ErrEventNotFound
}

// mayOverride says whether the caller may create or remove always/never overrides.
func (s *Server) mayOverride(r *http.Request) bool {
	if !s.cfg.OverridesAdminOnly {
		return true
	}
	p, ok := principalFrom(r.Context())
	return ok && p.admin()
}

func actorOf(r *http.Request) string {
	if p, ok := principalFrom(r.Context()); ok {
		return p.actor()
	}
	return apiKeyLabel
}

// feedbackHandler records a signal about a notification.
//
//	POST /feedback {"eventId","action","scope","reason"}
//
// Actions: opened, dismissed, undo, always, never, reply. "always" and "never"
// create a visible override (scope: type, repository or source) unless this
// service only lets admins do that, in which case they are recorded as signals.
// "undo" removes the override the same person's latest always or never created.
func (s *Server) feedbackHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	var in struct {
		EventID string `json:"eventId"`
		Action  string `json:"action"`
		Scope   string `json:"scope"`
		Reason  string `json:"reason"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	if s.feedback == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "feedback is not available"})
		return
	}
	actor := actorOf(r)
	res, err := s.feedback.Record(r.Context(), actor, feedback.Input{EventID: in.EventID, Action: in.Action, Scope: in.Scope, Reason: in.Reason}, s.mayOverride(r))
	switch {
	case errors.Is(err, feedback.ErrInvalid):
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, feedback.ErrEventNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "event not found"})
	case errors.Is(err, overrides.ErrInvalid):
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
	case err != nil:
		s.logFor(r.Context()).Error("record feedback failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	default:
		if res.Override != nil || res.Reverted {
			s.auditChange(r, "override."+in.Action, overrideTarget(res), "")
		}
		util.WriteJSON(w, http.StatusAccepted, map[string]any{
			"status": "accepted", "override": res.Override, "overrideSkipped": res.OverrideSkipped, "reverted": res.Reverted,
		})
	}
}

func overrideTarget(res feedback.Result) string {
	if res.Override != nil {
		return overrides.Describe(*res.Override)
	}
	return res.Feedback.EventID
}

// auditChange writes a security-relevant change to the audit trail, never failing the request.
func (s *Server) auditChange(r *http.Request, action, target, detail string) {
	if s.auth != nil {
		s.auth.AuditEvent(r.Context(), actorOf(r), action, target, clientIP(r), detail)
	}
}

// recordOpened notes that a person opened a notification, once per person and event. Best effort.
func (s *Server) recordOpened(r *http.Request, eventID string) {
	if s.feedback == nil {
		return
	}
	if err := s.feedback.RecordOpened(r.Context(), actorOf(r), eventID); err != nil {
		s.logFor(r.Context()).Warn("record opened failed", "error", err)
	}
}

// overridesHandler lists and manages the always and never notify choices.
//
//	GET    /api/overrides         every override, newest first
//	POST   /api/overrides         {"mode","source","eventType","repository","note"}
//	DELETE /api/overrides/{id}    remove one
func (s *Server) overridesHandler(w http.ResponseWriter, r *http.Request) {
	if s.overrides == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "overrides are not available"})
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, overridesPrefix), "/")
	switch {
	case id == "" && r.Method == http.MethodGet:
		list := s.overrides.List()
		if list == nil {
			list = []overrides.Override{}
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"overrides": list, "adminOnly": s.cfg.OverridesAdminOnly})
	case id == "" && r.Method == http.MethodPost:
		if !s.mayOverrideOrDeny(w, r) {
			return
		}
		var in overrides.Override
		if !s.decodeJSON(w, r, &in) {
			return
		}
		in.CreatedBy = actorOf(r)
		saved, err := s.overrides.Upsert(r.Context(), in)
		if err != nil {
			s.overrideFailure(w, r, err)
			return
		}
		s.auditChange(r, "override."+saved.Mode, overrides.Describe(saved), "")
		util.WriteJSON(w, http.StatusCreated, saved)
	case id != "" && !strings.Contains(id, "/") && r.Method == http.MethodDelete:
		if !s.mayOverrideOrDeny(w, r) {
			return
		}
		if err := s.overrides.Delete(r.Context(), id); err != nil {
			s.overrideFailure(w, r, err)
			return
		}
		s.auditChange(r, "override.removed", id, "")
		w.WriteHeader(http.StatusNoContent)
	default:
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
	}
}

func (s *Server) mayOverrideOrDeny(w http.ResponseWriter, r *http.Request) bool {
	if s.mayOverride(r) {
		return true
	}
	util.WriteJSON(w, http.StatusForbidden, util.ErrorResponse{Error: "admin access required"})
	return false
}

func (s *Server) overrideFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, overrides.ErrInvalid):
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, overrides.ErrNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: err.Error()})
	default:
		s.logFor(r.Context()).Error("override request failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	}
}
