package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/agentpulse/notification-service/pkg/learning"
	"github.com/agentpulse/notification-service/pkg/overrides"
	"github.com/agentpulse/notification-service/pkg/util"
)

const learningPrefix = "/api/learning/suggestions"

// initLearning builds the learning engine over the stores that already exist.
func (s *Server) initLearning() {
	var repo learning.Repository = learning.NewMemoryRepository()
	if s.sqlDB != nil {
		repo = learning.NewPostgresRepository(s.sqlDB)
	}
	s.learning = learning.NewService(repo, s.feedbackStore, s.eventRepo, s.rules, s.overrides, learning.Config{
		MinSignals: s.cfg.LearningMinSignals,
		Lookback:   s.cfg.LearningLookback,
	})
}

// learningHandler serves reviewable suggestions made from stored feedback.
//
//	GET  /api/learning/suggestions                 pending suggestions, refreshed from the latest feedback
//	POST /api/learning/suggestions/{id}/apply      admin only; makes the change and records an audit entry
//	POST /api/learning/suggestions/{id}/dismiss    admin only; the suggestion is not made again
func (s *Server) learningHandler(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, learningPrefix), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		list, err := s.learning.Review(r.Context())
		if err != nil {
			s.learningFailure(w, r, err)
			return
		}
		if list == nil {
			list = []learning.Suggestion{}
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"suggestions": list, "minSignals": s.learning.MinSignals()})
	case rest != "" && r.Method == http.MethodPost:
		id, action, ok := strings.Cut(rest, "/")
		if !ok || id == "" || (action != "apply" && action != "dismiss") {
			util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
			return
		}
		if !s.requireAdmin(w, r) {
			return
		}
		s.resolveSuggestion(w, r, id, action)
	default:
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
	}
}

func (s *Server) resolveSuggestion(w http.ResponseWriter, r *http.Request, id, action string) {
	actor := actorOf(r)
	var (
		sug learning.Suggestion
		err error
	)
	if action == "apply" {
		sug, err = s.learning.Apply(r.Context(), id, actor)
	} else {
		sug, err = s.learning.Dismiss(r.Context(), id, actor)
	}
	if err != nil {
		s.learningFailure(w, r, err)
		return
	}
	s.auditChange(r, "learning."+action, sug.Kind+" "+sug.Source+" "+sug.EventType, sug.Proposed)
	util.WriteJSON(w, http.StatusOK, map[string]any{"suggestion": sug})
}

func (s *Server) learningFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, learning.ErrNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, learning.ErrNotPending), errors.Is(err, learning.ErrRuleGone):
		util.WriteJSON(w, http.StatusConflict, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, learning.ErrGuardrail):
		util.WriteJSON(w, http.StatusForbidden, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, overrides.ErrInvalid):
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
	default:
		s.logFor(r.Context()).Error("learning request failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	}
}
