package com.agentpulse.notification;

import com.agentpulse.notification.application.MemoryDeduper;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.RulesLoader;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.Database;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import com.agentpulse.notification.infrastructure.PostgresEventRepository;
import com.agentpulse.notification.infrastructure.Providers;
import com.agentpulse.notification.interfaces.HttpApi;
import com.sun.net.httpserver.HttpServer;
import com.zaxxer.hikari.HikariDataSource;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.nio.file.Path;
import java.time.Clock;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;

/** Wires the service together. This is the only place that constructs concrete implementations. */
public final class Main {
    private static final Logger LOG = LoggerFactory.getLogger(Main.class);
    private static final Duration SCHEMA_WAIT = Duration.ofSeconds(60);
    private static final int SHUTDOWN_SECONDS = 5;

    private Main() {}

    public static void main(String[] args) throws Exception {
        Config config = Config.load(System.getenv());

        List<Rule> rules = config.rulesPath().isEmpty() ? defaultRules() : RulesLoader.loadFile(Path.of(config.rulesPath()));
        PolicyEngine engine = new PolicyEngine(rules);

        HikariDataSource database = null;
        EventRepository repository;
        if (config.databaseUrl().isEmpty()) {
            LOG.warn("DATABASE_URL unset; events are kept in memory and lost on restart");
            repository = new InMemoryEventRepository(Clock.systemUTC());
        } else {
            database = Database.connect(config.databaseUrl());
            Database.waitForSchema(database, SCHEMA_WAIT);
            repository = new PostgresEventRepository(database);
        }

        Pipeline pipeline = new Pipeline(engine, criticalProvider(config), repository)
                .withDeduper(new MemoryDeduper(config.dedupWindow(), Clock.systemUTC()));

        if (config.githubWebhookSecret().isEmpty() && config.allowUnsignedWebhooks()) {
            LOG.warn("GITHUB_WEBHOOK_SECRET unset; accepting unsigned webhooks (ALLOW_UNSIGNED_WEBHOOKS=true)");
        }
        if (config.apiKey().isEmpty() && config.allowUnauthenticated()) {
            LOG.warn("API_KEY unset; serving the API without authentication (ALLOW_UNAUTHENTICATED_API=true)");
        }

        HttpServer server = new HttpApi(config, pipeline, repository).start(config.port());
        LOG.info("notification service listening port={} rules={}", config.port(), rules.size());

        HikariDataSource toClose = database;
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            server.stop(SHUTDOWN_SECONDS);
            if (toClose != null) {
                toClose.close();
            }
        }));
    }

    /** Critical events go to every configured channel; with none configured they are only logged. */
    private static NotificationProvider criticalProvider(Config config) {
        List<NotificationProvider> providers = new ArrayList<>();
        if (!config.smtpHost().isEmpty() && !config.emailRecipients().isEmpty()) {
            providers.add(Providers.email(new Providers.Smtp(config.smtpHost(), config.smtpPort(), config.smtpUsername(),
                    config.smtpPassword(), config.smtpSender(), config.emailRecipients(), config.smtpRequireStartTls())));
        }
        if (!config.slackWebhookUrl().isEmpty()) {
            providers.add(Providers.slack(config.slackWebhookUrl()));
        }
        return providers.isEmpty() ? Providers.log() : Providers.all(providers);
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
