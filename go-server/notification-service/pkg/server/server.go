package server

import (
	"encoding/json"
	"fmt"
	"log"
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

type Server struct {
	cfg                *config.Config
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

func NewServer(cfg *config.Config) (*Server, error) {
	var provider delivery.Provider = &delivery.StubProvider{}
	if cfg != nil && cfg.SMTPHost != "" && cfg.SMTPSender != "" {
		provider = delivery.NewSMTPProvider(cfg)
	}

	eventRepo, err := initEventRepository(cfg)
	if err != nil {
		return nil, fmt.Errorf("init event repository: %w", err)
	}

	rules, err := loadPolicyRules(cfg)
	if err != nil {
		return nil, fmt.Errorf("load policy rules: %w", err)
	}

	deliverySvc := delivery.NewService(provider)
	policyEngine := policy.NewEngine(rules)

	s := &Server{
		cfg:              cfg,
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

func initEventRepository(cfg *config.Config) (event.Repository, error) {
	if cfg == nil || cfg.DatabaseURL == "" {
		return event.NewInMemoryRepository(), nil
	}

	sqlDB, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}
	log.Printf("connected to PostgreSQL and ran migrations")
	return event.NewPostgresRepository(sqlDB), nil
}

func loadPolicyRules(cfg *config.Config) ([]policy.Rule, error) {
	if cfg != nil && cfg.RulesPath != "" {
		rules, err := policy.LoadRulesFromFile(cfg.RulesPath)
		if err != nil {
			return nil, err
		}
		log.Printf("loaded %d policy rules from %s", len(rules), cfg.RulesPath)
		return rules, nil
	}
	log.Printf("using %d built-in default policy rules", len(policy.DefaultRules()))
	return policy.DefaultRules(), nil
}

func (s *Server) Run() error {
	mux := http.NewServeMux()
	mux.Handle("/notify", s.corsMiddleware(s.authMiddleware(s.loggingMiddleware(http.HandlerFunc(s.notifyHandler)))))
	mux.Handle("/health", s.corsMiddleware(s.authMiddleware(s.loggingMiddleware(http.HandlerFunc(s.healthHandler)))))
	mux.Handle("/metrics", s.corsMiddleware(s.authMiddleware(s.loggingMiddleware(http.HandlerFunc(s.metricsHandler)))))
	mux.Handle("/webhooks/github", s.corsMiddleware(s.loggingMiddleware(s.webhookAuthMiddleware(http.HandlerFunc(s.githubWebhookHandler)))))
	mux.Handle("/feedback", s.corsMiddleware(s.authMiddleware(s.loggingMiddleware(http.HandlerFunc(s.feedbackHandler)))))

	s.http = &http.Server{
		Addr:    s.cfg.Address,
		Handler: mux,
	}

	if s.cfg.GitHubWebhookSecret == "" && s.cfg.AllowUnsignedWebhooks {
		log.Printf("WARNING: GITHUB_WEBHOOK_SECRET unset; accepting unsigned webhooks (ALLOW_UNSIGNED_WEBHOOKS=true)")
	}
	log.Printf("notification service listening on %s", s.cfg.Address)
	return s.http.ListenAndServe()
}

func (s *Server) notifyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: "method not allowed"})
		return
	}

	var req notification.NotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "invalid request payload"})
		return
	}

	if err := s.service.Send(req); err != nil {
		log.Printf("notification error: %v", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: err.Error()})
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
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: "method not allowed"})
		return
	}

	var fb feedback.EventFeedback
	if err := json.NewDecoder(r.Body).Decode(&fb); err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "invalid request payload"})
		return
	}
	if fb.EventID == "" || fb.Action == "" {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "event id and action are required"})
		return
	}

	if err := s.feedbackRecorder.Record(fb); err != nil {
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: err.Error()})
		return
	}

	util.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) githubWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: "method not allowed"})
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "" {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "missing github event header"})
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "invalid request payload"})
		return
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "invalid request payload"})
		return
	}

	normalized, err := s.normalizeGitHubEvent(eventType, payloadBytes)
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
		return
	}

	result, err := s.eventPipeline.Handle(normalized)
	if err != nil {
		log.Printf("pipeline error: %v", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: err.Error()})
		return
	}

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
		log.Printf("incoming request method=%s path=%s remote=%s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(recorder, r)
		log.Printf("completed request method=%s path=%s status=%d duration=%s", r.Method, r.URL.Path, recorder.statusCode, time.Since(start))
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
