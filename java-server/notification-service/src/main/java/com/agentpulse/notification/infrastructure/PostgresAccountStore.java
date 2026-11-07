package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Accounts.AccountException;
import com.agentpulse.notification.application.Accounts.AuditEntry;
import com.agentpulse.notification.application.Accounts.Session;
import com.agentpulse.notification.application.Accounts.Store;
import com.agentpulse.notification.application.Accounts.User;
import com.agentpulse.notification.application.Ports.PortException;

import javax.sql.DataSource;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Timestamp;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;

/**
 * Keeps accounts, sessions and the audit trail in the {@code users}, {@code sessions} and {@code audit_log}
 * tables the Go service migrates (including the {@code users.status} column). This service never alters the schema.
 */
public final class PostgresAccountStore implements Store {
    private static final String USER_COLUMNS = "id, email, name, role, password_hash, disabled, status, created_at";
    private static final String UNIQUE_VIOLATION = "23505";

    private final DataSource dataSource;

    public PostgresAccountStore(DataSource dataSource) {
        this.dataSource = dataSource;
    }

    private static User user(ResultSet rs) throws SQLException {
        return new User(rs.getString(1), rs.getString(2), rs.getString(3), rs.getString(4), rs.getString(5),
                rs.getBoolean(6), rs.getString(7), rs.getTimestamp(8).toInstant());
    }

    @Override
    public void createUser(User u) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("INSERT INTO users (" + USER_COLUMNS + ") VALUES (?,?,?,?,?,?,?,?)")) {
            ps.setString(1, u.id());
            ps.setString(2, u.email());
            ps.setString(3, u.name());
            ps.setString(4, u.role());
            ps.setString(5, u.passwordHash());
            ps.setBoolean(6, u.disabled());
            ps.setString(7, u.statusOrActive());
            ps.setTimestamp(8, Timestamp.from(u.createdAt()));
            ps.executeUpdate();
        } catch (SQLException e) {
            if (UNIQUE_VIOLATION.equals(e.getSQLState())) {
                throw new AccountException(AccountException.Kind.EMAIL_TAKEN, "an account with that email already exists");
            }
            throw new PortException("create user: " + e.getMessage(), e);
        }
    }

    private Optional<User> one(String where, String value) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("SELECT " + USER_COLUMNS + " FROM users WHERE " + where + " = ?")) {
            ps.setString(1, value);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? Optional.of(user(rs)) : Optional.empty();
            }
        } catch (SQLException e) {
            throw new PortException("read user: " + e.getMessage(), e);
        }
    }

    @Override
    public Optional<User> userById(String id) {
        return one("id", id);
    }

    @Override
    public Optional<User> userByEmail(String email) {
        return one("email", email);
    }

    @Override
    public List<User> listUsers() {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("SELECT " + USER_COLUMNS + " FROM users ORDER BY email");
             ResultSet rs = ps.executeQuery()) {
            List<User> out = new ArrayList<>();
            while (rs.next()) {
                out.add(user(rs));
            }
            return out;
        } catch (SQLException e) {
            throw new PortException("list users: " + e.getMessage(), e);
        }
    }

    @Override
    public void updateUser(User u) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement(
                     "UPDATE users SET name=?, role=?, password_hash=?, disabled=?, status=? WHERE id=?")) {
            ps.setString(1, u.name());
            ps.setString(2, u.role());
            ps.setString(3, u.passwordHash());
            ps.setBoolean(4, u.disabled());
            ps.setString(5, u.statusOrActive());
            ps.setString(6, u.id());
            if (ps.executeUpdate() == 0) {
                throw new AccountException(AccountException.Kind.NOT_FOUND, "not found");
            }
        } catch (SQLException e) {
            throw new PortException("update user: " + e.getMessage(), e);
        }
    }

    private int count(String sql) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement(sql);
             ResultSet rs = ps.executeQuery()) {
            return rs.next() ? rs.getInt(1) : 0;
        } catch (SQLException e) {
            throw new PortException("count users: " + e.getMessage(), e);
        }
    }

    @Override
    public int countUsers() {
        return count("SELECT COUNT(*) FROM users");
    }

    @Override
    public int countActiveAdmins() {
        return count("SELECT COUNT(*) FROM users WHERE role = 'admin' AND NOT disabled AND status = 'active'");
    }

    @Override
    public void createSession(Session s) {
        exec("INSERT INTO sessions (hash, user_id, csrf, expires_at, created_at) VALUES (?,?,?,?,?)", ps -> {
            ps.setString(1, s.hash());
            ps.setString(2, s.userId());
            ps.setString(3, s.csrf());
            ps.setTimestamp(4, Timestamp.from(s.expiresAt()));
            ps.setTimestamp(5, Timestamp.from(s.createdAt()));
        });
    }

    @Override
    public Optional<Session> sessionByHash(String hash) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("SELECT hash, user_id, csrf, expires_at, created_at FROM sessions WHERE hash = ?")) {
            ps.setString(1, hash);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? Optional.of(new Session(rs.getString(1), rs.getString(2), rs.getString(3),
                        rs.getTimestamp(4).toInstant(), rs.getTimestamp(5).toInstant())) : Optional.empty();
            }
        } catch (SQLException e) {
            throw new PortException("read session: " + e.getMessage(), e);
        }
    }

    @Override
    public void deleteSession(String hash) {
        exec("DELETE FROM sessions WHERE hash = ?", ps -> ps.setString(1, hash));
    }

    @Override
    public void deleteUserSessions(String userId) {
        exec("DELETE FROM sessions WHERE user_id = ?", ps -> ps.setString(1, userId));
    }

    @Override
    public void deleteExpiredSessions(Instant now) {
        exec("DELETE FROM sessions WHERE expires_at <= ?", ps -> ps.setTimestamp(1, Timestamp.from(now)));
    }

    @Override
    public void addAudit(AuditEntry e) {
        exec("INSERT INTO audit_log (at, actor, action, target, ip, detail) VALUES (?,?,?,?,?,?)", ps -> {
            ps.setTimestamp(1, Timestamp.from(e.at()));
            ps.setString(2, e.actor());
            ps.setString(3, e.action());
            ps.setString(4, e.target());
            ps.setString(5, e.ip());
            ps.setString(6, e.detail());
        });
    }

    @Override
    public List<AuditEntry> listAudit(int limit) {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("SELECT at, actor, action, target, ip, detail FROM audit_log ORDER BY id DESC LIMIT ?")) {
            ps.setInt(1, limit > 0 ? limit : 100);
            try (ResultSet rs = ps.executeQuery()) {
                List<AuditEntry> out = new ArrayList<>();
                while (rs.next()) {
                    out.add(new AuditEntry(rs.getTimestamp(1).toInstant(), rs.getString(2), rs.getString(3), rs.getString(4),
                            rs.getString(5), rs.getString(6)));
                }
                return out;
            }
        } catch (SQLException e) {
            throw new PortException("list audit: " + e.getMessage(), e);
        }
    }

    private interface Binder {
        void bind(PreparedStatement ps) throws SQLException;
    }

    private void exec(String sql, Binder binder) {
        try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            binder.bind(ps);
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new PortException(e.getMessage(), e);
        }
    }
}
