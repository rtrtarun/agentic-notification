package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.application.Tuning;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import javax.sql.DataSource;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Timestamp;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Stores for score adjustments and the log of learning runs: in memory (no database, and tests) and PostgreSQL,
 * in the {@code learning_adjustments} and {@code learning_runs} tables the Go service migrates and also uses, so
 * either service reads what the other proposed and logged.
 */
public final class TuningStores {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final int LIST_LIMIT = 500;
    private static final String COLUMNS = "id, source, event_type, repository, delta, status, evidence, impact, version, "
            + "created_at, updated_at, resolved_by, resolved_at";

    private TuningStores() {}

    private static final Comparator<Tuning.Adjustment> NEWEST_FIRST =
            Comparator.comparing(Tuning.Adjustment::updatedAt).reversed().thenComparing(Tuning.Adjustment::id);

    /** Adjustments in memory. */
    public static final class MemoryAdjustments implements Tuning.AdjustmentRepository {
        private final Map<String, Tuning.Adjustment> list = new HashMap<>();

        @Override
        public synchronized void save(Tuning.Adjustment a) {
            list.put(a.id(), a);
        }

        @Override
        public synchronized Tuning.Adjustment get(String id) {
            Tuning.Adjustment a = list.get(id);
            if (a == null) {
                throw new Tuning.NotFoundException();
            }
            return a;
        }

        @Override
        public synchronized List<Tuning.Adjustment> list(String status) {
            List<Tuning.Adjustment> out = new ArrayList<>();
            for (Tuning.Adjustment a : list.values()) {
                if (status.isEmpty() || a.status().equals(status)) {
                    out.add(a);
                }
            }
            out.sort(NEWEST_FIRST);
            return out;
        }

        @Override
        public synchronized int maxVersion() {
            return list.values().stream().mapToInt(Tuning.Adjustment::version).max().orElse(0);
        }
    }

    /** The run log in memory. */
    public static final class MemoryRuns implements Tuning.RunRepository {
        private final List<Tuning.Run> list = new ArrayList<>(); // oldest first

        @Override
        public synchronized void start(Tuning.Run run, Instant staleBefore) {
            for (int i = 0; i < list.size(); i++) {
                Tuning.Run r = list.get(i);
                if (!Tuning.RUN_RUNNING.equals(r.status())) {
                    continue;
                }
                if (r.startedAt().isBefore(staleBefore)) {
                    list.set(i, new Tuning.Run(r.id(), Tuning.RUN_FAILED, r.startedAt(), run.startedAt(), r.feedbackRead(), r.eventsRead(),
                            r.proposalsMade(), Tuning.ABANDONED_RUN_TEXT));
                    continue;
                }
                throw new Tuning.RunInProgressException();
            }
            list.add(run);
        }

        @Override
        public synchronized void finish(Tuning.Run run) {
            for (int i = 0; i < list.size(); i++) {
                if (list.get(i).id().equals(run.id())) {
                    list.set(i, run);
                    return;
                }
            }
        }

        @Override
        public synchronized List<Tuning.Run> list(int limit) {
            List<Tuning.Run> out = new ArrayList<>(list);
            out.sort(Comparator.comparing(Tuning.Run::startedAt).reversed());
            return limit > 0 && out.size() > limit ? new ArrayList<>(out.subList(0, limit)) : out;
        }
    }

    private static OffsetDateTime utc(Instant t) {
        return OffsetDateTime.ofInstant(t, ZoneOffset.UTC);
    }

    /** Adjustments in PostgreSQL. */
    public static final class PostgresAdjustments implements Tuning.AdjustmentRepository {
        private final DataSource dataSource;

        public PostgresAdjustments(DataSource dataSource) {
            this.dataSource = dataSource;
        }

        @Override
        public void save(Tuning.Adjustment a) {
            String sql = "INSERT INTO learning_adjustments (id, scope_key, source, event_type, repository, delta, status, evidence, impact, "
                    + "version, created_at, updated_at, resolved_by, resolved_at) VALUES (?,?,?,?,?,?,?,?::jsonb,?::jsonb,?,?,?,?,?) "
                    + "ON CONFLICT (id) DO UPDATE SET delta = EXCLUDED.delta, status = EXCLUDED.status, evidence = EXCLUDED.evidence, "
                    + "impact = EXCLUDED.impact, version = EXCLUDED.version, updated_at = EXCLUDED.updated_at, "
                    + "resolved_by = EXCLUDED.resolved_by, resolved_at = EXCLUDED.resolved_at";
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
                ps.setString(1, a.id());
                ps.setString(2, scopeKey(a));
                ps.setString(3, a.source());
                ps.setString(4, a.eventType());
                ps.setString(5, a.repository());
                ps.setInt(6, a.delta());
                ps.setString(7, a.status());
                ps.setString(8, JSON.writeValueAsString(evidenceJson(a.evidence())));
                ps.setString(9, JSON.writeValueAsString(impactJson(a.impact())));
                ps.setInt(10, a.version());
                ps.setObject(11, utc(a.createdAt()));
                ps.setObject(12, utc(a.updatedAt()));
                ps.setString(13, a.resolvedBy());
                ps.setObject(14, a.resolvedAt() == null ? null : utc(a.resolvedAt()), java.sql.Types.TIMESTAMP_WITH_TIMEZONE);
                ps.executeUpdate();
            } catch (SQLException | JsonProcessingException e) {
                throw new PortException("save adjustment: " + e.getMessage(), e);
            }
        }

        @Override
        public Tuning.Adjustment get(String id) {
            try (Connection c = dataSource.getConnection();
                 PreparedStatement ps = c.prepareStatement("SELECT " + COLUMNS + " FROM learning_adjustments WHERE id = ?")) {
                ps.setString(1, id);
                try (ResultSet rs = ps.executeQuery()) {
                    if (!rs.next()) {
                        throw new Tuning.NotFoundException();
                    }
                    return read(rs);
                }
            } catch (SQLException | JsonProcessingException e) {
                throw new PortException("get adjustment: " + e.getMessage(), e);
            }
        }

        @Override
        public List<Tuning.Adjustment> list(String status) {
            String sql = "SELECT " + COLUMNS + " FROM learning_adjustments WHERE (? = '' OR status = ?) ORDER BY updated_at DESC, id ASC LIMIT " + LIST_LIMIT;
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
                ps.setString(1, status);
                ps.setString(2, status);
                try (ResultSet rs = ps.executeQuery()) {
                    List<Tuning.Adjustment> out = new ArrayList<>();
                    while (rs.next()) {
                        out.add(read(rs));
                    }
                    return out;
                }
            } catch (SQLException | JsonProcessingException e) {
                throw new PortException("list adjustments: " + e.getMessage(), e);
            }
        }

        @Override
        public int maxVersion() {
            try (Connection c = dataSource.getConnection();
                 PreparedStatement ps = c.prepareStatement("SELECT COALESCE(MAX(version), 0) FROM learning_adjustments");
                 ResultSet rs = ps.executeQuery()) {
                return rs.next() ? rs.getInt(1) : 0;
            } catch (SQLException e) {
                throw new PortException("read adjustment version: " + e.getMessage(), e);
            }
        }

        private static String scopeKey(Tuning.Adjustment a) {
            // Lower-cased source, event type and repository joined with the unit separator, as the Go service keeps it.
            return a.source().toLowerCase(java.util.Locale.ROOT) + "\u001f" + a.eventType().toLowerCase(java.util.Locale.ROOT)
                    + "\u001f" + a.repository().toLowerCase(java.util.Locale.ROOT);
        }

        private static Map<String, Object> evidenceJson(Tuning.Evidence e) {
            Map<String, Object> m = new LinkedHashMap<>();
            m.put("positive", e.positive());
            m.put("negative", e.negative());
            m.put("people", e.people());
            m.put("signals", e.signals());
            m.put("windowDays", e.windowDays());
            m.put("summary", e.summary());
            return m;
        }

        private static Map<String, Object> impactJson(Tuning.Impact i) {
            Map<String, Object> m = new LinkedHashMap<>();
            m.put("replayed", i.replayed());
            m.put("wouldChange", i.wouldChange());
            return m;
        }

        private static Tuning.Adjustment read(ResultSet rs) throws SQLException, JsonProcessingException {
            JsonNode ev = JSON.readTree(rs.getString(7));
            JsonNode im = JSON.readTree(rs.getString(8));
            Timestamp resolved = rs.getTimestamp(13);
            return new Tuning.Adjustment(rs.getString(1), rs.getString(2), rs.getString(3), rs.getString(4), rs.getInt(5), rs.getString(6),
                    new Tuning.Evidence(ev.path("positive").asInt(), ev.path("negative").asInt(), ev.path("people").asInt(),
                            ev.path("signals").asInt(), ev.path("windowDays").asInt(), ev.path("summary").asText("")),
                    new Tuning.Impact(im.path("replayed").asInt(), im.path("wouldChange").asInt()), rs.getInt(9),
                    rs.getTimestamp(10).toInstant(), rs.getTimestamp(11).toInstant(), rs.getString(12), resolved == null ? null : resolved.toInstant());
        }
    }

    /** The run log in PostgreSQL. A partial unique index allows one running run at a time, across both services. */
    public static final class PostgresRuns implements Tuning.RunRepository {
        private final DataSource dataSource;

        public PostgresRuns(DataSource dataSource) {
            this.dataSource = dataSource;
        }

        @Override
        public void start(Tuning.Run run, Instant staleBefore) {
            try (Connection c = dataSource.getConnection()) {
                try (PreparedStatement ps = c.prepareStatement("UPDATE learning_runs SET status = 'failed', finished_at = ?, error = ? "
                        + "WHERE status = 'running' AND started_at < ?")) {
                    ps.setObject(1, utc(run.startedAt()));
                    ps.setString(2, Tuning.ABANDONED_RUN_TEXT);
                    ps.setObject(3, utc(staleBefore));
                    ps.executeUpdate();
                }
                try (PreparedStatement ps = c.prepareStatement("INSERT INTO learning_runs (id, status, started_at) VALUES (?, 'running', ?) "
                        + "ON CONFLICT (status) WHERE status = 'running' DO NOTHING")) {
                    ps.setString(1, run.id());
                    ps.setObject(2, utc(run.startedAt()));
                    if (ps.executeUpdate() == 0) {
                        throw new Tuning.RunInProgressException();
                    }
                }
            } catch (SQLException e) {
                throw new PortException("start learning run: " + e.getMessage(), e);
            }
        }

        @Override
        public void finish(Tuning.Run run) {
            String sql = "UPDATE learning_runs SET status = ?, finished_at = ?, feedback_read = ?, events_read = ?, proposals_made = ?, error = ? WHERE id = ?";
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
                ps.setString(1, run.status());
                ps.setObject(2, run.finishedAt() == null ? null : utc(run.finishedAt()), java.sql.Types.TIMESTAMP_WITH_TIMEZONE);
                ps.setInt(3, run.feedbackRead());
                ps.setInt(4, run.eventsRead());
                ps.setInt(5, run.proposalsMade());
                ps.setString(6, run.error());
                ps.setString(7, run.id());
                ps.executeUpdate();
            } catch (SQLException e) {
                throw new PortException("finish learning run: " + e.getMessage(), e);
            }
        }

        @Override
        public List<Tuning.Run> list(int limit) {
            int n = limit <= 0 || limit > 200 ? 20 : limit;
            String sql = "SELECT id, status, started_at, finished_at, feedback_read, events_read, proposals_made, error "
                    + "FROM learning_runs ORDER BY started_at DESC, id ASC LIMIT " + n;
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql); ResultSet rs = ps.executeQuery()) {
                List<Tuning.Run> out = new ArrayList<>();
                while (rs.next()) {
                    Timestamp finished = rs.getTimestamp(4);
                    out.add(new Tuning.Run(rs.getString(1), rs.getString(2), rs.getTimestamp(3).toInstant(), finished == null ? null : finished.toInstant(),
                            rs.getInt(5), rs.getInt(6), rs.getInt(7), rs.getString(8)));
                }
                return out;
            } catch (SQLException e) {
                throw new PortException("list learning runs: " + e.getMessage(), e);
            }
        }
    }
}
