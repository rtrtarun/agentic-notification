package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/learning"
	"github.com/agentpulse/notification-service/pkg/util"
)

const (
	adjustmentsPrefix = "/api/learning/adjustments"
	runsPrefix        = "/api/learning/runs"
	// snapshotRefresh is how often the in-memory set of approved adjustments is reloaded, so a change made
	// through another service reaches this one. The set is used only while it is younger than attention.DefaultTuningMaxAge.
	snapshotRefresh = 30 * time.Second
	runsShown       = 20
)

// initTuning builds the offline score tuner over the stores that already exist and loads the first snapshot.
func (s *Server) initTuning(ctx context.Context) {
	var (
		adjustments learning.AdjustmentRepository = learning.NewMemoryAdjustments()
		runs        learning.RunRepository        = learning.NewMemoryRuns()
	)
	if s.sqlDB != nil {
		adjustments, runs = learning.NewPostgresAdjustments(s.sqlDB), learning.NewPostgresRuns(s.sqlDB)
	}
	s.tuner = learning.NewTuner(adjustments, runs, s.feedbackStore, s.eventRepo, s.rules, s.tuning, learning.TunerConfig{
		MinSignals:    s.cfg.LearningMinSignals,
		MinPeople:     s.cfg.LearningMinPeople,
		MaxAdjustment: s.cfg.LearningMaxAdjustment,
		Lookback:      s.cfg.LearningLookback,
		HalfLife:      s.cfg.LearningHalfLife,
	}).WithLogger(s.logger)
	if err := s.tuner.RefreshSnapshot(ctx); err != nil {
		// The service starts and scores as it did without learning until the next refresh works.
		s.logger.Warn("loading the learned adjustments failed; scoring without them", "error", err)
	}
}

// adjustmentsHandler lists score adjustments and lets an admin decide on them.
//
//	GET  /api/learning/adjustments?status=       adjustments, newest first (proposed, active, rejected, retired)
//	POST /api/learning/adjustments/{id}/approve  admin only; makes a proposed adjustment active
//	POST /api/learning/adjustments/{id}/reject   admin only; the adjustment is not proposed again
//	POST /api/learning/adjustments/{id}/retire   admin only; switches an active adjustment off
func (s *Server) adjustmentsHandler(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, adjustmentsPrefix), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		status := r.URL.Query().Get("status")
		switch status {
		case "", learning.AdjustmentProposed, learning.AdjustmentActive, learning.AdjustmentRejected, learning.AdjustmentRetired:
		default:
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "status must be proposed, active, rejected or retired"})
			return
		}
		list, err := s.tuner.List(r.Context(), status)
		if err != nil {
			s.tuningFailure(w, r, err)
			return
		}
		if list == nil {
			list = []learning.Adjustment{}
		}
		cfg := s.tuner.Config()
		util.WriteJSON(w, http.StatusOK, map[string]any{
			"adjustments": list, "version": s.tuner.Version(), "maxAdjustment": cfg.MaxAdjustment, "minSignals": cfg.MinSignals, "minPeople": cfg.MinPeople,
		})
	case rest != "" && r.Method == http.MethodPost:
		id, action, ok := strings.Cut(rest, "/")
		if !ok || id == "" || (action != "approve" && action != "reject" && action != "retire") {
			util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
			return
		}
		if !s.requireAdmin(w, r) {
			return
		}
		s.decideAdjustment(w, r, id, action)
	default:
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
	}
}

func (s *Server) decideAdjustment(w http.ResponseWriter, r *http.Request, id, action string) {
	actor := actorOf(r)
	var (
		adj learning.Adjustment
		err error
	)
	switch action {
	case "approve":
		adj, err = s.tuner.Approve(r.Context(), id, actor)
	case "reject":
		adj, err = s.tuner.Reject(r.Context(), id, actor)
	default:
		adj, err = s.tuner.Retire(r.Context(), id, actor)
	}
	if err != nil {
		s.tuningFailure(w, r, err)
		return
	}
	s.auditChange(r, "learning."+action, "adjustment "+adj.Source+" "+adj.EventType, adj.Evidence.Summary)
	util.WriteJSON(w, http.StatusOK, map[string]any{"adjustment": adj})
}

// runsHandler starts and lists learning runs.
//
//	GET  /api/learning/runs             the latest runs and what each read and made
//	POST /api/learning/runs             admin only; starts a run and returns at once with 202 and the run
//	POST /api/learning/runs?wait=true   admin only; waits for the run to finish and returns it with 200
func (s *Server) runsHandler(w http.ResponseWriter, r *http.Request) {
	if strings.Trim(strings.TrimPrefix(r.URL.Path, runsPrefix), "/") != "" {
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.tuner.Runs(r.Context(), runsShown)
		if err != nil {
			s.tuningFailure(w, r, err)
			return
		}
		if list == nil {
			list = []learning.Run{}
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"runs": list, "every": s.cfg.LearningTrainEvery.String()})
	case http.MethodPost:
		if !s.requireAdmin(w, r) {
			return
		}
		if r.URL.Query().Get("wait") == "true" {
			run, err := s.tuner.RunNow(r.Context())
			if err != nil {
				s.tuningFailure(w, r, err)
				return
			}
			s.auditChange(r, "learning.run", "run "+run.ID, run.Status)
			util.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
			return
		}
		run, err := s.tuner.Start(r.Context())
		if err != nil {
			s.tuningFailure(w, r, err)
			return
		}
		s.auditChange(r, "learning.run", "run "+run.ID, "started")
		util.WriteJSON(w, http.StatusAccepted, map[string]any{"run": run})
	default:
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
	}
}

func (s *Server) tuningFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, learning.ErrAdjustmentNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, learning.ErrNotProposed), errors.Is(err, learning.ErrNotActive), errors.Is(err, learning.ErrRunInProgress):
		util.WriteJSON(w, http.StatusConflict, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, learning.ErrTuneGuardrail):
		util.WriteJSON(w, http.StatusForbidden, util.ErrorResponse{Error: err.Error()})
	default:
		s.logFor(r.Context()).Error("learning request failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	}
}

// newTuning is the snapshot the heuristic reads and the tuner keeps current.
func newTuning() *attention.Tuning { return attention.NewTuning(attention.DefaultTuningMaxAge) }
