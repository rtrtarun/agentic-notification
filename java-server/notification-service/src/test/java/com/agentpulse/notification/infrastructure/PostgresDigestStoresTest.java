package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Digests;
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
 * The digest store against a real PostgreSQL the Go service migrated, including a digest written the way Go writes
 * one. Set TEST_DATABASE_URL; without it the tests are skipped. They use their own rows and remove them afterwards.
 */
class PostgresDigestStoresTest {
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
                s.execute("DELETE FROM digests WHERE id LIKE 'java-test-%'");
            }
            source.close();
        }
    }

    private Digests.Digest digest(String suffix, String user, Instant at) {
        return new Digests.Digest("java-test-" + run + "-" + suffix, at.minusSeconds(5), at, 2, "2 notifications held back", "summary",
                List.of(new Digests.Group("github/acme/api", "acme/api (github)", "2 notifications: 2 star", 2, List.of("a", "b"))),
                List.of(new Digests.Highlight("a", "github star in acme/api", "scored 60: x", 60)), "heuristic-v1", user, "heuristic", "sam", at);
    }

    @Test
    void aDigestIsStoredReadBackByIdAndListedNewestFirstWithoutPersonalOnes() {
        DigestStores.Postgres store = new DigestStores.Postgres(source);
        Instant now = Instant.now().truncatedTo(ChronoUnit.MICROS);
        Digests.Digest older = digest("older", "", now.plusSeconds(1000));
        Digests.Digest newer = digest("newer", "", now.plusSeconds(2000));
        Digests.Digest personal = digest("personal", "sam", now.plusSeconds(3000));
        store.add(older);
        store.add(newer);
        store.add(personal);

        assertEquals(newer, store.get(newer.id()));
        assertEquals(personal, store.get(personal.id()), "a personal digest can be read by id");
        List<String> ids = store.list(100).stream().map(Digests.Digest::id).toList();
        assertTrue(ids.indexOf(newer.id()) < ids.indexOf(older.id()), "newest first");
        assertTrue(!ids.contains(personal.id()), "personal digests are never listed");
        assertEquals(1, store.list(1).size());
        assertThrows(Digests.NotFoundException.class, () -> store.get("java-test-missing-" + run));
    }

    @Test
    void aDigestWrittenByTheGoServiceIsRead() throws Exception {
        DigestStores.Postgres store = new DigestStores.Postgres(source);
        String id = "java-test-" + run + "-go";
        String body = "{\"id\":\"" + id + "\",\"from\":\"0001-01-01T00:00:00Z\",\"to\":\"2099-11-10T06:02:53.2696195Z\",\"count\":1,"
                + "\"title\":\"1 notification held back\",\"summary\":\"s\",\"groups\":[{\"key\":\"github\",\"title\":\"github\",\"summary\":\"x\","
                + "\"count\":1,\"eventIds\":[\"e\"]}],\"highlights\":null,\"model\":\"heuristic-v1\",\"source\":\"heuristic\","
                + "\"createdBy\":\"schedule\",\"createdAt\":\"2099-11-10T06:02:53.2696195Z\"}";
        try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
            s.execute("INSERT INTO digests (id, window_end, body, created_at) VALUES ('" + id + "', '2099-11-10T06:02:53Z', '" + body
                    + "'::jsonb, '2099-11-10T06:02:53Z')");
        }
        Digests.Digest read = store.get(id);
        assertEquals("schedule", read.createdBy());
        assertEquals(Digests.BEGINNING, read.from());
        assertTrue(read.highlights().isEmpty());
        assertTrue(store.list(5).stream().anyMatch(d -> d.id().equals(id)));
    }
}
