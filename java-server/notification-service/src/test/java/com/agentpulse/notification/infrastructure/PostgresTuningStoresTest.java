package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Tuning;
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
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/**
 * The adjustment and run stores against a real PostgreSQL the Go service migrated, including rows written the way
 * Go writes them. Set TEST_DATABASE_URL; without it the tests are skipped. They use their own rows and remove them
 * afterwards. The runs table allows one running run at a time, so these tests share one lock.
 */
class PostgresTuningStoresTest {
    private static final Object RUNS = new Object();
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
                s.execute("DELETE FROM learning_adjustments WHERE id LIKE 'java-test-%'");
                s.execute("DELETE FROM learning_runs WHERE id LIKE 'java-test-%'");
            }
            source.close();
        }
    }

    private Tuning.Adjustment adjustment(String suffix, String type, String repository, String status, int version, Instant updated) {
        boolean resolved = !status.equals("proposed");
        return new Tuning.Adjustment("java-test-" + run + "-" + suffix, "java-test-" + run, type, repository, -14, status,
                new Tuning.Evidence(0, 5, 2, 5, 30, "5 of 5 signals from 2 people were negative"), new Tuning.Impact(9, 4), version,
                updated.minusSeconds(60), updated, resolved ? "admin" : "", resolved ? updated : null);
    }

    @Test
    void anAdjustmentIsSavedReadBackReplacedAndListedByStatusNewestFirst() {
        TuningStores.PostgresAdjustments store = new TuningStores.PostgresAdjustments(source);
        Instant now = Instant.now().truncatedTo(ChronoUnit.MICROS);
        Tuning.Adjustment older = adjustment("older", "star", "", "proposed", 0, now.plusSeconds(1000));
        Tuning.Adjustment newer = adjustment("newer", "push", "acme/web", "proposed", 0, now.plusSeconds(2000));
        Tuning.Adjustment active = adjustment("active", "issues", "", "active", 900_000, now.plusSeconds(3000));
        store.save(older);
        store.save(newer);
        store.save(active);

        assertEquals(newer, store.get(newer.id()));
        assertEquals(active, store.get(active.id()));
        List<String> proposed = store.list("proposed").stream().map(Tuning.Adjustment::id).toList();
        assertTrue(proposed.indexOf(newer.id()) < proposed.indexOf(older.id()), "newest update first");
        assertTrue(!proposed.contains(active.id()));
        assertTrue(store.list("active").stream().anyMatch(a -> a.id().equals(active.id())));
        assertTrue(store.list("").stream().anyMatch(a -> a.id().equals(active.id())), "an empty status means all");
        assertTrue(store.maxVersion() >= 900_000);

        Tuning.Adjustment retired = adjustment("older", "star", "", "retired", 900_001, now.plusSeconds(4000));
        store.save(retired);
        Tuning.Adjustment expected = new Tuning.Adjustment(retired.id(), retired.source(), retired.eventType(), retired.repository(), retired.delta(),
                retired.status(), retired.evidence(), retired.impact(), retired.version(), older.createdAt(), retired.updatedAt(), retired.resolvedBy(), retired.resolvedAt());
        assertEquals(expected, store.get(older.id()), "saving again replaces it and keeps the creation time");
        assertEquals(900_001, store.maxVersion());
        assertThrows(Tuning.NotFoundException.class, () -> store.get("java-test-missing-" + run));
    }

    @Test
    void anAdjustmentWrittenByTheGoServiceIsRead() throws Exception {
        TuningStores.PostgresAdjustments store = new TuningStores.PostgresAdjustments(source);
        String id = "java-test-" + run + "-go";
        try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
            s.execute("INSERT INTO learning_adjustments (id, scope_key, source, event_type, repository, delta, status, evidence, impact, version, "
                    + "created_at, updated_at, resolved_by, resolved_at) VALUES ('" + id + "', 'java-test-" + run + "\u001fpush\u001f', 'java-test-" + run
                    + "', 'push', '', 12, 'active', '{\"positive\":6,\"negative\":0,\"people\":3,\"signals\":6,\"windowDays\":30,"
                    + "\"summary\":\"6 of 6 signals from 3 people were positive\"}'::jsonb, '{\"replayed\":4,\"wouldChange\":0}'::jsonb, 3, "
                    + "'2099-11-10T06:02:53Z', '2099-11-10T07:02:53Z', 'admin@corp.com', '2099-11-10T07:02:53Z')");
        }
        Tuning.Adjustment read = store.get(id);
        assertEquals(12, read.delta());
        assertEquals("active", read.status());
        assertEquals(3, read.version());
        assertEquals(new Tuning.Evidence(6, 0, 3, 6, 30, "6 of 6 signals from 3 people were positive"), read.evidence());
        assertEquals(new Tuning.Impact(4, 0), read.impact());
        assertEquals("", read.repository());
        assertEquals("admin@corp.com", read.resolvedBy());
        assertNotNull(read.resolvedAt());
        assertTrue(store.list("active").stream().anyMatch(a -> a.id().equals(id)));
    }

    @Test
    void aJavaProposalCarriesTheScopeKeyGoLooksFor() throws Exception {
        TuningStores.PostgresAdjustments store = new TuningStores.PostgresAdjustments(source);
        Tuning.Adjustment a = adjustment("scope", "Pull_Request", "", "proposed", 0, Instant.now().truncatedTo(ChronoUnit.MICROS));
        store.save(a);
        try (Connection c = source.getConnection(); Statement s = c.createStatement();
             java.sql.ResultSet rs = s.executeQuery("SELECT scope_key FROM learning_adjustments WHERE id = '" + a.id() + "'")) {
            assertTrue(rs.next());
            assertEquals(a.source().toLowerCase() + "\u001fpull_request\u001f", rs.getString(1), "lower-cased source, event type and repository, as Go keeps it");
        }
    }

    @Test
    void theRunLogKeepsOneRunningRunAndAbandonsOldOnes() {
        synchronized (RUNS) {
            TuningStores.PostgresRuns runs = new TuningStores.PostgresRuns(source);
            try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
                s.execute("DELETE FROM learning_runs");
            } catch (Exception e) {
                throw new IllegalStateException(e);
            }
            Instant base = Instant.now().truncatedTo(ChronoUnit.MICROS);
            Tuning.Run first = new Tuning.Run("java-test-" + run + "-1", "running", base, null, 0, 0, 0, "");
            runs.start(first, base.minus(Tuning.STALE_RUN_AFTER));
            assertThrows(Tuning.RunInProgressException.class,
                    () -> runs.start(new Tuning.Run("java-test-" + run + "-2", "running", base.plusSeconds(1), null, 0, 0, 0, ""),
                            base.plusSeconds(1).minus(Tuning.STALE_RUN_AFTER)), "only one run may be running");

            Instant end = base.plusSeconds(60);
            runs.finish(new Tuning.Run(first.id(), "done", base, end, 40, 900, 2, ""));
            Tuning.Run second = new Tuning.Run("java-test-" + run + "-2", "running", base.plusSeconds(3600), null, 0, 0, 0, "");
            runs.start(second, base.plusSeconds(3600).minus(Tuning.STALE_RUN_AFTER));
            runs.finish(new Tuning.Run(second.id(), "failed", second.startedAt(), end, 0, 0, 0, "read feedback"));

            List<Tuning.Run> list = runs.list(10);
            assertEquals(2, list.size());
            assertEquals(second.id(), list.get(0).id(), "newest first");
            assertEquals(new Tuning.Run(first.id(), "done", base, end, 40, 900, 2, ""), list.get(1));
            assertEquals("read feedback", list.get(0).error());
            assertEquals(1, runs.list(1).size());

            Tuning.Run stuck = new Tuning.Run("java-test-" + run + "-3", "running", base.plusSeconds(7200), null, 0, 0, 0, "");
            runs.start(stuck, base.plusSeconds(7200).minus(Tuning.STALE_RUN_AFTER));
            Instant later = base.plusSeconds(7200).plus(Tuning.STALE_RUN_AFTER).plusSeconds(60);
            runs.start(new Tuning.Run("java-test-" + run + "-4", "running", later, null, 0, 0, 0, ""), later.minus(Tuning.STALE_RUN_AFTER));
            Tuning.Run abandoned = runs.list(10).stream().filter(r -> r.id().equals(stuck.id())).findFirst().orElseThrow();
            assertEquals("failed", abandoned.status());
            assertEquals(Tuning.ABANDONED_RUN_TEXT, abandoned.error());
            assertNotNull(abandoned.finishedAt());
        }
    }
}
