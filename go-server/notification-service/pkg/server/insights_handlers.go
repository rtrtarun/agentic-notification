package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/agentpulse/notification-service/pkg/insights"
	"github.com/agentpulse/notification-service/pkg/util"
)

// analyticsHandler serves GET /api/analytics?range=24h|7d|30d (default 7d).
func (s *Server) analyticsHandler(w http.ResponseWriter, r *http.Request) {
	if s.insights == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "insights are not available"})
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	rng, err := insights.ParseRange(r.URL.Query().Get("range"))
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
		return
	}
	result, err := s.insights.Analytics(r.Context(), rng)
	if err != nil {
		s.insightsFailed(w, r, "analytics", err)
		return
	}
	util.WriteJSON(w, http.StatusOK, result)
}

// activityHandler serves GET /api/activity?limit=1..200 (default 50).
func (s *Server) activityHandler(w http.ResponseWriter, r *http.Request) {
	if s.insights == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "insights are not available"})
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	n := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "limit must be a whole number from 1 to 200"})
			return
		}
		n = v
	}
	limit, err := insights.ParseLimit(n)
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
		return
	}
	entries, err := s.insights.Activity(r.Context(), limit)
	if err != nil {
		s.insightsFailed(w, r, "activity", err)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) insightsFailed(w http.ResponseWriter, r *http.Request, what string, err error) {
	if errors.Is(err, insights.ErrInvalidRange) {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
		return
	}
	s.logFor(r.Context()).Error(what+" failed", "error", err)
	util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
}
