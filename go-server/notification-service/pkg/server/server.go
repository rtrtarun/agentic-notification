package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/db"
	"github.com/example/notification-service/pkg/dedup"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/feedback"
	"github.com/example/notification-service/pkg/learning"
	"github.com/example/notification-service/pkg/notification"
	"github.com/example/notification-service/pkg/notification/impl"
	"github.com/example/notification-service/pkg/pipeline"
	"github.com/example/notification-service/pkg/policy"
	"github.com/example/notification-service/pkg/util"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
	githubEventHeader = "X-GitHub-Event"
	errInternal       = "internal error"
	errInvalidPayload = "invalid request payload"
	errMethod         = "method not allowed"
)

type Server struct {
	cfg                *config.Config
	logger             *slog.Logger
	service            notification.NotificationService
	deliveryService    *delivery.Service
	eventRepo          event.Repository
	feedbackRecorder   feedback.Recorder
	learningService    *learning.Service
	policyEngine       *policy.Engine
	eventPipeline      *pipeline.Pipeline
	http               *http.Server
	requestsTotal      atomic.Uint64
	notificationsTotal atomic.Uint64
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.statusCode == 0 {
		r.statusCode = code
	}
	r.ResponseWriter.WriteHeader(code)
}

// NewServer wires the service. ctx bounds start-up work such as the database connection.
func NewServer(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}

	var provider delivery.Provider = &delivery.StubProvider{}
	if cfg != nil && cfg.SMTPHost != "" && cfg.SMTPSender != "" {
		provider = delivery.NewSMTPProvider(cfg)
	}

	eventRepo, err := initEventRepository(ctx, cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("init event repository: %w", err)
	}

	rules, err := loadPolicyRules(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("load policy rules: %w", err)
	}

	deliverySvc := delivery.NewService(provider)
	policyEngine := policy.NewEngine(rules)

	s := &Server{
		cfg:              cfg,
		logger:           logger,
		service:          impl.NewNotificationService(cfg),
		deliveryService:  deliverySvc,
		eventRepo:        eventRepo,
		feedbackRecorder: feedback.NewInMemoryRecorder(),
		learningService:  learning.NewService(""),
		policyEngine:     policyEngine,
		eventPipeline: pipeline.New(
			policyEngine,
			deliverySvc,
			eventRepo,
			pipeline.Config{
				DefaultEmailRecipients: cfg.DefaultEmailRecipients(),
				SlackWebhookURLs:       cfg.SlackWebhookURLs(),
			},
		).WithDeduper(dedup.NewMemoryDeduper(cfg.DedupWindow)),
	}
	return s, nil
}

func initEventRepository(ctx context.Context, cfg *config.Config, logger *slog.Logger) (event.Repository, error) {
	if cfg == nil || cfg.DatabaseURL == "" {
		return event.NewInMemoryRepository(), nil
	}

	sqlDB, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}
	logger.Info("connected to PostgreSQL and ran migrations")
	return event.NewPostgresRepository(sqlDB), nil
}

func loadPolicyRules(cfg *config.Config, logger *slog.Logger) ([]policy.Rule, error) {
	if cfg != nil && cfg.RulesPath != "" {
		rules, err := policy.LoadRulesFromFile(cfg.RulesPath)
		if err != nil {
			return nil, err
		}
		logger.Info("loaded policy rules", "count", len(rules), "path", cfg.RulesPath)
		return rules, nil
	}
	rules := policy.DefaultRules()
	logger.Info("using built-in default policy rules", "count", len(rules))
	return rules, nil
}

// Handler returns the HTTP handler with all routes and middleware attached.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/notify", s.corsMiddleware(s.authMiddleware(s.loggingMiddleware(http.HandlerFunc(s.notifyHandler)))))
	mux.Handle("/health", s.corsMiddleware(s.authMiddleware(s.loggingMiddleware(http.HandlerFunc(s.healthHandler)))))
	mux.Handle("/metrics", s.corsMiddleware(s.authMiddleware(s.loggingMiddleware(http.HandlerFunc(s.metricsHandler)))))
	mux.Handle("/webhooks/github", s.corsMiddleware(s.loggingMiddleware(s.webhookAuthMiddleware(http.HandlerFunc(s.githubWebhookHandler)))))
	mux.Handle("/feedback", s.corsMiddleware(s.authMiddleware(s.loggingMiddleware(http.HandlerFunc(s.feedbackHandler)))))
	return requestIDMiddleware(mux)
}

// Run serves HTTP until ctx is cancelled, then shuts down gracefully,
// letting in-flight requests finish for up to shutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
	s.http = &http.Server{
		Addr:              s.cfg.Address,
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	if s.cfg.GitHubWebhookSecret == "" && s.cfg.AllowUnsignedWebhooks {
		s.logFor(ctx).Warn("GITHUB_WEBHOOK_SECRET unset; accepting unsigned webhooks (ALLOW_UNSIGNED_WEBHOOKS=true)")
	}

	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		// The parent context is already cancelled, so shutdown needs its own deadline.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownDone <- s.http.Shutdown(shutdownCtx)
	}()

	s.logFor(ctx).Info("notification service listening", "address", s.cfg.Address)
	if err := s.http.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-shutdownDone
}

func (s *Server) notifyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}

	var req notification.NotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: errInvalidPayload})
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

	s.notificationsTotal.Add(1)
	util.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"service": "attention-orchestration",
	})
}

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"requests":      s.requestsTotal.Load(),
		"notifications": s.notificationsTotal.Load(),
	})
}

func (s *Server) feedbackHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}

	var fb feedback.EventFeedback
	if err := json.NewDecoder(r.Body).Decode(&fb); err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: errInvalidPayload})
		return
	}
	if fb.EventID == "" || fb.Action == "" {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "event id and action are required"})
		return
	}

	if err := s.feedbackRecorder.Record(fb); err != nil {
		s.logFor(r.Context()).Error("record feedback failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		return
	}

	util.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) githubWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
		return
	}

	eventType := r.Header.Get(githubEventHeader)
	if eventType == "" {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "missing github event header"})
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: errInvalidPayload})
		return
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: errInvalidPayload})
		return
	}

	normalized, err := s.normalizeGitHubEvent(eventType, payloadBytes)
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: errInvalidPayload})
		return
	}

	result, err := s.eventPipeline.Handle(r.Context(), normalized)
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

func (s *Server) normalizeGitHubEvent(eventType string, payload []byte) (event.Event, error) {
	var body map[string]interface{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &body); err != nil {
			return event.Event{}, fmt.Errorf("invalid payload: %w", err)
		}
	} else {
		body = map[string]interface{}{}
	}

	repository := ""
	if repo, ok := body["repository"].(map[string]interface{}); ok {
		if fullName, ok := repo["full_name"].(string); ok {
			repository = fullName
		}
	}

	return event.Event{
		ID:         fmt.Sprintf("github-%s-%d", eventType, time.Now().UnixNano()),
		Source:     "github",
		EventType:  eventType,
		Repository: repository,
		Payload:    body,
	}, nil
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		s.requestsTotal.Add(1)
		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logFor(r.Context()).Info("request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.statusCode,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey != "" {
			if apiKey := r.Header.Get("X-API-Key"); apiKey != s.cfg.APIKey {
				util.WriteJSON(w, http.StatusUnauthorized, util.ErrorResponse{Error: "missing or invalid api key"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, X-Request-ID")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
