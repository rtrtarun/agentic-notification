package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Feedback;
import com.agentpulse.notification.application.Overrides;
import com.zaxxer.hikari.HikariDataSource;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

import java.sql.Connection;
import java.sql.Statement;
import java.time.Duration;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/**
 * The overrides and feedback stores against a real PostgreSQL the Go service migrated. Set TEST_DATABASE_URL; without
 * it the tests are skipped. They use their own rows and remove them afterwards.
 */
class PostgresFeedbackStoresTest {
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
                s.execute("DELETE FROM overrides WHERE source LIKE 'java-test-%' OR repository LIKE 'java-test-%'");
                s.execute("DELETE FROM feedback WHERE event_id LIKE 'java-test-%'");
            }
            source.close();
        }
    }

    @Test
    void anOverrideIsSavedListedReplacedByScopeAndDeleted() {
        FeedbackStores.PostgresOverrides store = new FeedbackStores.PostgresOverrides(source);
        Instant now = Instant.now().truncatedTo(ChronoUnit.MICROS);
        String name = "java-test-" + run;
        Overrides.Override first = store.save(new Overrides.Override("id-" + run, name, "push", "", "never", "sam", "n", now));
        assertEquals("id-" + run, first.id());

        Overrides.Override replaced = store.save(new Overrides.Override("other-" + run, name.toUpperCase(), "PUSH", "", "always", "kim", "m", now.plusSeconds(60)));
        assertEquals("id-" + run, replaced.id(), "the same scope keeps its id");
        assertEquals(now, replaced.createdAt(), "and its creation time");

        Overrides.Override loaded = store.load().stream().filter(o -> o.id().equals("id-" + run)).findFirst().orElseThrow();
        assertEquals("always", loaded.mode());
        assertEquals("kim", loaded.createdBy());
        assertEquals("m", loaded.note());

        store.delete("id-" + run);
        assertTrue(store.load().stream().noneMatch(o -> o.id().equals("id-" + run)));
        assertThrows(Overrides.NotFoundException.class, () -> store.delete("id-" + run));
    }

    @Test
    void aServiceBuiltOnTheStoreDecidesFromWhatIsStored() {
        FeedbackStores.PostgresOverrides store = new FeedbackStores.PostgresOverrides(source);
        String name = "java-test-svc-" + run;
        Overrides.Service service = new Overrides.Service(store, java.time.Clock.systemUTC());
        Overrides.Override saved = service.upsert(new Overrides.Override("", name, "", "", "never", "sam", "", null));
        try {
            assertTrue(new Overrides.Service(store, java.time.Clock.systemUTC()).list().stream().anyMatch(o -> o.id().equals(saved.id())));
        } finally {
            service.delete(saved.id());
        }
    }

    @Test
    void feedbackIsAddedAndFilteredNewestFirst() {
        FeedbackStores.PostgresFeedback store = new FeedbackStores.PostgresFeedback(source);
        String event = "java-test-" + run;
        Instant base = Instant.now().truncatedTo(ChronoUnit.MICROS).minusSeconds(100);
        store.add(new Feedback.Entry("a-" + run, event, "sam", "opened", "", "", "", "github", "push", "acme/api", base));
        store.add(new Feedback.Entry("b-" + run, event, "kim", "reply", "", "noisy", "", "github", "push", "acme/api", base.plusSeconds(10)));
        store.add(new Feedback.Entry("c-" + run, event, "sam", "never", "type", "", "ov", "github", "push", "acme/api", base.plusSeconds(20)));

        var all = store.list(new Feedback.Filter(event, "", Instant.EPOCH, 10));
        assertEquals(3, all.size());
        assertEquals("c-" + run, all.get(0).id(), "newest first");
        assertEquals("ov", all.get(0).overrideId());
        assertEquals("noisy", all.get(1).reason());
        assertEquals(2, store.list(new Feedback.Filter(event, "sam", Instant.EPOCH, 10)).size());
        assertEquals(1, store.list(new Feedback.Filter(event, "", Instant.EPOCH, 1)).size());
        assertEquals(1, store.list(new Feedback.Filter(event, "", base.plusSeconds(15), 10)).size());
        assertEquals("acme/api", all.get(2).repository());
    }
}
