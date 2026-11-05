package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/example/notification-service/pkg/attention"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/feedback"
	"github.com/example/notification-service/pkg/util"
)

const (
	digestsPrefix = "/api/digests"
	explainPrefix = "/api/explain/"
)

// digestNotifier hands a finished digest to the delivery queue, as one message per channel routed for digests.
type digestNotifier struct{ outbox *delivery.Outbox }

func (n digestNotifier) Notify(ctx context.Context, digestID, title, body string) error {
	_, err := n.outbox.Enqueue(ctx, delivery.Message{EventID: "digest-" + digestID, Class: "digest", Title: title, Body: body, Group: "attention"})
	return err
}

// initAttention builds the digest generator and its store.
func (s *Server) initAttention(outbox *delivery.Outbox) {
	var store attention.DigestStore = attention.NewMemoryDigestStore()
	if s.sqlDB != nil {
		store = attention.NewPostgresDigestStore(s.sqlDB)
	}
	s.digestStore = store
	h := attention.NewHeuristic()
	s.explainer = h
	s.digests = attention.NewGenerator(s.eventRepo, store, h, h, digestNotifier{outbox}, s.logger)
	s.digestScheduler = attention.NewScheduler(s.digests, s.cfg.AttentionDigestEvery, s.logger)
}

// digestsHandler lists digests and makes one on request.
//
//	GET  /api/digests            newest first
//	GET  /api/digests/{id}       one digest
//	POST /api/digests/generate   cover everything held back since the last digest (admin)
func (s *Server) digestsHandler(w http.ResponseWriter, r *http.Request) {
	if s.digests == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "digests are not available"})
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, digestsPrefix), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		limit := 30
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 100 {
				util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "limit must be between 1 and 100"})
				return
			}
			limit = n
		}
		list, err := s.digestStore.List(r.Context(), limit)
		if err != nil {
			s.attentionFailure(w, r, err)
			return
		}
		if list == nil {
			list = []attention.Digest{}
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"digests": list, "every": s.cfg.AttentionDigestEvery.String()})
	case rest == "generate" && r.Method == http.MethodPost:
		if !s.requireAdmin(w, r) {
			return
		}
		d, made, err := s.digests.Generate(r.Context(), actorOf(r))
		if err != nil {
			s.attentionFailure(w, r, err)
			return
		}
		if !made {
			util.WriteJSON(w, http.StatusOK, map[string]any{"generated": false, "digest": nil})
			return
		}
		s.auditChange(r, "digest.generated", d.ID, "")
		util.WriteJSON(w, http.StatusCreated, map[string]any{"generated": true, "digest": d})
	case rest != "" && rest != "generate" && !strings.Contains(rest, "/") && r.Method == http.MethodGet:
		d, err := s.digestStore.Get(r.Context(), rest)
		if err != nil {
			s.attentionFailure(w, r, err)
			return
		}
		util.WriteJSON(w, http.StatusOK, d)
	default:
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
	}
}

// explainHandler says in plain language why one event was handled the way it was.
//
//	GET /api/explain/{eventId}
func (s *Server) explainHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, explainPrefix)
	if id == "" || strings.Contains(id, "/") {
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
		return
	}
	evt, err := s.lookupEvent(r.Context(), id)
	if err != nil {
		s.attentionFailure(w, r, err)
		return
	}
	text, err := s.explainer.Explain(r.Context(), evt)
	if err != nil {
		s.attentionFailure(w, r, err)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"eventId": evt.ID, "explanation": text})
}

func (s *Server) attentionFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, attention.ErrDigestNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, feedback.ErrEventNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "event not found"})
	default:
		s.logFor(r.Context()).Error("attention request failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	}
}
