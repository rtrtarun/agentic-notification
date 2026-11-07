package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Ports.PortException;
import com.zaxxer.hikari.HikariConfig;
import com.zaxxer.hikari.HikariDataSource;

import java.net.URI;
import java.net.URISyntaxException;
import java.sql.Connection;
import java.sql.SQLException;
import java.sql.Statement;
import java.time.Duration;

/**
 * Connects to the PostgreSQL database whose schema the Go service owns and migrates. This service never creates
 * or alters tables: two migrators on one schema would drift. It only checks that the tables it reads exist.
 */
public final class Database {
    private static final String EVENT_COLUMNS = "id, source, event_type, repository, attention_class, "
            + "decision_status, decision_reason, decision_model_version, decision_confidence, decision_matched_rule, "
            + "payload, dedup_key, thread_id, merged_into, created_at";

    private Database() {}

    /** Turns {@code postgres://user:pass@host:5432/db?sslmode=disable} into a pooled data source. */
    public static HikariDataSource connect(String databaseUrl) {
        HikariConfig config = new HikariConfig();
        URI uri;
        try {
            uri = new URI(databaseUrl);
        } catch (URISyntaxException e) {
            throw new PortException("DATABASE_URL is not a valid URL", e);
        }
        if (uri.getHost() == null) {
            throw new PortException("DATABASE_URL has no host", null);
        }
        config.setJdbcUrl(jdbcUrl(uri));
        if (uri.getUserInfo() != null) {
            String[] userInfo = uri.getUserInfo().split(":", 2);
            config.setUsername(userInfo[0]);
            if (userInfo.length > 1) {
                config.setPassword(userInfo[1]);
            }
        }
        config.setMaximumPoolSize(10);
        config.setConnectionTimeout(Duration.ofSeconds(5).toMillis());
        config.setInitializationFailTimeout(-1); // start the pool lazily; waitForSchema reports the real error
        return new HikariDataSource(config);
    }

    static String jdbcUrl(URI uri) {
        int port = uri.getPort() > 0 ? uri.getPort() : 5432;
        String path = uri.getPath() == null ? "" : uri.getPath();
        String query = uri.getRawQuery() == null ? "" : "?" + uri.getRawQuery();
        return "jdbc:postgresql://" + uri.getHost() + ":" + port + path + query;
    }

    /**
     * Waits until the Go service has created the schema, so the two can be started together. Throws when the
     * schema is still missing after {@code timeout}.
     */
    public static void waitForSchema(HikariDataSource source, Duration timeout) {
        long deadline = System.nanoTime() + timeout.toNanos();
        SQLException last;
        while (true) {
            try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
                s.execute("SELECT " + EVENT_COLUMNS + " FROM events LIMIT 0");
                return;
            } catch (SQLException e) {
                last = e;
            }
            if (System.nanoTime() >= deadline) {
                throw new PortException("the events table is not ready (the Go service creates the schema; start it first): "
                        + last.getMessage(), last);
            }
            try {
                Thread.sleep(1000);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                throw new PortException("interrupted while waiting for the schema", e);
            }
        }
    }
}
