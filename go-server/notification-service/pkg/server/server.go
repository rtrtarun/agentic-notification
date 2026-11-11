package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/attention/llm"
	"github.com/agentpulse/notification-service/pkg/auth"
	"github.com/agentpulse/notification-service/pkg/budget"
	"github.com/agentpulse/notification-service/pkg/channels"
	"github.com/agentpulse/notification-service/pkg/config"
	"github.com/agentpulse/notification-service/pkg/db"
	"github.com/agentpulse/notification-service/pkg/dedup"
	"github.com/agentpulse/notification-service/pkg/delivery"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/feedback"
	"github.com/agentpulse/notification-service/pkg/inbox"
	"github.com/agentpulse/notification-service/pkg/insights"
	"github.com/agentpulse/notification-service/pkg/learning"
	"github.com/agentpulse/notification-service/pkg/mcp"
	"github.com/agentpulse/notification-service/pkg/metrics"
	"github.com/agentpulse/notification-service/pkg/notification"
	"github.com/agentpulse/notification-service/pkg/notification/impl"
	"github.com/agentpulse/notification-service/pkg/overrides"
	"github.com/agentpulse/notification-service/pkg/pipeline"
	"github.com/agentpulse/notification-service/pkg/policy"
	"github.com/agentpulse/notification-service/pkg/preferences"
	"github.com/agentpulse/notification-service/pkg/rules"
	"github.com/agentpulse/notification-service/pkg/sms"
	"github.com/agentpulse/notification-service/pkg/stream"
	"github.com/agentpulse/notification-service/pkg/telemetry"
)

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// writeTimeout must exceed the request timeout so handlers can still answer
	// after a slow delivery hits its own deadline.
	writeTimeout        = 40 * time.Second
	idleTimeout         = 120 * time.Second
	shutdownTimeout     = 10 * time.Second
	redisConnectTimeout = 5 * time.Second
	githubEventHeader   = "X-GitHub-Event"
	githubDeliveryHdr   = "X-GitHub-Delivery"
	maxDeliveryIDLen    = 64
	errInternal         = "internal error"
	errInvalidPayload   = "invalid request payload"
	errTooLarge         = "request body too large"
	errMethod           = "method not allowed"
)

// Server wires the HTTP layer to the pipeline and its dependencies.
type Server struct {
	cfg             *config.Config
	logger          *slog.Logger
	service         notification.NotificationService
	deliveryService *delivery.Service
	deliveryStore   delivery.Store
	dispatcher      *delivery.Dispatcher
	smsGateway      sms.Gateway
	auth            *auth.Service
	oidc            *auth.OIDC
	authSecret      []byte
	eventRepo       event.Repository
	sqlDB           *sql.DB
	inbox           *inbox.Service
	insights        *insights.Service
	rules           *rules.Service
	channelTester   *channels.Tester
	preferences     *preferences.Service
	broker          *stream.Broker
	// shutdown is closed when the server stops, so live streams end instead of delaying shutdown.
	shutdown        chan struct{}
	shutdownOnce    sync.Once
	feedback        *feedback.Service
	feedbackStore   feedback.Store
	learning        *learning.Service
	tuner           *learning.Tuner
	tuning          *attention.Tuning
	digests         *attention.Generator
	digestStore     attention.DigestStore
	digestScheduler *attention.Scheduler
	explainer       attention.Explainer
	classifier      attention.Classifier
	attentionWorker *attention.Worker
	mcp             *mcp.Server
	tracer          *telemetry.Tracer
	redisClient     *redis.Client
	attentionLLM    *llm.Model
	overrides       *overrides.Service
	policyEngine    *policy.Engine
	eventPipeline   *pipeline.Pipeline
	metrics         *metrics.Registry
	purger          event.Purger
	http            *http.Server
	closers         []func() error
}

// NewServer wires the service. ctx bounds start-up work such as the database connection.
func NewServer(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	s := &Server{cfg: cfg, logger: logger, metrics: metrics.NewRegistry(), shutdown: make(chan struct{}), broker: stream.NewBroker(maxLiveConnections)}

	eventRepo, err := s.initEventRepository(ctx)
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("init event repository: %w", err)
	}
	deduper, err := s.initDeduper(ctx)
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("init deduper: %w", err)
	}
	seedRules, err := loadPolicyRules(cfg, logger)
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("load policy rules: %w", err)
	}

	smsGateway, err := buildSMSGateway(cfg, logger)
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("init sms gateway: %w", err)
	}
	targets, err := buildTargets(cfg, smsGateway)
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, err
	}
	routes, err := buildRoutes(cfg)
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, err
	}
	provider := newProviderWith(cfg, logger, smsGateway)
	policyEngine := policy.NewEngine(seedRules)
	deliverySvc := delivery.NewService(provider)

	ruleService, err := rules.NewService(ctx, s.initRulesStore(), policyEngine, seedRules)
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("init rules: %w", err)
	}

	s.eventRepo = eventRepo
	s.rules = ruleService
	s.smsGateway = smsGateway
	if err := s.initAuth(ctx); err != nil {
		s.releaseOnStartupFailure()
		return nil, err
	}
	s.deliveryStore = s.initDeliveryStore()
	outbox := delivery.NewOutbox(s.deliveryStore, targets, routes)
	s.initAttention(outbox)
	s.dispatcher = delivery.NewDispatcher(s.deliveryStore, targets, channelProviders(cfg, smsGateway), logger).
		WithDailyCap(delivery.ChannelSMS, cfg.SMSDailyLimit)
	s.channelTester = channels.NewTester(deliverySvc, s.channelSettings(), channelTestGap)
	prefService, err := preferences.NewService(ctx, s.initPreferencesStore())
	if prefService != nil {
		prefService.WithPersonal(s.initPersonalPreferencesStore())
	}
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("init preferences: %w", err)
	}
	s.preferences = prefService
	s.inbox = inbox.NewService(eventRepo, s.initInboxRepository())
	s.insights = insights.NewService(s.inbox)
	s.deliveryService = deliverySvc
	s.policyEngine = policyEngine
	if err := s.initFeedback(ctx); err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("init feedback: %w", err)
	}
	s.initLearning()
	s.initTuning(ctx)
	s.mcp = mcp.New(mcpBackend{s}, "service")
	s.tracer = telemetry.NewTracer(logger)
	s.service = notification.NewValidatingService(
		impl.NewNotificationServiceWith(provider),
		notification.Limits{
			AllowedWebhookHosts: cfg.WebhookHostAllowList(),
			AllowedEmailDomains: cfg.AllowedEmailDomains,
		},
	)
	s.eventPipeline = pipeline.New(
		policyEngine,
		deliverySvc,
		eventRepo,
		pipeline.Config{
			DefaultEmailRecipients: cfg.DefaultEmailRecipients(),
			SlackWebhookURLs:       cfg.SlackWebhookURLs(),
			TeamsWebhookURLs:       cfg.TeamsWebhookURLs(),
			SMSRecipients:          targets.SMS,
		},
	).WithOutbox(outbox).WithOverrides(s.overrides).WithDeduper(deduper).WithLogger(logger).WithPreferences(s.preferences).WithPublisher(s.broker).WithTracer(s.tracer)
	if cfg.AttentionClassify {
		s.eventPipeline.WithClassifier(s.classifier)
	}
	attentionBudget, err := s.initBudget(ctx)
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("init attention budget: %w", err)
	}
	if attentionBudget != nil {
		s.eventPipeline.WithBudget(attentionBudget)
	}
	if queue, ok := eventRepo.(event.WorkQueue); ok {
		s.attentionWorker = attention.NewWorker(queue, s.eventPipeline, logger).WithInterval(cfg.AttentionWorkerInterval)
		if cfg.AttentionAsync {
			s.eventPipeline.WithAsyncAttention()
		}
	} else if cfg.AttentionAsync {
		s.releaseOnStartupFailure()
		return nil, errors.New("ATTENTION_ASYNC needs an event store that supports a work queue")
	}
	if purger, ok := eventRepo.(event.Purger); ok {
		s.purger = purger
	}
	return s, nil
}

// newProvider builds the delivery provider: email when SMTP is configured, plus
// webhooks, which refuse private addresses so a webhook URL cannot reach internal hosts.
func newProvider(cfg *config.Config, logger *slog.Logger) delivery.Provider {
	return newProviderWith(cfg, logger, nil)
}

// emailProvider is how email leaves the service: through Novu when NOVU_API_KEY is set, with
// SMTP behind it when SMTP is configured too, otherwise through SMTP alone, otherwise nowhere.
// Exactly one provider owns email, so a recipient is never sent the same message twice.
func emailProvider(cfg *config.Config) delivery.Provider {
	var smtpProvider delivery.Provider
	if cfg.SMTPHost != "" && cfg.SMTPSender != "" {
		smtpProvider = delivery.NewSMTPProvider(cfg)
	}
	if cfg.NovuAPIKey == "" {
		return smtpProvider
	}
	novu, err := delivery.NewNovuProvider(cfg.NovuAPIURL, cfg.NovuAPIKey, cfg.NovuWorkflowID)
	if err != nil {
		return smtpProvider // LoadConfig already validated the settings; keep email working if they are somehow wrong
	}
	if smtpProvider == nil {
		return novu
	}
	return delivery.NewFallbackProvider(novu, smtpProvider)
}

// newProviderWith is newProvider plus SMS when a gateway is configured.
func newProviderWith(cfg *config.Config, logger *slog.Logger, gw sms.Gateway) delivery.Provider {
	providers := []delivery.Provider{delivery.NewWebhookProvider(true)}
	if email := emailProvider(cfg); email != nil {
		providers = append(providers, email)
	}
	if gw != nil {
		providers = append(providers, delivery.NewSMSProvider(gw))
	}
	if len(providers) == 1 && cfg.SlackWebhookURL == "" && cfg.TeamsWebhookURL == "" {
		logger.Warn("no delivery channel configured; critical events will be recorded but not delivered")
	}
	return delivery.NewMultiProvider(providers...)
}

// channelProviders maps each channel to the provider that sends it, for the queue worker.
func channelProviders(cfg *config.Config, gw sms.Gateway) map[string]delivery.Provider {
	webhook := delivery.NewWebhookProvider(true)
	out := map[string]delivery.Provider{delivery.ChannelSlack: webhook, delivery.ChannelTeams: webhook}
	if email := emailProvider(cfg); email != nil {
		out[delivery.ChannelEmail] = email
	}
	if gw != nil {
		out[delivery.ChannelSMS] = delivery.NewSMSProvider(gw)
	}
	return out
}

func (s *Server) initEventRepository(ctx context.Context) (event.Repository, error) {
	if s.cfg.DatabaseURL == "" {
		return event.NewInMemoryRepository(), nil
	}

	sqlDB, err := db.Connect(ctx, s.cfg.DatabaseURL, db.PoolConfig{
		MaxOpenConns:    s.cfg.DBMaxOpenConns,
		MaxIdleConns:    s.cfg.DBMaxIdleConns,
		ConnMaxLifetime: s.cfg.DBConnMaxLifetime,
	})
	if err != nil {
		return nil, err
	}
	s.closers = append(s.closers, sqlDB.Close)
	s.sqlDB = sqlDB
	if err := migrate(ctx, sqlDB); err != nil {
		return nil, err
	}
	s.logger.Info("connected to PostgreSQL and ran migrations")
	return event.NewPostgresRepository(sqlDB), nil
}

// initInboxRepository keeps inbox state in PostgreSQL when the events are there, otherwise in memory.
func (s *Server) initInboxRepository() inbox.Repository {
	if s.sqlDB == nil {
		return inbox.NewMemoryRepository()
	}
	return inbox.NewPostgresRepository(s.sqlDB)
}

// initRulesStore keeps editable rules in PostgreSQL when there is a database, otherwise in memory.
func (s *Server) initRulesStore() rules.Store {
	if s.sqlDB == nil {
		return rules.NewMemoryStore()
	}
	return rules.NewPostgresStore(s.sqlDB)
}

// initPreferencesStore keeps preferences in PostgreSQL when there is a database, otherwise in memory.
func (s *Server) initPreferencesStore() preferences.Store {
	if s.sqlDB == nil {
		return preferences.NewMemoryStore()
	}
	return preferences.NewPostgresStore(s.sqlDB)
}

func migrate(ctx context.Context, sqlDB *sql.DB) error {
	return db.MigrateLocked(ctx, sqlDB)
}

// connectRedis opens the shared Redis client once, for every feature that needs it
// (deduplication and the attention budget), and closes it on shutdown.
func (s *Server) connectRedis(ctx context.Context) (*redis.Client, error) {
	if s.redisClient != nil {
		return s.redisClient, nil
	}
	opts, err := redis.ParseURL(s.cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	client := redis.NewClient(opts)
	s.closers = append(s.closers, client.Close)
	pingCtx, cancel := context.WithTimeout(ctx, redisConnectTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	s.redisClient = client
	return client, nil
}

// initBudget builds the attention budget: shared through Redis when REDIS_URL is set,
// so replicas count one budget together, otherwise per process. A cap of 0 turns it off.
func (s *Server) initBudget(ctx context.Context) (budget.Budget, error) {
	n := s.cfg.AttentionBudgetPerHour
	if n <= 0 {
		return nil, nil
	}
	if s.cfg.RedisURL == "" {
		return budget.NewMemoryBudget(n, budget.DefaultWindow), nil
	}
	client, err := s.connectRedis(ctx)
	if err != nil {
		return nil, err
	}
	return budget.NewRedisBudget(budget.NewRedisStore(client), n, budget.DefaultWindow), nil
}

// initDeduper shares the dedup window through Redis when REDIS_URL is set, so
// replicas agree on duplicates; otherwise it dedups within this process only.
func (s *Server) initDeduper(ctx context.Context) (dedup.Deduper, error) {
	if s.cfg.RedisURL == "" {
		return dedup.NewMemoryDeduper(s.cfg.DedupWindow), nil
	}
	client, err := s.connectRedis(ctx)
	if err != nil {
		return nil, err
	}
	s.logger.Info("using Redis for deduplication")
	return dedup.NewRedisDeduper(dedup.NewRedisStore(client), s.cfg.DedupWindow), nil
}

// stopStreams ends every live stream. Safe to call more than once.
func (s *Server) stopStreams() {
	s.shutdownOnce.Do(func() {
		if s.shutdown != nil {
			close(s.shutdown)
		}
	})
}

// releaseOnStartupFailure closes what NewServer opened before it gave up, logging
// any close error because the caller only sees the original failure.
func (s *Server) releaseOnStartupFailure() {
	if err := s.closeAll(); err != nil {
		s.logger.Warn("release resources after failed start-up", "error", err)
	}
}

// closeAll releases the resources opened by NewServer, joining any errors.
func (s *Server) closeAll() error {
	var errs []error
	for _, closeFn := range s.closers {
		errs = append(errs, closeFn())
	}
	s.closers = nil
	return errors.Join(errs...)
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

func (s *Server) initPersonalPreferencesStore() preferences.PersonalStore {
	if s.sqlDB == nil {
		return preferences.NewMemoryPersonalStore()
	}
	return preferences.NewPostgresPersonalStore(s.sqlDB)
}
