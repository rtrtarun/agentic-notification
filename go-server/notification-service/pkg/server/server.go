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

	"github.com/example/notification-service/pkg/channels"
	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/db"
	"github.com/example/notification-service/pkg/dedup"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/feedback"
	"github.com/example/notification-service/pkg/inbox"
	"github.com/example/notification-service/pkg/insights"
	"github.com/example/notification-service/pkg/learning"
	"github.com/example/notification-service/pkg/metrics"
	"github.com/example/notification-service/pkg/notification"
	"github.com/example/notification-service/pkg/notification/impl"
	"github.com/example/notification-service/pkg/pipeline"
	"github.com/example/notification-service/pkg/policy"
	"github.com/example/notification-service/pkg/preferences"
	"github.com/example/notification-service/pkg/rules"
	"github.com/example/notification-service/pkg/stream"
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
	eventRepo       event.Repository
	sqlDB           *sql.DB
	inbox           *inbox.Service
	insights        *insights.Service
	rules           *rules.Service
	channelTester   *channels.Tester
	preferences     *preferences.Service
	broker          *stream.Broker
	// shutdown is closed when the server stops, so live streams end instead of delaying shutdown.
	shutdown         chan struct{}
	shutdownOnce     sync.Once
	feedbackRecorder feedback.Recorder
	learningService  *learning.Service
	policyEngine     *policy.Engine
	eventPipeline    *pipeline.Pipeline
	metrics          *metrics.Registry
	purger           event.Purger
	http             *http.Server
	closers          []func() error
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

	provider := newProvider(cfg, logger)
	policyEngine := policy.NewEngine(seedRules)
	deliverySvc := delivery.NewService(provider)

	ruleService, err := rules.NewService(ctx, s.initRulesStore(), policyEngine, seedRules)
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("init rules: %w", err)
	}

	s.eventRepo = eventRepo
	s.rules = ruleService
	s.channelTester = channels.NewTester(deliverySvc, s.channelSettings(), channelTestGap)
	prefService, err := preferences.NewService(ctx, s.initPreferencesStore())
	if err != nil {
		s.releaseOnStartupFailure()
		return nil, fmt.Errorf("init preferences: %w", err)
	}
	s.preferences = prefService
	s.inbox = inbox.NewService(eventRepo, s.initInboxRepository())
	s.insights = insights.NewService(s.inbox)
	s.deliveryService = deliverySvc
	s.policyEngine = policyEngine
	s.feedbackRecorder = feedback.NewInMemoryRecorder()
	s.learningService = learning.NewService("")
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
		},
	).WithDeduper(deduper).WithLogger(logger).WithPreferences(s.preferences).WithPublisher(s.broker)
	if purger, ok := eventRepo.(event.Purger); ok {
		s.purger = purger
	}
	return s, nil
}

// newProvider builds the delivery provider: email when SMTP is configured, plus
// webhooks, which refuse private addresses so a webhook URL cannot reach internal hosts.
func newProvider(cfg *config.Config, logger *slog.Logger) delivery.Provider {
	providers := []delivery.Provider{delivery.NewWebhookProvider(true)}
	if cfg.SMTPHost != "" && cfg.SMTPSender != "" {
		providers = append(providers, delivery.NewSMTPProvider(cfg))
	}
	if len(providers) == 1 && cfg.SlackWebhookURL == "" && cfg.TeamsWebhookURL == "" {
		logger.Warn("no delivery channel configured; critical events will be recorded but not delivered")
	}
	return delivery.NewMultiProvider(providers...)
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

// initDeduper shares the dedup window through Redis when REDIS_URL is set, so
// replicas agree on duplicates; otherwise it dedups within this process only.
func (s *Server) initDeduper(ctx context.Context) (dedup.Deduper, error) {
	if s.cfg.RedisURL == "" {
		return dedup.NewMemoryDeduper(s.cfg.DedupWindow), nil
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
