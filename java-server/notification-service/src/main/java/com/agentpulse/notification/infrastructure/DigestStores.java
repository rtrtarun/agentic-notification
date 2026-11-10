package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Digests;
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
import java.util.List;
import java.util.Map;

/**
 * Stores for digests: in memory (no database, and tests) and PostgreSQL, as JSON in the {@code digests} table the Go
 * service migrates and also uses. The JSON is the Go service's, so either service reads the other's digests.
 */
public final class DigestStores {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final TypeReference<Map<String, Object>> MAP = new TypeReference<>() {};

    private DigestStores() {}

    /** Digests in memory. */
    public static final class Memory implements Digests.Store {
        private final List<Digests.Digest> list = new ArrayList<>();

        @Override
        public synchronized void add(Digests.Digest d) {
            list.add(d);
        }

        @Override
        public synchronized List<Digests.Digest> list(int limit) {
            List<Digests.Digest> out = new ArrayList<>();
            for (int i = list.size() - 1; i >= 0; i--) {
                if (!list.get(i).userId().isEmpty()) {
                    continue;
                }
                out.add(list.get(i));
                if (limit > 0 && out.size() >= limit) {
                    break;
                }
            }
            return out;
        }

        @Override
        public synchronized Digests.Digest get(String id) {
            return list.stream().filter(d -> d.id().equals(id)).findFirst().orElseThrow(Digests.NotFoundException::new);
        }
    }

    /** Digests in PostgreSQL. */
    public static final class Postgres implements Digests.Store {
        private final DataSource dataSource;

        public Postgres(DataSource dataSource) {
            this.dataSource = dataSource;
        }

        @Override
        public void add(Digests.Digest d) {
            try (Connection c = dataSource.getConnection();
                 PreparedStatement ps = c.prepareStatement(
                         "INSERT INTO digests (id, window_end, body, created_at) VALUES (?,?,?::jsonb,?)")) {
                ps.setString(1, d.id());
                ps.setObject(2, OffsetDateTime.ofInstant(d.to(), ZoneOffset.UTC));
                ps.setString(3, JSON.writeValueAsString(Digests.toMap(d)));
                ps.setObject(4, OffsetDateTime.ofInstant(d.createdAt(), ZoneOffset.UTC));
                ps.executeUpdate();
            } catch (SQLException | JsonProcessingException e) {
                throw new PortException("add digest: " + e.getMessage(), e);
            }
        }

        @Override
        public List<Digests.Digest> list(int limit) {
            String sql = "SELECT body FROM digests WHERE COALESCE(body->>'userId','') = '' ORDER BY created_at DESC LIMIT ?";
            try (Connection c = dataSource.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
                ps.setInt(1, limit > 0 ? limit : 100);
                try (ResultSet rs = ps.executeQuery()) {
                    List<Digests.Digest> out = new ArrayList<>();
                    while (rs.next()) {
                        out.add(Digests.fromMap(JSON.readValue(rs.getString(1), MAP)));
                    }
                    return out;
                }
            } catch (SQLException | JsonProcessingException e) {
                throw new PortException("list digests: " + e.getMessage(), e);
            }
        }

        @Override
        public Digests.Digest get(String id) {
            try (Connection c = dataSource.getConnection();
                 PreparedStatement ps = c.prepareStatement("SELECT body FROM digests WHERE id = ?")) {
                ps.setString(1, id);
                try (ResultSet rs = ps.executeQuery()) {
                    if (!rs.next()) {
                        throw new Digests.NotFoundException();
                    }
                    return Digests.fromMap(JSON.readValue(rs.getString(1), MAP));
                }
            } catch (SQLException | JsonProcessingException e) {
                throw new PortException("get digest: " + e.getMessage(), e);
            }
        }
    }
}
