package com.agentpulse.notification;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Dedup;
import com.agentpulse.notification.application.Feedback;
import com.agentpulse.notification.application.Overrides;
import com.agentpulse.notification.application.Dispatcher;
import com.agentpulse.notification.application.MemoryDeduper;
import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.DeliveryStore;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.RuleStore;
import com.agentpulse.notification.application.QueueOutbox;
import com.agentpulse.notification.application.RulesLoader;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.application.Targets;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.Database;
import com.agentpulse.notification.infrastructure.FeedbackStores;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import com.agentpulse.notification.infrastructure.JedisKeyStore;
import com.agentpulse.notification.infrastructure.MemoryAccountStore;
import com.agentpulse.notification.infrastructure.MemoryStores;
import com.agentpulse.notification.infrastructure.PostgresAccountStore;
import com.agentpulse.notification.infrastructure.PostgresDeliveryStore;
import com.agentpulse.notification.infrastructure.PostgresEventRepository;
import com.agentpulse.notification.infrastructure.PostgresRuleStore;
import com.agentpulse.notification.infrastructure.Providers;
import com.agentpulse.notification.infrastructure.RedisDeduper;
import com.agentpulse.notification.interfaces.HttpApi;
import com.sun.net.httpserver.HttpServer;
import com.zaxxer.hikari.HikariDataSource;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.nio.file.Path;
import java.time.Clock;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/** Wires the service together. This is the only place that constructs concrete implementations. */
public final class Main {
    private static final Logger LOG = LoggerFactory.getLogger(Main.class);
    private static final Duration SCHEMA_WAIT = Duration.ofSeconds(60);
    /** How many recent events feedback looks through to find the one it is about, as in the Go service. */
    private static final int FEEDBACK_LOOKUP_WINDOW = 1000;
    private static final int SHUTDOWN_SECONDS = 5;

    private Main() {}

    public static void main(String[] args) throws Exception {
        Config config = Config.load(System.getenv());
        Clock clock = Clock.systemUTC();

        List<Rule> seed = config.rulesPath().isEmpty() ? defaultRules() : RulesLoader.loadFile(Path.of(config.rulesPath()));

        HikariDataSource database = null;
        EventRepository repository;
        RuleStore ruleStore;
        DeliveryStore deliveryStore;
        Accounts.Store accountStore;
        Overrides.Store overrideStore;
        Feedback.Store feedbackStore;
        if (config.databaseUrl().isEmpty()) {
            LOG.warn("DATABASE_URL unset; events, rules and deliveries are kept in memory and lost on restart");
            repository = new InMemoryEventRepository(clock);
            ruleStore = new MemoryStores.Rules();
            deliveryStore = new MemoryStores.Deliveries();
            accountStore = new MemoryAccountStore();
            overrideStore = new FeedbackStores.MemoryOverrides();
            feedbackStore = new FeedbackStores.MemoryFeedback();
        } else {
            database = Database.connect(config.databaseUrl());
            Database.waitForSchema(database, SCHEMA_WAIT);
            repository = new PostgresEventRepository(database);
            ruleStore = new PostgresRuleStore(database);
            deliveryStore = new PostgresDeliveryStore(database);
            accountStore = new PostgresAccountStore(database);
            overrideStore = new FeedbackStores.PostgresOverrides(database);
            feedbackStore = new FeedbackStores.PostgresFeedback(database);
        }
        Accounts.Service accounts = new Accounts.Service(accountStore, config.authSessionTtl(), clock)
                .withSignup("approval".equals(config.authSignup()));
        if (config.bootstrapConfigured()) {
            if (accounts.bootstrap(config.authBootstrapEmail(), config.authBootstrapPassword())) {
                LOG.info("created the first admin account from AUTH_BOOTSTRAP_EMAIL");
            }
        }

        PolicyEngine engine = new PolicyEngine(List.of());
        RulesService rules = new RulesService(ruleStore, engine, seed);

        JedisKeyStore redis = null;
        Dedup.Deduper deduper;
        if (config.redisUrl().isEmpty()) {
            deduper = new MemoryDeduper(config.dedupWindow(), clock);
        } else {
            redis = JedisKeyStore.connect(config.redisUrl());
            LOG.info("using Redis for deduplication");
            deduper = new RedisDeduper(redis, config.dedupWindow());
        }

        Targets targets = config.targets();
        NotificationProvider webhook = Providers.webhook(true);
        NotificationProvider email = config.emailConfigured() ? Providers.email(new Providers.Smtp(config.smtpHost(),
                config.smtpPort(), config.smtpUsername(), config.smtpPassword(), config.smtpSender(),
                config.smtpRequireStartTls())) : null;
        List<NotificationProvider> all = new ArrayList<>(List.of(webhook));
        Map<String, NotificationProvider> byChannel = new HashMap<>(Map.of(Targets.SLACK, webhook, Targets.TEAMS, webhook));
        if (email != null) {
            all.add(email);
            byChannel.put(Targets.EMAIL, email);
        }
        NotificationProvider provider = Providers.all(all);
        if (targets.channels().isEmpty()) {
            LOG.warn("no delivery channel configured; critical events will be recorded but not delivered");
        }

        Map<String, List<String>> routes = Map.of("standard", config.standardChannels(),
                "background", config.backgroundChannels());
        QueueOutbox outbox = new QueueOutbox(deliveryStore, targets, routes, clock);
        Overrides.Service overrides = new Overrides.Service(overrideStore, clock);
        EventRepository eventsForLookup = repository;
        Feedback.Service feedback = new Feedback.Service(feedbackStore,
                id -> eventsForLookup.listRecent(FEEDBACK_LOOKUP_WINDOW).stream().filter(e -> e.getId().equals(id)).findFirst(),
                overrides, clock);
        Pipeline pipeline = new Pipeline(engine, provider, repository)
                .withDeduper(deduper)
                .withOverrides(overrides)
                .withOutbox(outbox)
                .withCriticalTargets(targets);
        Dispatcher dispatcher = new Dispatcher(deliveryStore, targets, byChannel, clock,
                Dispatcher.DEFAULT_MAX_ATTEMPTS, Dispatcher.DEFAULT_BACKOFF_BASE);

        if (config.githubWebhookSecret().isEmpty() && config.allowUnsignedWebhooks()) {
            LOG.warn("GITHUB_WEBHOOK_SECRET unset; accepting unsigned webhooks (ALLOW_UNSIGNED_WEBHOOKS=true)");
        }
        if (config.apiKey().isEmpty() && config.allowUnauthenticated()) {
            LOG.warn("API_KEY unset; serving the API without authentication (ALLOW_UNAUTHENTICATED_API=true)");
        }

        Notify.Service notify = new Notify.Service(config.notifyLimits(), provider);
        HttpServer server = new HttpApi(config, pipeline, repository, notify, rules, deliveryStore, clock, accounts, overrides, feedback)
                .start(config.port());
        if (config.dispatcherEnabled()) {
            dispatcher.start();
        } else {
            LOG.warn("DISPATCHER_ENABLED=false; this process will not claim queued deliveries");
        }
        LOG.info("notification service listening port={} rules={}", config.port(), rules.list().size());

        HikariDataSource db = database;
        JedisKeyStore cache = redis;
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            dispatcher.stop();
            server.stop(SHUTDOWN_SECONDS);
            if (cache != null) {
                cache.close();
            }
            if (db != null) {
                db.close();
            }
        }));
    }

    /** The rules used when RULES_PATH is unset; the same two as the Go service's built-in defaults. */
    private static List<Rule> defaultRules() {
        return List.of(
                new Rule("builtin-security-advisory", "github-security-advisory", "",
                        new Rule.Condition("github", "security_advisory", ""),
                        new Rule.Outcome("critical", "security vulnerability detected", false), false, true),
                new Rule("builtin-default-github", "default-github", "",
                        new Rule.Condition("github", "", ""),
                        new Rule.Outcome("standard", "default routing", true), false, false));
    }
}
