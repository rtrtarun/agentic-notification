package com.example.notification.infrastructure;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;
import java.util.Map;

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
        String databaseUrl,
        String rulesPath,
        Duration dedupWindow,
        long maxBodyBytes,
        String defaultNotifyEmail,
        String smtpHost,
        int smtpPort,
        String smtpUsername,
        String smtpPassword,
        String smtpSender,
        boolean smtpRequireStartTls,
        String slackWebhookUrl) {

    public static final int DEFAULT_PORT = 8080;
    public static final long DEFAULT_MAX_BODY_BYTES = 1L << 20;
    public static final int DEFAULT_SMTP_PORT = 587;

    /** Thrown for a setting that is present but not usable. */
    public static final class InvalidConfigException extends RuntimeException {
        public InvalidConfigException(String message) {
            super(message);
        }
    }

    /** Reads the settings from a map, normally {@code System.getenv()}. */
    public static Config load(Map<String, String> env) {
        return new Config(
                intValue(env, "PORT", DEFAULT_PORT, 1, 65535),
                text(env, "API_KEY"),
                bool(env, "ALLOW_UNAUTHENTICATED_API"),
                text(env, "GITHUB_WEBHOOK_SECRET"),
                bool(env, "ALLOW_UNSIGNED_WEBHOOKS"),
                text(env, "DATABASE_URL"),
                text(env, "RULES_PATH"),
                duration(env, "DEDUP_WINDOW"),
                intValue(env, "MAX_BODY_BYTES", (int) DEFAULT_MAX_BODY_BYTES, 1, 1 << 30),
                text(env, "DEFAULT_NOTIFY_EMAIL"),
                text(env, "SMTP_HOST"),
                intValue(env, "SMTP_PORT", DEFAULT_SMTP_PORT, 1, 65535),
                text(env, "SMTP_USERNAME"),
                text(env, "SMTP_PASSWORD"),
                text(env, "SMTP_SENDER"),
                bool(env, "SMTP_REQUIRE_STARTTLS"),
                text(env, "SLACK_WEBHOOK_URL"));
    }

    /** Recipients of critical email: DEFAULT_NOTIFY_EMAIL may hold several addresses separated by commas. */
    public List<String> emailRecipients() {
        return Arrays.stream(defaultNotifyEmail.split(","))
                .map(String::trim).filter(s -> !s.isEmpty()).toList();
    }

    private static String text(Map<String, String> env, String name) {
        String v = env.get(name);
        return v == null ? "" : v.trim();
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

    private static Duration duration(Map<String, String> env, String name) {
        String v = text(env, name);
        if (v.isEmpty()) {
            return Duration.ZERO;
        }
        try {
            return parseGoDuration(v);
        } catch (RuntimeException e) {
            throw new InvalidConfigException(name + " must be a duration such as 10m or 30s");
        }
    }

    /** Accepts the Go forms the shared settings use: 90s, 10m, 2h. */
    static Duration parseGoDuration(String v) {
        long n = Long.parseLong(v.substring(0, v.length() - 1));
        if (n <= 0) {
            throw new IllegalArgumentException("not positive");
        }
        return switch (v.charAt(v.length() - 1)) {
            case 's' -> Duration.ofSeconds(n);
            case 'm' -> Duration.ofMinutes(n);
            case 'h' -> Duration.ofHours(n);
            default -> throw new IllegalArgumentException("unit");
        };
    }
}
