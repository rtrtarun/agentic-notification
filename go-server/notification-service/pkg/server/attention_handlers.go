package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/example/notification-service/pkg/attention"
	"github.com/example/notification-service/pkg/attention/llm"
	"github.com/example/notification-service/pkg/claude"
	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/feedback"
	"github.com/example/notification-service/pkg/ollama"
	"github.com/example/notification-service/pkg/util"
)

const (
	digestsPrefix       = "/api/digests"
	explainPrefix       = "/api/explain/"
	attentionStatusPath = "/api/attention/status"
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
	var (
		classifier attention.Classifier = h
		summarizer attention.Summarizer = h
	)
	s.explainer = h
	if m := s.newAttentionModel(h); m != nil {
		s.attentionLLM = m
		classifier, summarizer, s.explainer = m, m, m
	}
	s.classifier = attention.Guarded(classifier)
	s.digests = attention.NewGenerator(s.eventRepo, store, s.classifier, summarizer, digestNotifier{outbox}, s.logger)
	s.digestScheduler = attention.NewScheduler(s.digests, s.cfg.AttentionDigestEvery, s.logger)
}

// newAttentionModel builds the configured language model, or returns nil to use
// the heuristic alone. A model that cannot be built is logged and skipped: the
// service must start and work without one.
func (s *Server) newAttentionModel(h *attention.Heuristic) *llm.Model {
	var c llm.Completer
	switch s.cfg.AttentionProvider {
	case config.ProviderOllama:
		c = ollama.NewCompleter(s.cfg.OllamaURL, s.cfg.OllamaModel)
	case config.ProviderClaude:
		client, err := claude.New(s.cfg.AnthropicAPIKey, s.cfg.AttentionModel, "")
		if err != nil {
			s.logger.Error("attention model disabled, using the heuristic", "provider", s.cfg.AttentionProvider, "error", err)
			return nil
		}
		c = client
	default:
		return nil
	}
	s.logger.Info("attention model enabled", "model", c.Name(), "timeout", s.cfg.AttentionTimeout.String(), "maxCallsPerMinute", s.cfg.AttentionMaxCallsPerMin)
	return llm.New(c, h, llm.Options{Timeout: s.cfg.AttentionTimeout, MaxCallsPerMinute: s.cfg.AttentionMaxCallsPerMin, Logger: s.logger})
}

// attentionStatusHandler reports which engine powers Attention Intelligence and
// how a model has been doing. It is admin only because an error can name an address.
//
//	GET /api/attention/status
func (s *Server) attentionStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}
	if r.URL.Path != attentionStatusPath {
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	if s.attentionLLM == nil {
		util.WriteJSON(w, http.StatusOK, map[string]any{"provider": config.ProviderHeuristic, "model": attention.ModelHeuristic, "breaker": "closed", "calls": 0, "fallbacks": 0})
		return
	}
	util.WriteJSON(w, http.StatusOK, s.attentionLLM.Status())
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
		if r.URL.Query().Get("scope") == scopeMe {
			s.listPersonalDigests(w, r, limit)
			return
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
		if r.URL.Query().Get("scope") == scopeMe {
			s.generatePersonalDigest(w, r)
			return
		}
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
	text, model := "", attention.ModelHeuristic
	if se, ok := s.explainer.(attention.SourcedExplainer); ok {
		text, model, err = se.ExplainSourced(r.Context(), evt)
	} else {
		text, err = s.explainer.Explain(r.Context(), evt)
	}
	if err != nil {
		s.attentionFailure(w, r, err)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"eventId": evt.ID, "explanation": text, "model": model, "source": attention.SourceOf(model)})
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
