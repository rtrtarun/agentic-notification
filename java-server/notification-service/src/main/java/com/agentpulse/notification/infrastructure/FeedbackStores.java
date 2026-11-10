package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Feedback;
import com.agentpulse.notification.application.Overrides;
import com.agentpulse.notification.application.Ports.PortException;

import javax.sql.DataSource;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;

/**
 * Stores for overrides and feedback: in memory (no database, and tests) and PostgreSQL, in the {@code overrides} and
 * {@code feedback} tables the Go service migrates and also uses.
 */
public final class FeedbackStores {
    /** MemoryFeedback keeps at most this many entries; the oldest go first. */
    public static final int MAX_IN_MEMORY = 10_000;

    private FeedbackStores() {}

    private static OffsetDateTime ts(Instant i) {
        return OffsetDateTime.ofInstant(i, ZoneOffset.UTC);
    }

    /** Overrides in memory. */
    public static final class MemoryOverrides implements Overrides.Store {
        private final List<Overrides.Override> list = new ArrayList<>();

        @Override
        public synchronized List<Overrides.Override> load() {
            return List.copyOf(list);
        }

        @Override
        public synchronized Overrides.Override save(Overrides.Override o) {
            for (int i = 0; i < list.size(); i++) {
                if (Overrides.scopeKey(list.get(i)).equals(Overrides.scopeKey(o))) {
                    Overrides.Override old = list.get(i);
                    Overrides.Override merged = new Overrides.Override(old.id(), o.source(), o.eventType(), o.repository(),
                            o.mode(), o.createdBy(), o.note(), old.createdAt());
                    list.set(i, merged);
                    return merged;
                }
            }
            list.add(o);
            return o;
        }

        @Override
        public synchronized void delete(String id) {
            if (!list.removeIf(o -> o.id().equals(id))) {
                throw new Overrides.NotFoundException();
            }
        }
    }

    /** Overrides in PostgreSQL. The same scope keeps its id and creation time and takes the new mode. */
    public static final class PostgresOverrides implements Overrides.Store {
        private final DataSource dataSource;

        public PostgresOverrides(DataSource dataSource) {
            this.dataSource = dataSource;
        }

        @Override
        public List<Overrides.Override> load() {
            String sql = "SELECT id, source, event_type, repository, mode, created_by, note, created_at "
                    + "FROM overrides ORDER BY created_at DESC";
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql);
                 ResultSet rs = ps.executeQuery()) {
                List<Overrides.Override> out = new ArrayList<>();
                while (rs.next()) {
                    out.add(new Overrides.Override(rs.getString(1), rs.getString(2), rs.getString(3), rs.getString(4),
                            rs.getString(5), rs.getString(6), rs.getString(7),
                            rs.getObject(8, OffsetDateTime.class).toInstant()));
                }
                return out;
            } catch (SQLException e) {
                throw new PortException("load overrides: " + e.getMessage(), e);
            }
        }

        @Override
        public Overrides.Override save(Overrides.Override o) {
            String sql = "INSERT INTO overrides (id, scope_key, source, event_type, repository, mode, created_by, note, created_at) "
                    + "VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT (scope_key) DO UPDATE SET mode = EXCLUDED.mode, "
                    + "created_by = EXCLUDED.created_by, note = EXCLUDED.note RETURNING id, created_at";
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
                ps.setString(1, o.id());
                ps.setString(2, Overrides.scopeKey(o));
                ps.setString(3, o.source());
                ps.setString(4, o.eventType());
                ps.setString(5, o.repository());
                ps.setString(6, o.mode());
                ps.setString(7, o.createdBy());
                ps.setString(8, o.note());
                ps.setObject(9, ts(o.createdAt()));
                try (ResultSet rs = ps.executeQuery()) {
                    rs.next();
                    return o.withIdAndTime(rs.getString(1), rs.getObject(2, OffsetDateTime.class).toInstant());
                }
            } catch (SQLException e) {
                throw new PortException("save override: " + e.getMessage(), e);
            }
        }

        @Override
        public void delete(String id) {
            try (Connection c = dataSource.getConnection();
                 PreparedStatement ps = c.prepareStatement("DELETE FROM overrides WHERE id = ?")) {
                ps.setString(1, id);
                if (ps.executeUpdate() == 0) {
                    throw new Overrides.NotFoundException();
                }
            } catch (SQLException e) {
                throw new PortException("delete override: " + e.getMessage(), e);
            }
        }
    }

    /** Feedback in memory, bounded. */
    public static final class MemoryFeedback implements Feedback.Store {
        private final List<Feedback.Entry> list = new ArrayList<>();

        @Override
        public synchronized void add(Feedback.Entry e) {
            if (list.size() >= MAX_IN_MEMORY) {
                list.remove(0);
            }
            list.add(e);
        }

        @Override
        public synchronized List<Feedback.Entry> list(Feedback.Filter f) {
            List<Feedback.Entry> out = new ArrayList<>();
            for (int i = list.size() - 1; i >= 0; i--) {
                Feedback.Entry x = list.get(i);
                if ((!f.eventId().isEmpty() && !x.eventId().equals(f.eventId()))
                        || (!f.userId().isEmpty() && !x.userId().equals(f.userId()))
                        || x.createdAt().isBefore(f.since())) {
                    continue;
                }
                out.add(x);
                if (f.limit() > 0 && out.size() >= f.limit()) {
                    break;
                }
            }
            return out;
        }
    }

    /** Feedback in PostgreSQL. */
    public static final class PostgresFeedback implements Feedback.Store {
        private final DataSource dataSource;

        public PostgresFeedback(DataSource dataSource) {
            this.dataSource = dataSource;
        }

        @Override
        public void add(Feedback.Entry f) {
            String sql = "INSERT INTO feedback (id, event_id, user_id, action, scope, reason, override_id, source, "
                    + "event_type, repository, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)";
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
                ps.setString(1, f.id());
                ps.setString(2, f.eventId());
                ps.setString(3, f.userId());
                ps.setString(4, f.action());
                ps.setString(5, f.scope());
                ps.setString(6, f.reason());
                ps.setString(7, f.overrideId());
                ps.setString(8, f.source());
                ps.setString(9, f.eventType());
                ps.setString(10, f.repository());
                ps.setObject(11, ts(f.createdAt()));
                ps.executeUpdate();
            } catch (SQLException e) {
                throw new PortException("add feedback: " + e.getMessage(), e);
            }
        }

        @Override
        public List<Feedback.Entry> list(Feedback.Filter f) {
            String sql = "SELECT id, event_id, user_id, action, scope, reason, override_id, source, event_type, repository, "
                    + "created_at FROM feedback WHERE (? = '' OR event_id = ?) AND (? = '' OR user_id = ?) AND created_at >= ? "
                    + "ORDER BY created_at DESC LIMIT ?";
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
                ps.setString(1, f.eventId());
                ps.setString(2, f.eventId());
                ps.setString(3, f.userId());
                ps.setString(4, f.userId());
                ps.setObject(5, ts(f.since()));
                ps.setInt(6, f.limit() > 0 ? f.limit() : 10_000);
                try (ResultSet rs = ps.executeQuery()) {
                    List<Feedback.Entry> out = new ArrayList<>();
                    while (rs.next()) {
                        out.add(new Feedback.Entry(rs.getString(1), rs.getString(2), rs.getString(3), rs.getString(4),
                                rs.getString(5), rs.getString(6), rs.getString(7), rs.getString(8), rs.getString(9),
                                rs.getString(10), rs.getObject(11, OffsetDateTime.class).toInstant()));
                    }
                    return out;
                }
            } catch (SQLException e) {
                throw new PortException("list feedback: " + e.getMessage(), e);
            }
        }
    }
}
