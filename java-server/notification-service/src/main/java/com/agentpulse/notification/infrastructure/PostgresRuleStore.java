package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.application.Ports.RuleStore;
import com.agentpulse.notification.domain.Rule;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;

import javax.sql.DataSource;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.util.ArrayList;
import java.util.List;

/**
 * Keeps rules in the {@code policy_rules} table the Go service migrates and also reads. A stored rule that uses a
 * field this service does not know is refused loudly: ignoring it could silently widen the rule.
 */
public final class PostgresRuleStore implements RuleStore {
    private static final ObjectMapper JSON = new ObjectMapper()
            .configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, true);

    private final DataSource dataSource;

    public PostgresRuleStore(DataSource dataSource) {
        this.dataSource = dataSource;
    }

    @Override
    public List<Rule> list() {
        try (Connection c = dataSource.getConnection();
             PreparedStatement ps = c.prepareStatement("SELECT body FROM policy_rules ORDER BY position");
             ResultSet rs = ps.executeQuery()) {
            List<Rule> out = new ArrayList<>();
            while (rs.next()) {
                out.add(JSON.readValue(rs.getString(1), Rule.class));
            }
            return out;
        } catch (JsonProcessingException e) {
            throw new PortException("a stored rule uses a field this service does not know: " + e.getOriginalMessage(), e);
        } catch (SQLException e) {
            throw new PortException("list rules: " + e.getMessage(), e);
        }
    }

    /** Swaps the whole list in one transaction, so a failure never leaves a half-written rule set. */
    @Override
    public void replace(List<Rule> rules) {
        try (Connection c = dataSource.getConnection()) {
            c.setAutoCommit(false);
            try {
                try (PreparedStatement del = c.prepareStatement("DELETE FROM policy_rules")) {
                    del.executeUpdate();
                }
                try (PreparedStatement ins = c.prepareStatement("INSERT INTO policy_rules (position, id, body) VALUES (?, ?, ?::jsonb)")) {
                    for (int i = 0; i < rules.size(); i++) {
                        ins.setInt(1, i);
                        ins.setString(2, rules.get(i).id());
                        ins.setString(3, JSON.writeValueAsString(rules.get(i)));
                        ins.executeUpdate();
                    }
                }
                c.commit();
            } catch (SQLException | JsonProcessingException e) {
                c.rollback();
                throw e;
            }
        } catch (SQLException | JsonProcessingException e) {
            throw new PortException("replace rules: " + e.getMessage(), e);
        }
    }
}
