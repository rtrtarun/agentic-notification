package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Accounts.AccountException;
import com.agentpulse.notification.application.Accounts.AuditEntry;
import com.agentpulse.notification.application.Accounts.Session;
import com.agentpulse.notification.application.Accounts.User;
import com.zaxxer.hikari.HikariDataSource;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

import java.sql.Connection;
import java.sql.Statement;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/**
 * The account store against a real PostgreSQL the Go service migrated (including {@code users.status}). Set
 * TEST_DATABASE_URL; without it the tests are skipped. They use their own rows and remove them afterwards.
 */
class PostgresAccountStoreTest {
    private static HikariDataSource source;
    private static final String RUN = UUID.randomUUID().toString().substring(0, 8);
    private static final Instant NOW = Instant.now().truncatedTo(ChronoUnit.MILLIS);

    @BeforeAll
    static void connect() {
        String url = System.getenv("TEST_DATABASE_URL");
        assumeTrue(url != null && !url.isEmpty(), "TEST_DATABASE_URL not set; skipping the PostgreSQL tests");
        source = Database.connect(url);
        Database.waitForSchema(source, Duration.ofSeconds(10));
    }

    @AfterAll
    static void clean() throws Exception {
        if (source != null) {
            try (Connection c = source.getConnection(); Statement s = c.createStatement()) {
                s.execute("DELETE FROM users WHERE email LIKE '%@java-" + RUN + ".test'");
                s.execute("DELETE FROM audit_log WHERE actor LIKE '%@java-" + RUN + ".test'");
            }
            source.close();
        }
    }

    private static User user(String name, String status) {
        return new User("java-" + RUN + "-" + name, name + "@java-" + RUN + ".test", name, "member", "hash", false, status, NOW);
    }

    @Test
    void usersKeepTheirStatusAndApprovalPersists() {
        PostgresAccountStore store = new PostgresAccountStore(source);
        User pending = user("pending", Accounts.STATUS_PENDING);
        store.createUser(pending);
        User read = store.userByEmail(pending.email()).orElseThrow();
        assertEquals(Accounts.STATUS_PENDING, read.status());
        assertFalse(read.active());
        assertEquals(pending.id(), store.userById(pending.id()).orElseThrow().id());

        store.updateUser(new User(read.id(), read.email(), "Renamed", read.role(), read.passwordHash(), false, Accounts.STATUS_ACTIVE, read.createdAt()));
        User approved = store.userById(pending.id()).orElseThrow();
        assertTrue(approved.active());
        assertEquals("Renamed", approved.name());
        assertTrue(store.listUsers().stream().anyMatch(u -> u.id().equals(pending.id())));
    }

    @Test
    void anEmptyStatusIsStoredAsActiveAndADuplicateEmailIsRefused() {
        PostgresAccountStore store = new PostgresAccountStore(source);
        User legacy = user("legacy", "");
        store.createUser(legacy);
        assertEquals(Accounts.STATUS_ACTIVE, store.userById(legacy.id()).orElseThrow().status());
        AccountException dup = assertThrows(AccountException.class, () -> store.createUser(new User("other-" + RUN, legacy.email(), "", "member", "", false, "active", NOW)));
        assertEquals(AccountException.Kind.EMAIL_TAKEN, dup.kind());
        assertThrows(AccountException.class, () -> store.updateUser(user("ghost", "active")));
    }

    @Test
    void sessionsCanBeFoundAndEndedAndAuditIsNewestFirst() {
        PostgresAccountStore store = new PostgresAccountStore(source);
        User member = user("session", Accounts.STATUS_ACTIVE);
        store.createUser(member);
        Session live = new Session(Accounts.hashToken("live-" + RUN), member.id(), "csrf", NOW.plus(1, ChronoUnit.HOURS), NOW);
        Session old = new Session(Accounts.hashToken("old-" + RUN), member.id(), "csrf", NOW.minus(1, ChronoUnit.HOURS), NOW.minus(2, ChronoUnit.HOURS));
        store.createSession(live);
        store.createSession(old);
        assertTrue(store.sessionByHash(live.hash()).isPresent());
        store.deleteExpiredSessions(NOW);
        assertTrue(store.sessionByHash(old.hash()).isEmpty());
        store.deleteSession(live.hash());
        assertTrue(store.sessionByHash(live.hash()).isEmpty());
        store.createSession(live);
        store.deleteUserSessions(member.id());
        assertTrue(store.sessionByHash(live.hash()).isEmpty());

        store.addAudit(new AuditEntry(NOW, member.email(), "first", "", "", ""));
        store.addAudit(new AuditEntry(NOW.plusSeconds(1), member.email(), "second", "t", "1.2.3.4", "d"));
        assertEquals("second", store.listAudit(2).get(0).action());
        assertTrue(store.countUsers() >= 1);
        assertTrue(store.countActiveAdmins() >= 0);
    }

    @Test
    void aWholeSignUpAndApprovalWorksAgainstTheDatabase() {
        PostgresAccountStore store = new PostgresAccountStore(source);
        Accounts.Service service = new Accounts.Service(store, Duration.ofHours(1), Clock.systemUTC()).withSignup(true);
        String email = "flow@java-" + RUN + ".test";
        service.signUp(email, "Flow", "correct horse battery", "10.0.0.1");
        assertEquals(Accounts.AccountException.Kind.PENDING_APPROVAL,
                assertThrows(AccountException.class, () -> service.login(email, "correct horse battery", "10.0.0.1")).kind());
        User u = store.userByEmail(email).orElseThrow();
        service.updateUser("tester@java-" + RUN + ".test", u.id(), Accounts.Patch.none().withStatus(Accounts.STATUS_ACTIVE));
        assertTrue(service.login(email, "correct horse battery", "10.0.0.1").user().active());
    }
}
