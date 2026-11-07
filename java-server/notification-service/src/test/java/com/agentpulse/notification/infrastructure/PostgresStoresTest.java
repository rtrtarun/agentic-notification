package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Delivery;
import com.agentpulse.notification.domain.Rule;
import com.zaxxer.hikari.HikariDataSource;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

import java.sql.Connection;
import java.sql.Statement;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/**
 * The rule store and the delivery queue against a real PostgreSQL the Go service migrated. Set TEST_DATABASE_URL
 * (see {@link PostgresEventRepositoryTest}); without it the tests are skipped. They use their own rows and leave the
 * shared rules table as they found it.
 */
class PostgresStoresTest {
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
                s.execute("DELETE FROM delivery_jobs WHERE event_id LIKE 'java-test-%'");
            }
            source.close();
        }
    }

    private Delivery.Job job(String suffix, String channel) {
        return Delivery.Job.pending("java-test-" + run + "-" + suffix, channel, "standard", "[STANDARD] github/push",
                "body text", "github", Instant.now().minusSeconds(5));
    }

    // ---- rules ---------------------------------------------------------------------------------------------------

    @Test
    void rulesRoundTripInOrderAndAreReadableByTheGoServiceShape() throws Exception {
        PostgresRuleStore store = new PostgresRuleStore(source);
        List<Rule> before = store.list();
        try {
            Rule a = new Rule("java-a-" + run, "advisory", "protects security", new Rule.Condition("github", "security_advisory", ""),
                    new Rule.Outcome("critical", "security vulnerability detected", false), false, true);
            Rule b = new Rule("java-b-" + run, "default", "", new Rule.Condition("github", "", "acme/*"),
                    new Rule.Outcome("standard", "default routing", true), true, false);
            store.replace(List.of(a, b));

            List<Rule> read = store.list();
            assertEquals(List.of(a, b), read);

            // The stored JSON is what Go's json.Unmarshal reads: empty strings and false flags are absent.
            try (Connection c = source.getConnection(); Statement s = c.createStatement();
                 var rs = s.executeQuery("SELECT body::text FROM policy_rules WHERE id = 'java-a-" + run + "'")) {
                assertTrue(rs.next());
                String body = rs.getString(1);
                assertTrue(body.contains("\"locked\": true"), body);
                assertFalse(body.contains("disabled"), body);
                assertFalse(body.contains("\"repository\""), body);
            }

            store.replace(List.of(b, a));
            assertEquals(List.of(b, a), store.list(), "the order is stored");
        } finally {
            store.replace(before);
        }
    }

    @Test
    void aStoredRuleUsingAFeatureTheJavaServiceLacksIsRefusedNotIgnored() throws Exception {
        PostgresRuleStore store = new PostgresRuleStore(source);
        List<Rule> before = store.list();
        try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
            store.replace(List.of());
            s.execute("INSERT INTO policy_rules (position, id, body) VALUES (0, 'java-go-" + run + "', "
                    + "'{\"id\":\"java-go-" + run + "\",\"name\":\"go rule\",\"when\":{\"payloadMatch\":[{\"field\":\"a\",\"op\":\"eq\",\"value\":1}]},"
                    + "\"then\":{\"attentionClass\":\"standard\",\"reason\":\"r\"}}'::jsonb)");
            var e = assertThrows(com.agentpulse.notification.application.Ports.PortException.class, store::list);
            assertTrue(e.getMessage().contains("does not support"), e.getMessage());
        } finally {
            store.replace(before);
        }
    }

    // ---- delivery queue ------------------------------------------------------------------------------------------

    @Test
    void enqueueIsIdempotentPerEventAndChannelAndAJobIsClaimedSentAndCounted() {
        PostgresDeliveryStore store = new PostgresDeliveryStore(source);
        Delivery.Job j = job("a", "slack");
        assertTrue(store.enqueue(j));
        assertFalse(store.enqueue(job("a", "slack")), "the same event and channel is never queued twice");
        assertTrue(store.enqueue(job("a", "email")), "another channel is its own job");

        Instant now = Instant.now();
        List<Delivery.Job> claimed = store.claim(now, Duration.ofMinutes(2), 1000).stream()
                .filter(c -> c.eventId().equals(j.eventId())).toList();
        assertEquals(2, claimed.size());
        assertTrue(claimed.stream().allMatch(c -> c.status().equals("sending") && c.attempts() == 1));
        assertEquals("body text", claimed.get(0).message());

        store.complete(claimed.get(0).id(), now);
        store.complete(claimed.get(1).id(), now);
        Delivery.Job done = store.list(1000).stream().filter(c -> c.id().equals(claimed.get(0).id())).findFirst().orElseThrow();
        assertEquals("sent", done.status());
        assertTrue(store.counts().get("sent") >= 2);
    }

    @Test
    void failuresRetryThenDieAndADeadJobCanBeRequeued() {
        PostgresDeliveryStore store = new PostgresDeliveryStore(source);
        Delivery.Job j = job("b", "slack");
        store.enqueue(j);
        Instant now = Instant.now();
        Delivery.Job claimed = store.claim(now, Duration.ofMinutes(2), 1000).stream()
                .filter(c -> c.eventId().equals(j.eventId())).findFirst().orElseThrow();

        store.fail(claimed.id(), "boom", now.plusSeconds(600), now);
        Delivery.Job retrying = find(store, claimed.id());
        assertEquals("pending", retrying.status());
        assertEquals("boom", retrying.lastError());
        assertTrue(store.claim(now, Duration.ofMinutes(2), 1000).stream().noneMatch(c -> c.id().equals(claimed.id())),
                "not due until the backoff has passed");

        store.fail(claimed.id(), "gave up", null, now);
        assertEquals("dead", find(store, claimed.id()).status());
        assertThrows(Delivery.NotFoundException.class, () -> store.requeue(claimed.id() + "-x", now));
    }

    @Test
    void requeueAndErrorsDistinguishMissingFromNotDead() {
        PostgresDeliveryStore store = new PostgresDeliveryStore(source);
        Delivery.Job j = job("c", "slack");
        store.enqueue(j);
        String id = store.list(1000).stream().filter(c -> c.eventId().equals(j.eventId())).findFirst().orElseThrow().id();
        assertThrows(Delivery.NotRetryableException.class, () -> store.requeue(id, Instant.now()), "pending is not dead");
        assertThrows(Delivery.NotFoundException.class, () -> store.requeue("missing-" + run, Instant.now()));
        assertThrows(Delivery.NotFoundException.class, () -> store.complete("missing-" + run, Instant.now()));

        store.fail(id, "x", null, Instant.now());
        store.requeue(id, Instant.now());
        Delivery.Job again = find(store, id);
        assertEquals("pending", again.status());
        assertEquals(0, again.attempts());
    }

    @Test
    void anInlineDeliveryIsLoggedAndReplacedNotDuplicated() {
        PostgresDeliveryStore store = new PostgresDeliveryStore(source);
        Instant now = Instant.now();
        String eventId = "java-test-" + run + "-d";
        Delivery.Job first = new Delivery.Job(Delivery.newId(), eventId, "webhook+email", "critical", "[CRITICAL] x", "",
                "github", "sent", 1, now, Instant.EPOCH, "", true, now, now);
        store.record(first);
        store.record(new Delivery.Job(Delivery.newId(), eventId, "webhook+email", "critical", "[CRITICAL] x", "",
                "github", "dead", 1, now, Instant.EPOCH, "channel down", true, now, now));

        List<Delivery.Job> mine = store.list(1000).stream().filter(c -> c.eventId().equals(eventId)).toList();
        assertEquals(1, mine.size());
        assertEquals("dead", mine.get(0).status());
        assertTrue(mine.get(0).direct());
        assertEquals(first.id(), mine.get(0).id(), "the first row is updated in place");
        assertTrue(store.claim(now.plusSeconds(60), Duration.ofMinutes(1), 1000).stream()
                .noneMatch(c -> c.eventId().equals(eventId)), "a logged delivery is never claimed");
    }

    @Test
    void twoWorkersNeverClaimTheSameJob() throws Exception {
        PostgresDeliveryStore store = new PostgresDeliveryStore(source);
        for (int i = 0; i < 20; i++) {
            store.enqueue(job("race-" + i, "slack"));
        }
        ExecutorService pool = Executors.newFixedThreadPool(4);
        try {
            Instant now = Instant.now();
            List<Future<List<Delivery.Job>>> results = new java.util.ArrayList<>();
            for (int w = 0; w < 4; w++) {
                results.add(pool.submit(() -> store.claim(now, Duration.ofMinutes(2), 8)));
            }
            java.util.Set<String> seen = new java.util.HashSet<>();
            for (Future<List<Delivery.Job>> f : results) {
                for (Delivery.Job c : f.get()) {
                    assertTrue(seen.add(c.id()), "job " + c.id() + " was handed to two workers");
                }
            }
        } finally {
            pool.shutdownNow();
        }
    }

    private static Delivery.Job find(PostgresDeliveryStore store, String id) {
        return store.list(1000).stream().filter(c -> c.id().equals(id)).findFirst().orElseThrow();
    }

    @Test
    void countsAreByStatus() {
        Map<String, Integer> counts = new PostgresDeliveryStore(source).counts();
        assertTrue(counts.values().stream().allMatch(n -> n > 0));
    }
}
