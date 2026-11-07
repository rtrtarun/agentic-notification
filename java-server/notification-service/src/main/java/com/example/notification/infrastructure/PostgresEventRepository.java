package com.example.notification.infrastructure;

import com.example.notification.application.Ports.EventRepository;
import com.example.notification.application.Ports.PortException;
import com.example.notification.domain.DecisionState;
import com.example.notification.domain.Event;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;

import javax.sql.DataSource;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Types;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/** Stores events in the {@code events} table the Go service migrates. All SQL for events lives here. */
public final class PostgresEventRepository implements EventRepository {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final TypeReference<Map<String, Object>> MAP = new TypeReference<>() {};

    private static final String COLUMNS = "id, source, event_type, repository, attention_class, decision_status, "
            + "decision_reason, decision_model_version, decision_confidence, decision_matched_rule, payload, "
            + "dedup_key, thread_id, merged_into, created_at";

    private final DataSource dataSource;

    public PostgresEventRepository(DataSource dataSource) {
        this.dataSource = dataSource;
    }

    @Override
    public void save(Event event) {
        String payload;
        try {
            payload = JSON.writeValueAsString(event.getPayload());
        } catch (JsonProcessingException e) {
            throw new PortException("marshal payload", e);
        }
        DecisionState d = event.getDecisionState();
        String sql = "INSERT INTO events (id, source, event_type, repository, attention_class, decision_status, "
                + "decision_reason, decision_model_version, decision_confidence, decision_matched_rule, payload, "
                + "dedup_key, thread_id, merged_into, attention_state) "
                + "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?, ?, 'done') ON CONFLICT (id) DO NOTHING";
        try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            ps.setString(1, event.getId());
            ps.setString(2, event.getSource());
            ps.setString(3, event.getEventType());
            ps.setString(4, event.getRepository());
            ps.setString(5, event.getAttentionClass().isEmpty() ? "standard" : event.getAttentionClass());
            setNullable(ps, 6, d == null ? "" : d.getStatus());
            setNullable(ps, 7, d == null ? "" : d.getReason());
            setNullable(ps, 8, d == null ? "" : d.getModelVersion());
            if (d != null && d.getConfidence() > 0) {
                ps.setDouble(9, d.getConfidence());
            } else {
                ps.setNull(9, Types.DOUBLE);
            }
            setNullable(ps, 10, d == null ? "" : d.getMatchedRule());
            ps.setString(11, payload);
            setNullable(ps, 12, event.getDedupKey());
            setNullable(ps, 13, event.getThreadId());
            setNullable(ps, 14, event.getMergedInto());
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new PortException("save event", e);
        }
    }

    @Override
    public boolean exists(String id) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("SELECT 1 FROM events WHERE id = ?")) {
            ps.setString(1, id);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next();
            }
        } catch (SQLException e) {
            throw new PortException("check event exists", e);
        }
    }

    @Override
    public List<Event> listRecent(int limit) {
        String sql = "SELECT " + COLUMNS + " FROM events ORDER BY created_at DESC, id DESC LIMIT ?";
        try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            ps.setInt(1, limit);
            try (ResultSet rs = ps.executeQuery()) {
                List<Event> events = new ArrayList<>();
                while (rs.next()) {
                    events.add(read(rs));
                }
                return events;
            }
        } catch (SQLException e) {
            throw new PortException("list events", e);
        }
    }

    private static Event read(ResultSet rs) throws SQLException {
        Map<String, Object> payload;
        try {
            payload = JSON.readValue(rs.getString("payload"), MAP);
        } catch (JsonProcessingException e) {
            throw new PortException("unmarshal payload", e);
        }
        Event event = new Event(rs.getString("id"), rs.getString("source"), rs.getString("event_type"),
                rs.getString("repository"), payload);
        event.setAttentionClass(rs.getString("attention_class"));
        event.setDedupKey(rs.getString("dedup_key"));
        event.setThreadId(rs.getString("thread_id"));
        event.setMergedInto(rs.getString("merged_into"));
        event.setCreatedAt(rs.getTimestamp("created_at").toInstant());

        String status = rs.getString("decision_status");
        String reason = rs.getString("decision_reason");
        if (status != null || reason != null) {
            DecisionState d = new DecisionState(reason, rs.getString("decision_matched_rule"));
            d.setStatus(status);
            d.setModelVersion(rs.getString("decision_model_version"));
            d.setConfidence(rs.getDouble("decision_confidence"));
            event.setDecisionState(d);
        }
        return event;
    }

    private static void setNullable(PreparedStatement ps, int index, String value) throws SQLException {
        if (value == null || value.isEmpty()) {
            ps.setNull(index, Types.VARCHAR);
        } else {
            ps.setString(index, value);
        }
    }
}
