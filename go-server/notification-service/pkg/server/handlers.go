package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/notification"
	"github.com/example/notification-service/pkg/pipeline"
	"github.com/example/notification-service/pkg/util"
)

// Handler returns the HTTP handler with all routes and middleware attached.
// Logging wraps auth so rejected requests are still logged and counted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	protected := func(h http.HandlerFunc) http.Handler {
		return s.corsMiddleware(s.loggingMiddleware(s.authMiddleware(s.timeoutMiddleware(h))))
	}
	// Sign-in routes that must work before anyone is signed in.
	public := func(h http.HandlerFunc) http.Handler {
		return s.corsMiddleware(s.loggingMiddleware(s.timeoutMiddleware(h)))
	}
	mux.Handle(authPrefix+"/status", public(s.authStatusHandler))
	mux.Handle(authPrefix+"/login", public(s.loginHandler))
	mux.Handle(authPrefix+"/oidc/start", public(s.oidcStartHandler))
	mux.Handle(authPrefix+"/oidc/callback", public(s.oidcCallbackHandler))
	mux.Handle(authPrefix+"/logout", protected(s.logoutHandler))
	mux.Handle(authPrefix+"/me", protected(s.meHandler))
	mux.Handle(authPrefix+"/password", protected(s.passwordHandler))
	mux.Handle(usersPrefix, protected(s.usersHandler))
	mux.Handle(usersPrefix+"/", protected(s.usersHandler))
	mux.Handle("/api/audit", protected(s.auditHandler))
	mux.Handle(digestsPrefix, protected(s.digestsHandler))
	mux.Handle(digestsPrefix+"/", protected(s.digestsHandler))
	mux.Handle(explainPrefix, protected(s.explainHandler))
	mux.Handle(attentionStatusPath, protected(s.attentionStatusHandler))
	mux.Handle(mePreferencesPath, protected(s.mePreferencesHandler))
	mux.Handle(learningPrefix, protected(s.learningHandler))
	mux.Handle(learningPrefix+"/", protected(s.learningHandler))
	mux.Handle(overridesPrefix, protected(s.overridesHandler))
	mux.Handle(overridesPrefix+"/", protected(s.overridesHandler))
	mux.Handle("/notify", protected(s.notifyHandler))
	mux.Handle("/metrics", protected(s.metricsHandler))
	mux.Handle("/feedback", protected(s.feedbackHandler))
	mux.Handle("/events", protected(s.eventsHandler))
	mux.Handle(inboxPrefix, protected(s.inboxHandler))
	mux.Handle(inboxPrefix+"/", protected(s.inboxHandler))
	mux.Handle("/api/analytics", protected(s.analyticsHandler))
	mux.Handle("/api/activity", protected(s.activityHandler))
	mux.Handle(rulesPrefix, protected(s.rulesHandler))
	mux.Handle(rulesPrefix+"/", protected(s.rulesHandler))
	mux.Handle("/api/policies", protected(s.policiesHandler))
	mux.Handle(channelsPrefix, protected(s.channelsHandler))
	mux.Handle(channelsPrefix+"/", protected(s.channelsHandler))
	mux.Handle(deliveriesPrefix, protected(s.deliveriesHandler))
	mux.Handle(deliveriesPrefix+"/", protected(s.deliveriesHandler))
	mux.Handle("/api/integrations", protected(s.integrationsHandler))
	mux.Handle("/api/preferences", protected(s.preferencesHandler))
	// The stream is long-lived, so it skips the request timeout; it extends its own write deadline.
	mux.Handle("/api/stream", s.corsMiddleware(s.loggingMiddleware(s.authMiddleware(http.HandlerFunc(s.streamHandler)))))
	// Health is unauthenticated so orchestrator probes and the dashboard work without a key.
	mux.Handle("/health", s.corsMiddleware(s.loggingMiddleware(http.HandlerFunc(s.healthHandler))))
	mux.Handle("/webhooks/github", s.corsMiddleware(s.loggingMiddleware(
		s.webhookAuthMiddleware(s.timeoutMiddleware(http.HandlerFunc(s.githubWebhookHandler))))))
	return requestIDMiddleware(mux)
}

// Run serves HTTP until ctx is cancelled, then shuts down gracefully,
// letting in-flight requests finish for up to shutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
	s.http = &http.Server{
		Addr:              s.cfg.Address,
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	if s.cfg.GitHubWebhookSecret == "" && s.cfg.AllowUnsignedWebhooks {
		s.logFor(ctx).Warn("GITHUB_WEBHOOK_SECRET unset; accepting unsigned webhooks (ALLOW_UNSIGNED_WEBHOOKS=true)")
	}
	if s.cfg.APIKey == "" && s.cfg.AllowUnauthenticated {
		s.logFor(ctx).Warn("API_KEY unset; serving the API without authentication (ALLOW_UNAUTHENTICATED_API=true)")
	}

	var background sync.WaitGroup
	if s.purger != nil && s.cfg.EventRetention > 0 {
		background.Add(1)
		go func() {
			defer background.Done()
			s.runRetention(ctx, s.purger)
		}()
	}

	if s.dispatcher != nil {
		background.Add(1)
		go func() {
			defer background.Done()
			s.dispatcher.Run(ctx)
		}()
	}

	if s.digestScheduler != nil {
		background.Add(1)
		go func() {
			defer background.Done()
			s.digestScheduler.Run(ctx)
		}()
	}

	if s.attentionWorker != nil {
		background.Add(1)
		go func() {
			defer background.Done()
			s.attentionWorker.Run(ctx)
		}()
	}

	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		// End live streams first; a stream never finishes on its own, so Shutdown would wait for it.
		s.stopStreams()
		// ctx is already cancelled, so shutdown keeps its values but gets its own deadline.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		shutdownDone <- s.http.Shutdown(shutdownCtx)
	}()

	s.logFor(ctx).Info("notification service listening", "address", s.cfg.Address)
	serveErr := s.http.ListenAndServe()
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = <-shutdownDone
	}
	background.Wait()
	return errors.Join(serveErr, s.closeAll())
}

func (s *Server) notifyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}

	var req notification.NotificationRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}

	if err := s.service.Send(r.Context(), req); err != nil {
		if errors.Is(err, notification.ErrInvalidRequest) {
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
			return
		}
		s.logFor(r.Context()).Error("send notification failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		return
	}

	s.metrics.NotificationAccepted()
	util.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"service": "attention-orchestration",
	})
}

// metricsHandler serves the Prometheus text exposition format.
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.metrics.WritePrometheus(w)
}

func (s *Server) githubWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}

	eventType := r.Header.Get(githubEventHeader)
	if !validEventType(eventType) {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "missing or invalid github event header"})
		return
	}

	var payload map[string]interface{}
	if !s.decodeJSON(w, r, &payload) {
		return
	}

	normalized := s.normalizeGitHubEvent(eventType, r.Header.Get(githubDeliveryHdr), payload)

	result, err := s.eventPipeline.Handle(r.Context(), normalized)
	if errors.Is(err, pipeline.ErrDuplicateDelivery) {
		s.logFor(r.Context()).Info("ignored redelivered event", "event_id", normalized.ID)
		util.WriteJSON(w, http.StatusOK, map[string]string{"status": "duplicate", "eventId": normalized.ID})
		return
	}
	if err != nil {
		s.logFor(r.Context()).Error("pipeline failed",
			"event_id", normalized.ID, "event_type", eventType, "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		return
	}

	s.logFor(r.Context()).Info("event processed",
		"event_id", result.ID,
		"thread_id", result.ThreadID,
		"attention_class", result.AttentionClass,
		"decision_status", result.DecisionState.Status)

	util.WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":         "accepted",
		"eventId":        result.ID,
		"attentionClass": result.AttentionClass,
		"decisionState":  result.DecisionState,
	})
}

// normalizeGitHubEvent maps a webhook to the internal event. The event ID embeds
// GitHub's delivery ID so a redelivery maps to the same ID and is recognised;
// without a usable delivery ID a random one is generated.
func (s *Server) normalizeGitHubEvent(eventType, deliveryID string, body map[string]interface{}) event.Event {
	if body == nil {
		body = map[string]interface{}{}
	}

	repository := ""
	if repo, ok := body["repository"].(map[string]interface{}); ok {
		if fullName, ok := repo["full_name"].(string); ok {
			repository = fullName
		}
	}

	if !validDeliveryID(deliveryID) {
		deliveryID = randomID()
	}
	return event.Event{
		ID:         fmt.Sprintf("github-%s-%s", eventType, deliveryID),
		Source:     "github",
		EventType:  eventType,
		Repository: repository,
		Payload:    body,
	}
}

// validDeliveryID accepts short IDs of letters, digits and dashes (GitHub sends a GUID).
func validDeliveryID(id string) bool {
	if id == "" || len(id) > maxDeliveryIDLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

// validEventType accepts GitHub event names: short, lowercase letters and underscores.
func validEventType(t string) bool {
	if t == "" || len(t) > maxDeliveryIDLen {
		return false
	}
	for _, c := range t {
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return true
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is effectively fatal on a healthy host; fall back to time
		// so the request still gets a (less unique) ID instead of panicking.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

const (
	defaultEventsLimit = 100
	maxEventsLimit     = 500
)

// eventSummary is the read model for the event list. It leaves out the raw
// payload, which can carry names and emails and is large.
type eventSummary struct {
	ID             string               `json:"id"`
	Source         string               `json:"source"`
	EventType      string               `json:"eventType"`
	Repository     string               `json:"repository,omitempty"`
	AttentionClass string               `json:"attentionClass"`
	DecisionState  *event.DecisionState `json:"decisionState,omitempty"`
	ThreadID       string               `json:"threadId,omitempty"`
	MergedInto     string               `json:"mergedInto,omitempty"`
	CreatedAt      time.Time            `json:"createdAt"`
}

// eventsHandler lists the most recent events, newest first.
func (s *Server) eventsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}

	limit := defaultEventsLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxEventsLimit {
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{
				Error: "limit must be a whole number from 1 to " + strconv.Itoa(maxEventsLimit)})
			return
		}
		limit = n
	}

	stored, err := s.eventRepo.List(r.Context())
	if err != nil {
		s.logFor(r.Context()).Error("list events failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		return
	}

	// List returns oldest first; the console wants newest first.
	summaries := make([]eventSummary, 0, min(limit, len(stored)))
	for i := len(stored) - 1; i >= 0 && len(summaries) < limit; i-- {
		e := stored[i]
		summaries = append(summaries, eventSummary{
			ID: e.ID, Source: e.Source, EventType: e.EventType, Repository: e.Repository,
			AttentionClass: e.AttentionClass, DecisionState: e.DecisionState,
			ThreadID: e.ThreadID, MergedInto: e.MergedInto, CreatedAt: e.CreatedAt,
		})
	}
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{"events": summaries})
}
