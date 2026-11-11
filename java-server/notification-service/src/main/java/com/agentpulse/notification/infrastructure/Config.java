package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Targets;

import java.net.URI;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Settings read from environment variables, with the same names as the Go service. Zero values fail closed:
 * with no API key the API refuses requests, and with no webhook secret the webhook refuses them, unless the
 * matching ALLOW_* switch is set.
 */
public record Config(
        int port,
        String apiKey,
        boolean allowUnauthenticated,
        String githubWebhookSecret,
        boolean allowUnsignedWebhooks,
        boolean dispatcherEnabled,
        boolean overridesAdminOnly,
        boolean attentionClassify,
        Duration attentionDigestEvery,
        int attentionBudgetPerHour,
        int learningMinSignals,
        Duration learningLookback,
        String databaseUrl,
        String redisUrl,
        String rulesPath,
        Duration dedupWindow,
        Duration eventRetention,
        long maxBodyBytes,
        String defaultNotifyEmail,
        String smtpHost,
        int smtpPort,
        String smtpUsername,
        String smtpPassword,
        String smtpSender,
        boolean smtpRequireStartTls,
        String slackWebhookUrl,
        String teamsWebhookUrl,
        List<String> allowedWebhookHosts,
        List<String> allowedEmailDomains,
        List<String> standardChannels,
        List<String> backgroundChannels,
        String authBootstrapEmail,
        String authBootstrapPassword,
        Duration authSessionTtl,
        String authCookieSecure,
        String authCookieSameSite,
        String authSignup,
        String appVersion) {

    public static final int DEFAULT_PORT = 8080;
    public static final long DEFAULT_MAX_BODY_BYTES = 1L << 20;
    public static final int DEFAULT_SMTP_PORT = 587;
    public static final Duration DEFAULT_RETENTION = Duration.ofDays(90);
    static final String DEFAULT_WEBHOOK_HOSTS = "hooks.slack.com,outlook.office.com,outlook.office365.com,*.webhook.office.com";
    private static final Pattern DURATION_PART = Pattern.compile("(\\d+)(s|m|h)");

    /** Thrown for a setting that is present but not usable. */
    public static final class InvalidConfigException extends RuntimeException {
        public InvalidConfigException(String message) {
            super(message);
        }
    }

    /** Reads the settings from a map, normally {@code System.getenv()}. */
    public static Config load(Map<String, String> env) {
        String provider = choice(env, "ATTENTION_PROVIDER", "heuristic", "heuristic", "ollama", "claude");
        if (!provider.equals("heuristic")) {
            throw new InvalidConfigException("ATTENTION_PROVIDER=" + provider
                    + " is not supported by this service yet; only the built-in heuristic is");
        }
        List<String> standard = channels(env, "NOTIFY_STANDARD_CHANNELS");
        List<String> background = channels(env, "NOTIFY_BACKGROUND_CHANNELS");
        return new Config(
                intValue(env, "PORT", DEFAULT_PORT, 1, 65535),
                text(env, "API_KEY"),
                bool(env, "ALLOW_UNAUTHENTICATED_API"),
                text(env, "GITHUB_WEBHOOK_SECRET"),
                bool(env, "ALLOW_UNSIGNED_WEBHOOKS"),
                text(env, "DISPATCHER_ENABLED").isEmpty() || bool(env, "DISPATCHER_ENABLED"),
                bool(env, "OVERRIDES_ADMIN_ONLY"),
                text(env, "ATTENTION_CLASSIFY").isEmpty() || bool(env, "ATTENTION_CLASSIFY"),
                duration(env, "ATTENTION_DIGEST_EVERY", Duration.ZERO, true),
                intValue(env, "ATTENTION_BUDGET_PER_HOUR", 10, 0, 100000),
                intValue(env, "LEARNING_MIN_SIGNALS", 5, 1, 10000),
                duration(env, "LEARNING_LOOKBACK", Duration.ofDays(30), false),
                text(env, "DATABASE_URL"),
                text(env, "REDIS_URL"),
                text(env, "RULES_PATH"),
                duration(env, "DEDUP_WINDOW", Duration.ZERO, false),
                duration(env, "EVENT_RETENTION", DEFAULT_RETENTION, true),
                intValue(env, "MAX_BODY_BYTES", (int) DEFAULT_MAX_BODY_BYTES, 1, 1 << 30),
                text(env, "DEFAULT_NOTIFY_EMAIL"),
                text(env, "SMTP_HOST"),
                intValue(env, "SMTP_PORT", DEFAULT_SMTP_PORT, 1, 65535),
                text(env, "SMTP_USERNAME"),
                text(env, "SMTP_PASSWORD"),
                text(env, "SMTP_SENDER"),
                bool(env, "SMTP_REQUIRE_STARTTLS"),
                text(env, "SLACK_WEBHOOK_URL"),
                text(env, "TEAMS_WEBHOOK_URL"),
                list(env, "NOTIFY_ALLOWED_WEBHOOK_HOSTS", DEFAULT_WEBHOOK_HOSTS),
                list(env, "NOTIFY_ALLOWED_EMAIL_DOMAINS", ""),
                standard,
                background,
                text(env, "AUTH_BOOTSTRAP_EMAIL"),
                env.getOrDefault("AUTH_BOOTSTRAP_PASSWORD", ""),
                duration(env, "AUTH_SESSION_TTL", Duration.ofHours(12), false),
                choice(env, "AUTH_COOKIE_SECURE", "auto", "auto", "true", "false"),
                choice(env, "AUTH_COOKIE_SAMESITE", "lax", "lax", "strict", "none"),
                choice(env, "AUTH_SIGNUP", "off", "off", "approval"),
                text(env, "APP_VERSION").isEmpty() ? "dev" : text(env, "APP_VERSION"));
    }

    /** Recipients of critical email: DEFAULT_NOTIFY_EMAIL may hold several addresses separated by commas. */
    public List<String> emailRecipients() {
        return split(defaultNotifyEmail);
    }

    /** Whether SMTP is configured well enough to send. */
    public boolean emailConfigured() {
        return !smtpHost.isEmpty() && !smtpSender.isEmpty();
    }

    /** Where critical events (and routed standard and background events) are delivered. */
    public Targets targets() {
        return new Targets(emailConfigured() ? emailRecipients() : List.of(),
                slackWebhookUrl.isEmpty() ? List.of() : List.of(slackWebhookUrl),
                teamsWebhookUrl.isEmpty() ? List.of() : List.of(teamsWebhookUrl));
    }

    /** The hosts outbound webhooks may target: the allow-list plus the hosts of the operator's own webhook URLs. */
    public List<String> webhookHostAllowList() {
        List<String> hosts = new ArrayList<>(allowedWebhookHosts);
        for (String raw : List.of(slackWebhookUrl, teamsWebhookUrl)) {
            if (!raw.isEmpty()) {
                try {
                    String host = URI.create(raw).getHost();
                    if (host != null) {
                        hosts.add(host);
                    }
                } catch (IllegalArgumentException ignored) {
                    // an unusable operator URL simply adds no host
                }
            }
        }
        return hosts;
    }

    /** The limits applied to what a /notify caller may ask the service to contact. */
    public Notify.Limits notifyLimits() {
        return new Notify.Limits(webhookHostAllowList(), allowedEmailDomains);
    }

    /** The first of the given words, lower-cased; a setting must be one of them. */
    private static String choice(Map<String, String> env, String name, String fallback, String... allowed) {
        String v = text(env, name).toLowerCase(java.util.Locale.ROOT);
        if (v.isEmpty()) {
            return fallback;
        }
        for (String a : allowed) {
            if (a.equals(v)) {
                return v;
            }
        }
        throw new InvalidConfigException(name + " must be one of " + String.join(", ", allowed));
    }

    /** Whether the bootstrap pair is usable: both set or both empty. */
    public boolean bootstrapConfigured() {
        return !authBootstrapEmail.isEmpty();
    }

    private static String text(Map<String, String> env, String name) {
        String v = env.get(name);
        return v == null ? "" : v.trim();
    }

    private static List<String> split(String raw) {
        return Arrays.stream(raw.split(",")).map(String::trim).filter(s -> !s.isEmpty()).toList();
    }

    private static List<String> list(Map<String, String> env, String name, String fallback) {
        String v = text(env, name);
        return split(v.isEmpty() ? fallback : v);
    }

    private static List<String> channels(Map<String, String> env, String name) {
        try {
            return Targets.parseChannels(list(env, name, ""));
        } catch (IllegalArgumentException e) {
            throw new InvalidConfigException(name + ": " + e.getMessage());
        }
    }

    private static boolean bool(Map<String, String> env, String name) {
        String v = text(env, name);
        if (v.isEmpty()) {
            return false;
        }
        if (v.equalsIgnoreCase("true")) {
            return true;
        }
        if (v.equalsIgnoreCase("false")) {
            return false;
        }
        throw new InvalidConfigException(name + " must be true or false");
    }

    private static int intValue(Map<String, String> env, String name, int fallback, int min, int max) {
        String v = text(env, name);
        if (v.isEmpty()) {
            return fallback;
        }
        try {
            int n = Integer.parseInt(v);
            if (n >= min && n <= max) {
                return n;
            }
        } catch (NumberFormatException ignored) {
            // reported below
        }
        throw new InvalidConfigException(name + " must be a whole number from " + min + " to " + max);
    }

    private static Duration duration(Map<String, String> env, String name, Duration fallback, boolean allowZero) {
        String v = text(env, name);
        if (v.isEmpty()) {
            return fallback;
        }
        try {
            Duration d = parseGoDuration(v);
            if (d.isNegative() || (d.isZero() && !allowZero)) {
                throw new IllegalArgumentException("not positive");
            }
            return d;
        } catch (RuntimeException e) {
            throw new InvalidConfigException(name + " must be a duration such as 10m, 30s or 2160h");
        }
    }

    /** Accepts the Go forms the shared settings use: 90s, 10m, 2h and combinations such as 1h30m. */
    static Duration parseGoDuration(String v) {
        Matcher m = DURATION_PART.matcher(v);
        Duration total = Duration.ZERO;
        int end = 0;
        while (m.find()) {
            if (m.start() != end) {
                throw new IllegalArgumentException("unexpected text");
            }
            long n = Long.parseLong(m.group(1));
            total = total.plus(switch (m.group(2)) {
                case "s" -> Duration.ofSeconds(n);
                case "m" -> Duration.ofMinutes(n);
                default -> Duration.ofHours(n);
            });
            end = m.end();
        }
        if (end == 0 || end != v.length()) {
            throw new IllegalArgumentException("unexpected text");
        }
        return total;
    }
}
