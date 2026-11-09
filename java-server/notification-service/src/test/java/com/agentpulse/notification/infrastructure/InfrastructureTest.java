package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Dedup;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.application.Targets;
import com.agentpulse.notification.domain.Event;
import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.Test;

import java.net.InetSocketAddress;
import java.net.URI;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Settings, the in-memory store, the delivery providers, Redis dedup and the database URL handling. */
class InfrastructureTest {

    private static Notification note(Targets targets) {
        return new Notification("[CRITICAL] github/security_advisory", "Reason: x", "agent-1", "owner", "grp", targets);
    }

    // ---- settings ---------------------------------------------------------------------------------------------

    @Test
    void emptySettingsFailClosedAndHaveSafeDefaults() {
        Config c = Config.load(Map.of());
        assertEquals(8080, c.port());
        assertEquals("", c.apiKey());
        assertFalse(c.allowUnauthenticated());
        assertFalse(c.allowUnsignedWebhooks());
        assertTrue(c.dispatcherEnabled());
        assertFalse(Config.load(Map.of("DISPATCHER_ENABLED", "false")).dispatcherEnabled());
        assertThrows(Config.InvalidConfigException.class, () -> Config.load(Map.of("DISPATCHER_ENABLED", "maybe")));
        assertEquals(Duration.ZERO, c.dedupWindow());
        assertEquals(Duration.ofDays(90), c.eventRetention());
        assertEquals(Config.DEFAULT_MAX_BODY_BYTES, c.maxBodyBytes());
        assertEquals(587, c.smtpPort());
        assertTrue(c.emailRecipients().isEmpty());
        assertTrue(c.targets().channels().isEmpty(), "nothing configured means no channel");
        assertTrue(c.allowedWebhookHosts().contains("hooks.slack.com"));
        assertTrue(c.standardChannels().isEmpty());
        assertEquals("", c.redisUrl());
    }

    @Test
    void settingsAreReadFromTheSameNamesAsTheGoService() {
        Map<String, String> env = new HashMap<>();
        env.putAll(Map.of("PORT", "9000", "API_KEY", " k ", "ALLOW_UNAUTHENTICATED_API", "true",
                "GITHUB_WEBHOOK_SECRET", "s", "ALLOW_UNSIGNED_WEBHOOKS", "false", "DEDUP_WINDOW", "90s",
                "DEFAULT_NOTIFY_EMAIL", "a@x.com, b@x.com ,", "SMTP_REQUIRE_STARTTLS", "TRUE"));
        env.putAll(Map.of("REDIS_URL", "redis://r:6379/0", "EVENT_RETENTION", "0s", "SMTP_HOST", "smtp.x.com",
                "SMTP_SENDER", "from@x.com", "SLACK_WEBHOOK_URL", "https://hooks.example.org/services/T/B/x",
                "NOTIFY_STANDARD_CHANNELS", "slack, email", "NOTIFY_BACKGROUND_CHANNELS", "email"));
        Config c = Config.load(env);
        assertEquals(9000, c.port());
        assertEquals("k", c.apiKey());
        assertTrue(c.allowUnauthenticated());
        assertEquals(Duration.ofSeconds(90), c.dedupWindow());
        assertEquals(Duration.ZERO, c.eventRetention(), "retention may be switched off with 0");
        assertEquals(List.of("a@x.com", "b@x.com"), c.emailRecipients());
        assertTrue(c.smtpRequireStartTls());
        assertEquals(List.of("slack", "email"), c.standardChannels());
        assertEquals(List.of("email"), c.backgroundChannels());
        assertEquals(List.of("email", "slack"), c.targets().channels());
        assertTrue(c.webhookHostAllowList().contains("hooks.example.org"), "the operator's own webhook host is allowed");
        assertEquals(Duration.ofMinutes(10), Config.parseGoDuration("10m"));
        assertEquals(Duration.ofHours(2), Config.parseGoDuration("2h"));
        assertEquals(Duration.ofMinutes(90), Config.parseGoDuration("1h30m"));
    }

    @Test
    void emailIsAChannelOnlyWhenSmtpIsConfigured() {
        Config withoutSmtp = Config.load(Map.of("DEFAULT_NOTIFY_EMAIL", "a@x.com"));
        assertTrue(withoutSmtp.targets().email().isEmpty());
        Config withSmtp = Config.load(Map.of("DEFAULT_NOTIFY_EMAIL", "a@x.com", "SMTP_HOST", "h", "SMTP_SENDER", "s@x.com"));
        assertEquals(List.of("a@x.com"), withSmtp.targets().email());
    }

    @Test
    void badSettingsAreRefusedWithTheirName() {
        for (Map<String, String> env : List.of(Map.of("PORT", "0"), Map.of("PORT", "x"), Map.of("ALLOW_UNAUTHENTICATED_API", "yes"),
                Map.of("DEDUP_WINDOW", "soon"), Map.of("DEDUP_WINDOW", "-5m"), Map.of("DEDUP_WINDOW", "5d"),
                Map.of("DEDUP_WINDOW", "0s"), Map.of("EVENT_RETENTION", "x1h"), Map.of("NOTIFY_STANDARD_CHANNELS", "pager"))) {
            assertThrows(Config.InvalidConfigException.class, () -> Config.load(env), env.toString());
        }
    }

    @Test
    void channelsParseAndOnlyKnownNamesAreAccepted() {
        assertEquals(List.of("slack", "teams"), Targets.parseChannels(List.of(" Slack ", "", "TEAMS")));
        assertThrows(IllegalArgumentException.class, () -> Targets.parseChannels(List.of("sms")));
        Targets t = new Targets(List.of("a@x.com"), List.of("https://hooks.slack.com/x"), null);
        assertEquals(List.of("a@x.com"), t.forChannel("email"));
        assertTrue(t.forChannel("pager").isEmpty());
        assertTrue(t.only("slack").email().isEmpty());
        assertEquals(List.of("https://hooks.slack.com/x"), t.only("slack").slack());
    }

    // ---- in-memory event store ----------------------------------------------------------------------------------

    @Test
    void theInMemoryStoreIsIdempotentAndListsNewestFirst() {
        Clock clock = new Clock() {
            private Instant now = Instant.parse("2026-01-01T00:00:00Z");
            @Override public ZoneOffset getZone() { return ZoneOffset.UTC; }
            @Override public Clock withZone(java.time.ZoneId z) { return this; }
            @Override public synchronized Instant instant() { now = now.plusSeconds(1); return now; }
        };
        InMemoryEventRepository repo = new InMemoryEventRepository(clock);
        repo.save(new Event("a", "github", "push", "", null));
        repo.save(new Event("b", "github", "push", "", null));
        repo.save(new Event("a", "github", "other", "", null));

        assertTrue(repo.exists("a"));
        assertFalse(repo.exists("z"));
        List<Event> events = repo.listRecent(10);
        assertEquals(List.of("b", "a"), events.stream().map(Event::getId).toList());
        assertEquals("push", events.get(1).getEventType(), "a second save of the same id changes nothing");
        assertEquals(1, repo.listRecent(1).size());
    }

    // ---- providers ---------------------------------------------------------------------------------------------

    private static NotificationProvider stub(String name, boolean fail, List<String> calls) {
        return new NotificationProvider() {
            @Override public void send(Notification n) {
                calls.add(name);
                if (fail) {
                    throw new PortException(name + " down", null);
                }
            }
            @Override public String name() { return name; }
        };
    }

    @Test
    void everyChannelIsAttemptedEvenWhenOneFailsAndThenTheFailureIsReported() {
        List<String> calls = new ArrayList<>();
        NotificationProvider all = Providers.all(List.of(stub("email", true, calls), stub("webhook", false, calls)));
        PortException e = assertThrows(PortException.class, () -> all.send(note(Targets.none())));
        assertEquals(List.of("email", "webhook"), calls, "a failing channel does not stop the others");
        assertTrue(e.getMessage().contains("email down") && !e.getMessage().contains("webhook down"));
        assertEquals("email+webhook", all.name());
    }

    @Test
    void whenNothingFailsTheCompositeSucceeds() {
        List<String> calls = new ArrayList<>();
        Providers.all(List.of(stub("a", false, calls), stub("b", false, calls))).send(note(Targets.none()));
        assertEquals(List.of("a", "b"), calls);
    }

    @Test
    void theWebhookProviderIsANoOpWithoutUrlsAndPostsToEveryUrl() throws Exception {
        List<String> received = new ArrayList<>();
        int[] status = {200};
        HttpServer fake = HttpServer.create(new InetSocketAddress("localhost", 0), 0);
        fake.createContext("/", x -> {
            received.add(x.getRequestURI().getPath() + " " + new String(x.getRequestBody().readAllBytes()));
            x.sendResponseHeaders(status[0], -1);
            x.close();
        });
        fake.start();
        try {
            String base = "http://localhost:" + fake.getAddress().getPort();
            NotificationProvider webhook = Providers.webhook(false);
            webhook.send(note(Targets.none()));
            assertTrue(received.isEmpty(), "nothing to post to");

            webhook.send(note(new Targets(null, List.of(base + "/slack"), List.of(base + "/teams"))));
            assertEquals(2, received.size());
            assertTrue(received.get(0).startsWith("/slack ") && received.get(0).contains("Reason: x"));
            assertTrue(received.get(0).contains("Agent: agent-1") && received.get(0).contains("Group: grp"));
            assertTrue(received.get(1).startsWith("/teams "));
            assertEquals("webhook", webhook.name());

            status[0] = 500;
            PortException e = assertThrows(PortException.class,
                    () -> webhook.send(note(new Targets(null, List.of(base + "/a", base + "/b"), null))));
            assertTrue(e.getMessage().contains("500"));
            assertEquals(2, received.size() - 2, "a failing URL does not stop the next one");
        } finally {
            fake.stop(0);
        }
    }

    @Test
    void theWebhookProviderRefusesPrivateAddressesWhenAskedTo() {
        NotificationProvider strict = Providers.webhook(true);
        PortException e = assertThrows(PortException.class,
                () -> strict.send(note(new Targets(null, List.of("https://localhost/hook"), null))));
        assertTrue(e.getMessage().contains("blocked address"), e.getMessage());
        assertThrows(PortException.class, () -> strict.send(note(new Targets(null, List.of("https://127.0.0.1/x"), null))));
        assertThrows(PortException.class, () -> strict.send(note(new Targets(null, List.of("https://192.168.1.10/x"), null))));
        assertThrows(PortException.class, () -> strict.send(note(new Targets(null, List.of("https://169.254.169.254/x"), null))));
    }

    @Test
    void anUnreachableWebhookNeverLeaksItsPathInTheError() {
        NotificationProvider webhook = Providers.webhook(false);
        PortException e = assertThrows(PortException.class,
                () -> webhook.send(note(new Targets(null, List.of("http://localhost:1/services/SECRET/PATH"), null))));
        assertFalse(e.getMessage().contains("SECRET"), e.getMessage());
    }

    @Test
    void emailDoesNothingWithoutRecipientsAndFailsWithAPortExceptionWhenTheServerIsUnreachable() {
        NotificationProvider email = Providers.email(new Providers.Smtp("localhost", 1, "", "", "from@x.com", false));
        assertEquals("email", email.name());
        email.send(note(Targets.none()));
        assertThrows(PortException.class, () -> email.send(note(new Targets(List.of("to@x.com"), null, null))));
    }

    // ---- Redis dedup (against a fake key store; the real Redis runs in CI) -------------------------------------

    /** A key store that behaves like Redis SET NX, GET and a delete-if-equal script. */
    private static final class FakeRedis implements RedisDeduper.KeyStore {
        final Map<String, String> data = new HashMap<>();
        boolean down;

        @Override public boolean setIfAbsent(String key, String value, Duration ttl) {
            check();
            return data.putIfAbsent(key, value) == null;
        }
        @Override public Optional<String> get(String key) {
            check();
            return Optional.ofNullable(data.get(key));
        }
        @Override public void deleteIfEquals(String key, String value) {
            check();
            data.remove(key, value);
        }
        private void check() {
            if (down) {
                throw new IllegalStateException("connection refused");
            }
        }
    }

    private static Event pr(String id) {
        return new Event(id, "github", "pull_request", "acme/api", new HashMap<>(Map.of("action", "opened", "number", 5)));
    }

    @Test
    void redisDedupMergesAcrossReplicasThatShareTheStore() {
        FakeRedis shared = new FakeRedis();
        RedisDeduper replicaA = new RedisDeduper(shared, Duration.ofMinutes(10));
        RedisDeduper replicaB = new RedisDeduper(shared, Duration.ofMinutes(10));

        assertFalse(replicaA.check(pr("e1")).duplicate());
        Dedup.Result repeat = replicaB.check(pr("e2"));
        assertTrue(repeat.duplicate(), "the second replica sees what the first recorded");
        assertEquals("e1", repeat.originalId());
        assertTrue(shared.data.keySet().iterator().next().startsWith("dedup:"));
    }

    @Test
    void forgetOnlyRemovesTheKeyTheSameEventCreated() {
        FakeRedis store = new FakeRedis();
        RedisDeduper d = new RedisDeduper(store, null);
        Event first = pr("e1");
        d.check(first);
        d.forget(Dedup.dedupKey(first), "someone-else");
        assertTrue(d.check(pr("e2")).duplicate());
        d.forget(Dedup.dedupKey(first), "e1");
        assertFalse(d.check(pr("e3")).duplicate());
    }

    @Test
    void whenRedisIsDownTheDeduperReportsTheFailureSoThePipelineCanFailOpen() {
        FakeRedis store = new FakeRedis();
        store.down = true;
        RedisDeduper d = new RedisDeduper(store, Duration.ofMinutes(1));
        assertThrows(PortException.class, () -> d.check(pr("e1")));
        assertThrows(PortException.class, () -> d.forget("k", "e1"));
    }

    // ---- database url ---------------------------------------------------------------------------------------------

    @Test
    void theDatabaseUrlBecomesAJdbcUrl() {
        assertEquals("jdbc:postgresql://db:5432/attention?sslmode=disable",
                Database.jdbcUrl(URI.create("postgres://u:p@db:5432/attention?sslmode=disable")));
        assertEquals("jdbc:postgresql://db:5432/attention", Database.jdbcUrl(URI.create("postgres://db/attention")));
    }

    @Test
    void anUnusableDatabaseUrlOrMissingSchemaIsReportedClearly() {
        assertThrows(PortException.class, () -> Database.connect("not a url"));
        assertThrows(PortException.class, () -> Database.connect("postgres:///nohost"));
        try (var source = Database.connect("postgres://u:p@localhost:1/none")) {
            PortException e = assertThrows(PortException.class, () -> Database.waitForSchema(source, Duration.ofMillis(10)));
            assertTrue(e.getMessage().contains("Go service creates the schema"));
        }
    }
}
