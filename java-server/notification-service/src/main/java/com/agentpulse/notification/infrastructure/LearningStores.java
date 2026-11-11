package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Learning;
import com.agentpulse.notification.application.Ports.PortException;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;

import javax.sql.DataSource;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * Stores for learning suggestions: in memory (no database, and tests) and PostgreSQL, as JSON in the
 * {@code learning_suggestions} table the Go service migrates and also uses. The JSON is the Go service's, so either
 * service reads the other's suggestions.
 */
public final class LearningStores {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final TypeReference<Map<String, Object>> MAP = new TypeReference<>() {};
    private static final int LIST_LIMIT = 500;

    private LearningStores() {}

    private static final Comparator<Learning.Suggestion> NEWEST_FIRST =
            Comparator.comparing(Learning.Suggestion::updatedAt).reversed().thenComparing(Learning.Suggestion::id);

    /** Suggestions in memory. */
    public static final class Memory implements Learning.Repository {
        private final Map<String, Learning.Suggestion> list = new HashMap<>();

        @Override
        public synchronized void save(Learning.Suggestion s) {
            list.put(s.id(), s);
        }

        @Override
        public synchronized Learning.Suggestion get(String id) {
            Learning.Suggestion s = list.get(id);
            if (s == null) {
                throw new Learning.NotFoundException();
            }
            return s;
        }

        @Override
        public synchronized List<Learning.Suggestion> list(String status) {
            List<Learning.Suggestion> out = new ArrayList<>();
            for (Learning.Suggestion s : list.values()) {
                if (status.isEmpty() || s.status().equals(status)) {
                    out.add(s);
                }
            }
            out.sort(NEWEST_FIRST);
            return out;
        }
    }

    /** Suggestions in PostgreSQL. */
    public static final class Postgres implements Learning.Repository {
        private final DataSource dataSource;

        public Postgres(DataSource dataSource) {
            this.dataSource = dataSource;
        }

        @Override
        public void save(Learning.Suggestion s) {
            String sql = "INSERT INTO learning_suggestions (id, status, body, created_at, updated_at) VALUES (?,?,?::jsonb,?,?) "
                    + "ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, body = EXCLUDED.body, updated_at = EXCLUDED.updated_at";
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
                ps.setString(1, s.id());
                ps.setString(2, s.status());
                ps.setString(3, JSON.writeValueAsString(Learning.toMap(s)));
                ps.setObject(4, OffsetDateTime.ofInstant(s.createdAt(), ZoneOffset.UTC));
                ps.setObject(5, OffsetDateTime.ofInstant(s.updatedAt(), ZoneOffset.UTC));
                ps.executeUpdate();
            } catch (SQLException | JsonProcessingException e) {
                throw new PortException("save suggestion: " + e.getMessage(), e);
            }
        }

        @Override
        public Learning.Suggestion get(String id) {
            try (Connection c = dataSource.getConnection();
                 PreparedStatement ps = c.prepareStatement("SELECT body FROM learning_suggestions WHERE id = ?")) {
                ps.setString(1, id);
                try (ResultSet rs = ps.executeQuery()) {
                    if (!rs.next()) {
                        throw new Learning.NotFoundException();
                    }
                    return Learning.fromMap(JSON.readValue(rs.getString(1), MAP));
                }
            } catch (SQLException | JsonProcessingException e) {
                throw new PortException("get suggestion: " + e.getMessage(), e);
            }
        }

        @Override
        public List<Learning.Suggestion> list(String status) {
            String sql = "SELECT body FROM learning_suggestions WHERE (? = '' OR status = ?) ORDER BY updated_at DESC, id ASC LIMIT " + LIST_LIMIT;
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
                ps.setString(1, status);
                ps.setString(2, status);
                try (ResultSet rs = ps.executeQuery()) {
                    List<Learning.Suggestion> out = new ArrayList<>();
                    while (rs.next()) {
                        out.add(Learning.fromMap(JSON.readValue(rs.getString(1), MAP)));
                    }
                    return out;
                }
            } catch (SQLException | JsonProcessingException e) {
                throw new PortException("list suggestions: " + e.getMessage(), e);
            }
        }
    }
}
