package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
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
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Settings, the in-memory store, the delivery providers and the database URL handling. */
class InfrastructureTest {

    @Test
    void emptySettingsFailClosedAndHaveSafeDefaults() {
        Config c = Config.load(Map.of());
        assertEquals(8080, c.port());
        assertEquals("", c.apiKey());
        assertFalse(c.allowUnauthenticated());
        assertFalse(c.allowUnsignedWebhooks());
        assertEquals(Duration.ZERO, c.dedupWindow());
        assertEquals(Config.DEFAULT_MAX_BODY_BYTES, c.maxBodyBytes());
        assertEquals(587, c.smtpPort());
        assertTrue(c.emailRecipients().isEmpty());
    }

    @Test
    void settingsAreReadFromTheSameNamesAsTheGoService() {
        Config c = Config.load(Map.of("PORT", "9000", "API_KEY", " k ", "ALLOW_UNAUTHENTICATED_API", "true",
                "GITHUB_WEBHOOK_SECRET", "s", "ALLOW_UNSIGNED_WEBHOOKS", "false", "DEDUP_WINDOW", "90s",
                "DEFAULT_NOTIFY_EMAIL", "a@x.com, b@x.com ,", "SMTP_REQUIRE_STARTTLS", "TRUE"));
        assertEquals(9000, c.port());
        assertEquals("k", c.apiKey());
        assertTrue(c.allowUnauthenticated());
        assertEquals(Duration.ofSeconds(90), c.dedupWindow());
        assertEquals(List.of("a@x.com", "b@x.com"), c.emailRecipients());
        assertTrue(c.smtpRequireStartTls());
        assertEquals(Duration.ofMinutes(10), Config.parseGoDuration("10m"));
        assertEquals(Duration.ofHours(2), Config.parseGoDuration("2h"));
    }

    @Test
    void badSettingsAreRefusedWithTheirName() {
        for (Map<String, String> env : List.of(Map.of("PORT", "0"), Map.of("PORT", "x"), Map.of("ALLOW_UNAUTHENTICATED_API", "yes"),
                Map.of("DEDUP_WINDOW", "soon"), Map.of("DEDUP_WINDOW", "-5m"), Map.of("DEDUP_WINDOW", "5d"))) {
            assertThrows(Config.InvalidConfigException.class, () -> Config.load(env), env.toString());
        }
    }

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

    @Test
    void theLogProviderNeverFails() {
        Providers.log().send(new Notification("t", "m", "g"));
        assertEquals("log", Providers.log().name());
    }

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
    void aCriticalNotificationSucceedsWhenAnyChannelTakesIt() {
        List<String> calls = new ArrayList<>();
        NotificationProvider all = Providers.all(List.of(stub("email", true, calls), stub("slack", false, calls)));
        all.send(new Notification("t", "m", "g"));
        assertEquals(List.of("email", "slack"), calls, "a failing channel does not stop the others");
        assertEquals("email,slack", all.name());
    }

    @Test
    void itFailsOnlyWhenEveryChannelFailed() {
        List<String> calls = new ArrayList<>();
        NotificationProvider all = Providers.all(List.of(stub("email", true, calls), stub("slack", true, calls)));
        PortException e = assertThrows(PortException.class, () -> all.send(new Notification("t", "m", "g")));
        assertTrue(e.getMessage().contains("email down") && e.getMessage().contains("slack down"));
    }

    @Test
    void slackPostsTheMessageAndReportsErrors() throws Exception {
        AtomicReference<String> received = new AtomicReference<>();
        int[] status = {200};
        HttpServer fake = HttpServer.create(new InetSocketAddress("localhost", 0), 0);
        fake.createContext("/hook", x -> {
            received.set(new String(x.getRequestBody().readAllBytes()));
            x.sendResponseHeaders(status[0], -1);
            x.close();
        });
        fake.start();
        try {
            NotificationProvider slack = Providers.slack("http://localhost:" + fake.getAddress().getPort() + "/hook");
            slack.send(new Notification("[CRITICAL] github/security_advisory", "Reason: x", "github"));
            assertTrue(received.get().contains("[CRITICAL] github/security_advisory"));
            assertEquals("slack", slack.name());

            status[0] = 500;
            PortException e = assertThrows(PortException.class, () -> slack.send(new Notification("t", "m", "g")));
            assertTrue(e.getMessage().contains("500"));
        } finally {
            fake.stop(0);
        }
    }

    @Test
    void emailFailsWithAPortExceptionWhenTheServerIsUnreachable() {
        NotificationProvider email = Providers.email(new Providers.Smtp("localhost", 1, "", "", "from@x.com",
                List.of("to@x.com"), false));
        assertEquals("email", email.name());
        assertThrows(PortException.class, () -> email.send(new Notification("t", "m", "g")));
    }

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
