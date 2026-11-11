package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Learning;
import com.zaxxer.hikari.HikariDataSource;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

import java.sql.Connection;
import java.sql.Statement;
import java.time.Duration;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.List;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/**
 * The suggestion store against a real PostgreSQL the Go service migrated, including a suggestion written the way Go
 * writes one. Set TEST_DATABASE_URL; without it the tests are skipped. They use their own rows and remove them afterwards.
 */
class PostgresLearningStoresTest {
    private static HikariDataSource source;
    private final String run = UUID.randomUUID().toString().substring(0, 8);

    @BeforeAll
    static void connect() {
        String url = System.getenv("TEST_DATABASE_URL");
        assumeTrue(url != null && !url.isEmpty(), "TEST_DATABASE_URL not set; skipping the PostgreSQL tests");
        source = Database.connect(url);
        Database.waitForSchema(source, Duration.ofSeconds(10));
    }

    @AfterAll
    static void close() throws Exception {
        if (source != null) {
            try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
                s.execute("DELETE FROM learning_suggestions WHERE id LIKE 'java-test-%'");
            }
            source.close();
        }
    }

    private Learning.Suggestion suggestion(String suffix, String status, Instant updated) {
        return new Learning.Suggestion("java-test-" + run + "-" + suffix, "demote", status, "github", "pull_request", "acme/api", "rid",
                "default", "summary", "proposed", new Learning.Evidence(5, 1, 0, 0, 0), updated.minusSeconds(60), updated,
                status.equals("pending") ? "" : "admin", status.equals("pending") ? null : updated);
    }

    @Test
    void aSuggestionIsSavedReadBackReplacedAndListedByStatusNewestFirst() {
        LearningStores.Postgres store = new LearningStores.Postgres(source);
        Instant now = Instant.now().truncatedTo(ChronoUnit.MICROS);
        Learning.Suggestion older = suggestion("older", "pending", now.plusSeconds(1000));
        Learning.Suggestion newer = suggestion("newer", "pending", now.plusSeconds(2000));
        Learning.Suggestion done = suggestion("done", "applied", now.plusSeconds(3000));
        store.save(older);
        store.save(newer);
        store.save(done);

        assertEquals(newer, store.get(newer.id()));
        assertEquals(done, store.get(done.id()));
        List<String> pending = store.list("pending").stream().map(Learning.Suggestion::id).toList();
        assertTrue(pending.indexOf(newer.id()) < pending.indexOf(older.id()), "newest update first");
        assertTrue(!pending.contains(done.id()));
        assertTrue(store.list("applied").stream().anyMatch(s -> s.id().equals(done.id())));
        assertTrue(store.list("").stream().anyMatch(s -> s.id().equals(done.id())), "an empty status means all");

        Learning.Suggestion dismissed = suggestion("older", "dismissed", now.plusSeconds(4000));
        store.save(dismissed);
        assertEquals("dismissed", store.get(older.id()).status(), "saving again replaces it");
        assertThrows(Learning.NotFoundException.class, () -> store.get("java-test-missing-" + run));
    }

    @Test
    void aSuggestionWrittenByTheGoServiceIsRead() throws Exception {
        LearningStores.Postgres store = new LearningStores.Postgres(source);
        String id = "java-test-" + run + "-go";
        String body = "{\"id\":\"" + id + "\",\"kind\":\"promote\",\"status\":\"pending\",\"source\":\"github\",\"eventType\":\"star\","
                + "\"ruleId\":\"r1\",\"ruleName\":\"github-star\",\"summary\":\"s\",\"proposed\":\"p\","
                + "\"evidence\":{\"dismissed\":0,\"never\":0,\"opened\":0,\"reply\":0,\"always\":2},"
                + "\"createdAt\":\"2099-11-10T06:02:53.2696195Z\",\"updatedAt\":\"2099-11-10T06:02:53.2696195Z\"}";
        try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
            s.execute("INSERT INTO learning_suggestions (id, status, body, created_at, updated_at) VALUES ('" + id
                    + "', 'pending', '" + body + "'::jsonb, '2099-11-10T06:02:53Z', '2099-11-10T06:02:53Z')");
        }
        Learning.Suggestion read = store.get(id);
        assertEquals("promote", read.kind());
        assertEquals(2, read.evidence().always());
        assertEquals("", read.repository());
        assertEquals("", read.resolvedBy());
        assertTrue(store.list("pending").stream().anyMatch(s -> s.id().equals(id)));
    }
}
