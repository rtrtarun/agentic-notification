package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Delivery;
import com.agentpulse.notification.application.Ports.DeliveryStore;
import com.agentpulse.notification.application.Ports.PortException;

import javax.sql.DataSource;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.time.Duration;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/** Keeps the delivery queue in the {@code delivery_jobs} table the Go service migrates and also works from. */
public final class PostgresDeliveryStore implements DeliveryStore {
    private static final String COLUMNS = "id, event_id, channel, class, title, message, grp, status, attempts, "
            + "next_attempt_at, COALESCE(lease_until, 'epoch'), last_error, direct, created_at, updated_at";
    private static final String INSERT = "INSERT INTO delivery_jobs (id, event_id, channel, class, title, message, grp, "
            + "status, attempts, next_attempt_at, last_error, direct, created_at, updated_at) "
            + "VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)";

    private final DataSource dataSource;

    public PostgresDeliveryStore(DataSource dataSource) {
        this.dataSource = dataSource;
    }

    private static OffsetDateTime ts(Instant i) {
        return OffsetDateTime.ofInstant(i, ZoneOffset.UTC);
    }

    private void bind(PreparedStatement ps, Delivery.Job j) throws SQLException {
        ps.setString(1, j.id());
        ps.setString(2, j.eventId());
        ps.setString(3, j.channel());
        ps.setString(4, j.attentionClass());
        ps.setString(5, j.title());
        ps.setString(6, j.message());
        ps.setString(7, j.group());
        ps.setString(8, j.status());
        ps.setInt(9, j.attempts());
        ps.setObject(10, ts(j.nextAttemptAt()));
        ps.setString(11, j.lastError());
        ps.setBoolean(12, j.direct());
        ps.setObject(13, ts(j.createdAt()));
        ps.setObject(14, ts(j.updatedAt()));
    }

    private static Delivery.Job read(ResultSet rs) throws SQLException {
        return new Delivery.Job(rs.getString(1), rs.getString(2), rs.getString(3), rs.getString(4), rs.getString(5),
                rs.getString(6), rs.getString(7), rs.getString(8), rs.getInt(9),
                rs.getObject(10, OffsetDateTime.class).toInstant(), rs.getObject(11, OffsetDateTime.class).toInstant(),
                rs.getString(12), rs.getBoolean(13), rs.getObject(14, OffsetDateTime.class).toInstant(),
                rs.getObject(15, OffsetDateTime.class).toInstant());
    }

    @Override
    public boolean enqueue(Delivery.Job job) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement(INSERT + " ON CONFLICT (event_id, channel) DO NOTHING")) {
            bind(ps, job);
            return ps.executeUpdate() > 0;
        } catch (SQLException e) {
            throw new PortException("enqueue delivery: " + e.getMessage(), e);
        }
    }

    @Override
    public void record(Delivery.Job job) {
        String sql = INSERT + " ON CONFLICT (event_id, channel) DO UPDATE SET status = EXCLUDED.status, "
                + "attempts = EXCLUDED.attempts, last_error = EXCLUDED.last_error, updated_at = EXCLUDED.updated_at";
        try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            bind(ps, job);
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new PortException("record delivery: " + e.getMessage(), e);
        }
    }

    /** SKIP LOCKED lets several instances share the queue: each job is leased to exactly one of them. */
    @Override
    public List<Delivery.Job> claim(Instant now, Duration lease, int limit) {
        String sql = "UPDATE delivery_jobs SET status = 'sending', attempts = attempts + 1, lease_until = ?, updated_at = ? "
                + "WHERE id IN (SELECT id FROM delivery_jobs WHERE (status = 'pending' AND next_attempt_at <= ?) "
                + "OR (status = 'sending' AND lease_until <= ?) ORDER BY next_attempt_at LIMIT ? FOR UPDATE SKIP LOCKED) "
                + "RETURNING " + COLUMNS;
        try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            ps.setObject(1, ts(now.plus(lease)));
            ps.setObject(2, ts(now));
            ps.setObject(3, ts(now));
            ps.setObject(4, ts(now));
            ps.setInt(5, limit);
            try (ResultSet rs = ps.executeQuery()) {
                return collect(rs);
            }
        } catch (SQLException e) {
            throw new PortException("claim deliveries: " + e.getMessage(), e);
        }
    }

    @Override
    public void complete(String id, Instant now) {
        exec("UPDATE delivery_jobs SET status = 'sent', last_error = '', updated_at = ? WHERE id = ?", ts(now), id);
    }

    @Override
    public void fail(String id, String error, Instant retryAt, Instant now) {
        if (retryAt == null) {
            exec("UPDATE delivery_jobs SET status = 'dead', last_error = ?, updated_at = ? WHERE id = ?", error, ts(now), id);
        } else {
            exec("UPDATE delivery_jobs SET status = 'pending', last_error = ?, updated_at = ?, next_attempt_at = ? WHERE id = ?",
                    error, ts(now), ts(retryAt), id);
        }
    }

    @Override
    public void requeue(String id, Instant now) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("UPDATE delivery_jobs SET status = 'pending', attempts = 0, "
                     + "next_attempt_at = ?, updated_at = ? WHERE id = ? AND status = 'dead'")) {
            ps.setObject(1, ts(now));
            ps.setObject(2, ts(now));
            ps.setString(3, id);
            if (ps.executeUpdate() > 0) {
                return;
            }
            try (PreparedStatement q = c.prepareStatement("SELECT status FROM delivery_jobs WHERE id = ?")) {
                q.setString(1, id);
                try (ResultSet rs = q.executeQuery()) {
                    if (!rs.next()) {
                        throw new Delivery.NotFoundException();
                    }
                    throw new Delivery.NotRetryableException();
                }
            }
        } catch (SQLException e) {
            throw new PortException("retry delivery: " + e.getMessage(), e);
        }
    }

    private void exec(String sql, Object... args) {
        try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            for (int i = 0; i < args.length; i++) {
                ps.setObject(i + 1, args[i]);
            }
            if (ps.executeUpdate() == 0) {
                throw new Delivery.NotFoundException();
            }
        } catch (SQLException e) {
            throw new PortException("update delivery: " + e.getMessage(), e);
        }
    }

    @Override
    public List<Delivery.Job> list(int limit) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("SELECT " + COLUMNS + " FROM delivery_jobs ORDER BY created_at DESC LIMIT ?")) {
            ps.setInt(1, limit);
            try (ResultSet rs = ps.executeQuery()) {
                return collect(rs);
            }
        } catch (SQLException e) {
            throw new PortException("list deliveries: " + e.getMessage(), e);
        }
    }

    @Override
    public Map<String, Integer> counts() {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("SELECT status, COUNT(*) FROM delivery_jobs GROUP BY status");
             ResultSet rs = ps.executeQuery()) {
            Map<String, Integer> out = new HashMap<>();
            while (rs.next()) {
                out.put(rs.getString(1), rs.getInt(2));
            }
            return out;
        } catch (SQLException e) {
            throw new PortException("count deliveries: " + e.getMessage(), e);
        }
    }

    private static List<Delivery.Job> collect(ResultSet rs) throws SQLException {
        List<Delivery.Job> out = new ArrayList<>();
        while (rs.next()) {
            out.add(read(rs));
        }
        return out;
    }
}
