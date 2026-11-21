package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.domain.DecisionState;
import com.agentpulse.notification.domain.Event;
import com.zaxxer.hikari.HikariDataSource;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

import java.sql.Connection;
import java.sql.Statement;
import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/**
 * Runs against a real PostgreSQL whose schema the Go service migrated (run {@code go run ./cmd/migrate} first).
 * Set TEST_DATABASE_URL, for example {@code postgres://attention:attention@localhost:5432/attention}. Without it
 * the tests are skipped, so a plain {@code mvn test} needs no database; CI sets it.
 */
class PostgresEventRepositoryTest {
    private static HikariDataSource source;
    private static PostgresEventRepository repo;
    private final String run = UUID.randomUUID().toString().substring(0, 8);

    @BeforeAll
    static void connect() {
        String url = System.getenv("TEST_DATABASE_URL");
        assumeTrue(url != null && !url.isEmpty(), "TEST_DATABASE_URL not set; skipping the PostgreSQL tests");
        source = Database.connect(url);
        Database.waitForSchema(source, Duration.ofSeconds(10));
        repo = new PostgresEventRepository(source);
    }

    @AfterAll
    static void close() throws Exception {
        if (source != null) {
            try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
                s.execute("DELETE FROM events WHERE id LIKE 'java-test-%'");
            }
            source.close();
        }
    }

    private Event event(String suffix) {
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("action", "opened");
        payload.put("number", 42);
        payload.put("nested", Map.of("a", List.of(1, 2)));
        Event e = new Event("java-test-" + run + "-" + suffix, "github", "pull_request", "acme/api", payload);
        e.setAttentionClass("standard");
        DecisionState d = new DecisionState("default routing", "default-github");
        d.setStatus("queued");
        e.setDecisionState(d);
        e.setDedupKey("github:acme/api:42|pull_request|opened|");
        e.setThreadId("github:acme/api:42");
        return e;
    }

    @Test
    void anEventRoundTripsThroughTheGoSchema() {
        Event saved = event("a");
        repo.save(saved);

        assertTrue(repo.exists(saved.getId()));
        Event read = repo.listRecent(500).stream().filter(e -> e.getId().equals(saved.getId())).findFirst().orElseThrow();
        assertEquals("github", read.getSource());
        assertEquals("pull_request", read.getEventType());
        assertEquals("acme/api", read.getRepository());
        assertEquals("standard", read.getAttentionClass());
        assertEquals("queued", read.getDecisionState().getStatus());
        assertEquals("default routing", read.getDecisionState().getReason());
        assertEquals("default-github", read.getDecisionState().getMatchedRule());
        assertEquals("github:acme/api:42", read.getThreadId());
        assertEquals("", read.getMergedInto());
        assertEquals(42, ((Number) read.getPayload().get("number")).intValue());
        assertEquals(Map.of("a", List.of(1, 2)), read.getPayload().get("nested"));
        assertNotNull(read.getCreatedAt());
    }

    @Test
    void savingTheSameIdTwiceKeepsTheFirst() {
        Event first = event("b");
        repo.save(first);
        Event second = event("b");
        second.setMergedInto("someone");
        repo.save(second);

        Event read = repo.listRecent(500).stream().filter(e -> e.getId().equals(first.getId())).findFirst().orElseThrow();
        assertEquals("", read.getMergedInto());
    }

    /** Milestone 39a: the chain columns are stored exactly as Go stores them, NULL outside a run, never ''. */
    @Test
    void theChainColumnsAreNullOutsideARunAndRoundTripInsideOne() throws Exception {
        Event step = event("step");
        step.setTraceId("run-" + run);
        step.setParentEventId("java-test-" + run + "-plan");
        step.setAgentName("researcher");
        repo.save(step);
        Event webhook = event("hook");
        repo.save(webhook);

        Event read = repo.listRecent(500).stream().filter(e -> e.getId().equals(step.getId())).findFirst().orElseThrow();
        assertEquals("run-" + run, read.getTraceId());
        assertEquals("java-test-" + run + "-plan", read.getParentEventId());
        assertEquals("researcher", read.getAgentName());
        try (Connection c = source.getConnection(); Statement s = c.createStatement();
             java.sql.ResultSet rs = s.executeQuery("SELECT trace_id, parent_event_id, agent_name FROM events WHERE id = '" + webhook.getId() + "'")) {
            assertTrue(rs.next());
            assertNull(rs.getString(1), "no trace is NULL, as Go's nullString writes it");
            assertNull(rs.getString(2));
            assertNull(rs.getString(3));
        }
        Event hook = repo.listRecent(500).stream().filter(e -> e.getId().equals(webhook.getId())).findFirst().orElseThrow();
        assertEquals("", hook.getTraceId(), "NULL reads back as empty");
        assertEquals("", hook.getParentEventId());
        assertEquals("", hook.getAgentName());
    }

    /** A row written the way the Go service writes it, at one instant with another row: read the same, ties by id. */
    @Test
    void rowsGoWroteReadTheSameAndTiesAreOrderedById() throws Exception {
        String a = "java-test-" + run + "-tie-a";
        String b = "java-test-" + run + "-tie-b";
        try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
            for (String id : List.of(b, a)) {
                s.execute("INSERT INTO events (id, source, event_type, repository, attention_class, payload, attention_state, "
                        + "trace_id, parent_event_id, agent_name, created_at) VALUES ('" + id + "', 'agent', 'agent.plan', '', "
                        + "'standard', '{}'::jsonb, 'done', 'tie-" + run + "', NULL, NULL, '2030-01-01T00:00:00Z')");
            }
        }
        List<Event> newest = repo.listRecent(2);
        assertEquals(List.of(b, a), newest.stream().map(Event::getId).toList(), "created_at DESC, id DESC, as Go's List now orders");
        assertEquals("tie-" + run, newest.get(0).getTraceId());
        assertEquals("", newest.get(0).getParentEventId());
        try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
            s.execute("DELETE FROM events WHERE id IN ('" + a + "', '" + b + "')");
        }
    }

    @Test
    void aMissingEventDoesNotExistAndAnEventWithoutADecisionReadsBack() {
        assertFalse(repo.exists("java-test-" + run + "-missing"));

        Event bare = new Event("java-test-" + run + "-bare", "github", "star", "", null);
        repo.save(bare);
        Event read = repo.listRecent(500).stream().filter(e -> e.getId().equals(bare.getId())).findFirst().orElseThrow();
        assertNull(read.getDecisionState());
        assertTrue(read.getPayload().isEmpty());
    }
}
