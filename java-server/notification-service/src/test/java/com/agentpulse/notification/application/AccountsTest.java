package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Accounts.AccountException;
import com.agentpulse.notification.application.Accounts.AccountException.Kind;
import com.agentpulse.notification.application.Accounts.Patch;
import com.agentpulse.notification.application.Accounts.User;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.MemoryAccountStore;
import org.junit.jupiter.api.Test;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Accounts, sign-in, self sign-up with admin approval, and the password hash shared with the Go service. */
class AccountsTest {
    private static final String PASSWORD = "correct horse battery";
    /** Made by the Go service's HashPassword for PASSWORD: an account created there must sign in here. */
    private static final String GO_HASH = "argon2id$m=19456,t=2,p=1$PKIJsky8jG5YOPvizPgBJQ$9Hut2PVhaFJWvDvTP6hBxlIEbfPmnl3SekuhHXQEQ8s";

    /** A clock the test can move. */
    private static final class MovingClock extends Clock {
        private Instant now = Instant.parse("2026-06-01T09:00:00Z");

        @Override public java.time.ZoneId getZone() { return ZoneOffset.UTC; }
        @Override public Clock withZone(java.time.ZoneId zone) { return this; }
        @Override public Instant instant() { return now; }
        void advance(Duration d) { now = now.plus(d); }
    }

    private final MovingClock clock = new MovingClock();
    private final MemoryAccountStore store = new MemoryAccountStore();
    private final Accounts.Service service = new Accounts.Service(store, Duration.ofHours(1), clock);

    private static Kind kindOf(Runnable r) {
        return assertThrows(AccountException.class, r::run).kind();
    }

    // ---- the password hash ----------------------------------------------------------------------------------

    @Test
    void aHashMadeByTheGoServiceVerifiesHere() {
        assertTrue(PasswordHasher.verify(GO_HASH, PASSWORD));
        assertFalse(PasswordHasher.verify(GO_HASH, PASSWORD + "x"));
    }

    @Test
    void hashesRoundTripUseFreshSaltsAndNeverContainThePassword() {
        String a = PasswordHasher.hash(PASSWORD);
        String b = PasswordHasher.hash(PASSWORD);
        assertTrue(a.startsWith("argon2id$m=19456,t=2,p=1$"));
        assertFalse(a.contains(PASSWORD));
        assertNotEquals(a, b, "each hash needs its own salt");
        assertTrue(PasswordHasher.verify(a, PASSWORD));
        assertFalse(PasswordHasher.verify(a, "another long password"));
    }

    @Test
    void aMalformedHashNeverVerifies() {
        for (String bad : new String[] {"", "plain", "bcrypt$m=1,t=1,p=1$YQ$YQ", "argon2id$m=0,t=0,p=0$YQ$YQ",
                "argon2id$m=1,t=1,p=1$!!$!!", "argon2id$m=99999999,t=1,p=1$YQ$YQ", "argon2id$x$YQ$YQ", "argon2id$m=1,t=1,p=1$YQ"}) {
            assertFalse(PasswordHasher.verify(bad, PASSWORD), bad);
        }
        assertFalse(PasswordHasher.verify(null, PASSWORD));
        assertFalse(PasswordHasher.verify(GO_HASH, null));
        assertFalse(PasswordHasher.verify(GO_HASH, "x".repeat(300)));
    }

    @Test
    void passwordLengthRulesMatchTheGoService() {
        assertFalse(PasswordHasher.acceptable(null));
        assertFalse(PasswordHasher.acceptable("short"));
        assertFalse(PasswordHasher.acceptable("x".repeat(300)));
        assertTrue(PasswordHasher.acceptable(PASSWORD));
        assertTrue(PasswordHasher.acceptable("a".repeat(12)));
    }

    // ---- bootstrap and sign-in ------------------------------------------------------------------------------

    @Test
    void bootstrapCreatesOnlyTheFirstAdmin() {
        assertFalse(service.hasUsers());
        assertTrue(service.bootstrap("Admin@Example.com", PASSWORD));
        assertFalse(service.bootstrap("other@example.com", "another long password"), "a restart must not reset anything");
        assertEquals(1, store.countUsers());
        User admin = store.userByEmail("admin@example.com").orElseThrow();
        assertEquals(Accounts.ROLE_ADMIN, admin.role());
        assertTrue(admin.active());
    }

    @Test
    void signInStartsAndEndsASession() {
        service.bootstrap("admin@corp.com", PASSWORD);
        Accounts.Login login = service.login(" Admin@Corp.com ", PASSWORD, "1.1.1.1");
        assertFalse(login.token().isEmpty());
        assertNotEquals(login.token(), login.session().hash(), "only a hash of the token is stored");
        assertEquals(Accounts.hashToken(login.token()), login.session().hash());
        assertTrue(service.authenticate(login.token()).isPresent());
        service.logout(login.token());
        assertTrue(service.authenticate(login.token()).isEmpty(), "sign-out must end the session");
        assertTrue(service.authenticate("").isEmpty());
        assertTrue(service.authenticate(null).isEmpty());
    }

    @Test
    void anExpiredSessionStopsWorking() {
        service.bootstrap("admin@corp.com", PASSWORD);
        String token = service.login("admin@corp.com", PASSWORD, "1.1.1.1").token();
        clock.advance(Duration.ofHours(2));
        assertTrue(service.authenticate(token).isEmpty());
    }

    @Test
    void signInErrorsDoNotRevealWhichAccountsExist() {
        service.bootstrap("admin@corp.com", PASSWORD);
        AccountException wrong = assertThrows(AccountException.class, () -> service.login("admin@corp.com", "wrong password here", "1.1.1.1"));
        AccountException unknown = assertThrows(AccountException.class, () -> service.login("nobody@corp.com", PASSWORD, "1.1.1.1"));
        assertEquals(Kind.INVALID_CREDENTIALS, wrong.kind());
        assertEquals(wrong.getMessage(), unknown.getMessage());
        assertEquals("incorrect email or password", wrong.getMessage());
    }

    @Test
    void repeatedFailuresLockTheAccount() {
        service.bootstrap("admin@corp.com", PASSWORD);
        for (int i = 0; i < 5; i++) {
            assertEquals(Kind.INVALID_CREDENTIALS, kindOf(() -> service.login("admin@corp.com", "wrong password here", "2.2.2.2")));
        }
        assertEquals(Kind.LOCKED, kindOf(() -> service.login("admin@corp.com", PASSWORD, "2.2.2.2")), "even the right password is refused while locked");
        clock.advance(Duration.ofMinutes(16));
        assertTrue(service.login("admin@corp.com", PASSWORD, "2.2.2.2").session().csrf().length() > 10);
    }

    // ---- self sign-up with admin approval ------------------------------------------------------------------

    @Test
    void signUpIsOffByDefault() {
        assertFalse(service.signupEnabled());
        assertEquals(Kind.SIGNUP_DISABLED, kindOf(() -> service.signUp("new@corp.com", "New", PASSWORD, "3.3.3.3")));
    }

    @Test
    void aSignUpIsAPendingMemberWhoCannotSignInUntilApproved() {
        service.bootstrap("admin@corp.com", PASSWORD);
        service.withSignup(true);
        service.signUp(" New@Corp.com ", " Newbie ", PASSWORD, "3.3.3.3");
        User u = store.userByEmail("new@corp.com").orElseThrow();
        assertEquals(Accounts.ROLE_MEMBER, u.role());
        assertEquals(Accounts.STATUS_PENDING, u.status());
        assertFalse(u.active());
        assertEquals("Newbie", u.name());
        assertFalse(u.passwordHash().contains(PASSWORD), "the password is stored hashed");

        assertEquals(Kind.PENDING_APPROVAL, kindOf(() -> service.login("new@corp.com", PASSWORD, "3.3.3.3")));
        assertEquals(Kind.INVALID_CREDENTIALS, kindOf(() -> service.login("new@corp.com", "wrong password here", "3.3.3.3")),
                "a wrong password must stay generic: pending is only revealed to the right password");

        service.updateUser("admin@corp.com", u.id(), Patch.none().withStatus(Accounts.STATUS_ACTIVE));
        assertTrue(service.login("new@corp.com", PASSWORD, "3.3.3.3").user().active());
    }

    @Test
    void aRejectedSignUpCannotSignIn() {
        service.bootstrap("admin@corp.com", PASSWORD);
        service.withSignup(true);
        service.signUp("no@corp.com", "", PASSWORD, "4.4.4.4");
        User u = store.userByEmail("no@corp.com").orElseThrow();
        service.updateUser("admin@corp.com", u.id(), Patch.none().withStatus(Accounts.STATUS_REJECTED));
        assertEquals(Kind.ACCOUNT_REJECTED, kindOf(() -> service.login("no@corp.com", PASSWORD, "4.4.4.4")));
        assertEquals(Kind.INVALID_INPUT, kindOf(() -> service.updateUser("admin@corp.com", u.id(), Patch.none().withStatus("owner"))));
    }

    @Test
    void aRepeatSignUpLooksLikeSuccessAndChangesNothing() {
        service.bootstrap("admin@corp.com", PASSWORD);
        service.withSignup(true);
        service.signUp("admin@corp.com", "Intruder", "a different long password", "5.5.5.5");
        User admin = store.userByEmail("admin@corp.com").orElseThrow();
        assertEquals(Accounts.ROLE_ADMIN, admin.role());
        assertTrue(admin.active());
        assertTrue(PasswordHasher.verify(admin.passwordHash(), PASSWORD), "the existing password must be untouched");
    }

    @Test
    void signUpValidatesInputAndIsThrottledPerAddress() {
        service.withSignup(true);
        assertEquals(Kind.INVALID_INPUT, kindOf(() -> service.signUp("nope", "", PASSWORD, "6.6.6.6")));
        assertEquals(Kind.WEAK_PASSWORD, kindOf(() -> service.signUp("ok@corp.com", "", "short", "6.6.6.6")));
        Kind last = null;
        for (int i = 0; i < 25; i++) {
            try {
                service.signUp("u" + i + "@corp.com", "", PASSWORD, "7.7.7.7");
            } catch (AccountException e) {
                last = e.kind();
            }
        }
        assertEquals(Kind.TOO_MANY_SIGNUPS, last, "one address must be throttled");
        service.signUp("other@corp.com", "", PASSWORD, "8.8.8.8");
        assertTrue(store.userByEmail("other@corp.com").isPresent(), "another address is unaffected");
    }

    @Test
    void aSessionOfAnAccountThatIsNoLongerApprovedStopsWorking() {
        service.bootstrap("admin@corp.com", PASSWORD);
        User member = service.createUser("admin@corp.com", "m@corp.com", "", Accounts.ROLE_MEMBER, PASSWORD);
        String token = service.login("m@corp.com", PASSWORD, "9.9.9.9").token();
        assertTrue(service.authenticate(token).isPresent());
        service.updateUser("admin@corp.com", member.id(), Patch.none().withStatus(Accounts.STATUS_REJECTED));
        assertTrue(service.authenticate(token).isEmpty(), "rejecting ends the person's sessions");
    }

    // ---- account management ---------------------------------------------------------------------------------

    @Test
    void theLastAdminCannotBeDemotedOrDisabled() {
        service.bootstrap("admin@corp.com", PASSWORD);
        User admin = store.userByEmail("admin@corp.com").orElseThrow();
        assertEquals(Kind.LAST_ADMIN, kindOf(() -> service.updateUser("x", admin.id(), Patch.none().withRole(Accounts.ROLE_MEMBER))));
        assertEquals(Kind.LAST_ADMIN, kindOf(() -> service.updateUser("x", admin.id(), Patch.none().withDisabled(true))));
        service.createUser("x", "second@corp.com", "", Accounts.ROLE_ADMIN, PASSWORD);
        assertEquals(Accounts.ROLE_MEMBER, service.updateUser("x", admin.id(), Patch.none().withRole(Accounts.ROLE_MEMBER)).role());
    }

    @Test
    void disablingOrChangingAPasswordEndsSessions() {
        service.bootstrap("admin@corp.com", PASSWORD);
        User member = service.createUser("a", "m@corp.com", "", Accounts.ROLE_MEMBER, PASSWORD);
        String token = service.login("m@corp.com", PASSWORD, "1.1.1.1").token();
        service.updateUser("a", member.id(), Patch.none().withPassword("a brand new password"));
        assertTrue(service.authenticate(token).isEmpty());
        assertEquals(Kind.WEAK_PASSWORD, kindOf(() -> service.updateUser("a", member.id(), Patch.none().withPassword("short"))));
        assertEquals(Kind.NOT_FOUND, kindOf(() -> service.updateUser("a", "missing", Patch.none().withName("x"))));
        assertEquals(Kind.INVALID_INPUT, kindOf(() -> service.updateUser("a", member.id(), Patch.none().withRole("owner"))));
    }

    @Test
    void createUserValidatesAndRefusesDuplicates() {
        assertEquals(Kind.INVALID_INPUT, kindOf(() -> service.createUser("a", "nope", "", Accounts.ROLE_MEMBER, PASSWORD)));
        assertEquals(Kind.INVALID_INPUT, kindOf(() -> service.createUser("a", "x@corp.com", "", "owner", PASSWORD)));
        assertEquals(Kind.WEAK_PASSWORD, kindOf(() -> service.createUser("a", "x@corp.com", "", Accounts.ROLE_MEMBER, "short")));
        User sso = service.createUser("a", "sso@corp.com", "S", Accounts.ROLE_MEMBER, "");
        assertEquals("", sso.passwordHash(), "an account without a password can only sign in another way");
        assertEquals(Kind.INVALID_CREDENTIALS, kindOf(() -> service.login("sso@corp.com", PASSWORD, "1.1.1.1")));
        assertEquals(Kind.EMAIL_TAKEN, kindOf(() -> service.createUser("a", "SSO@corp.com", "", Accounts.ROLE_MEMBER, PASSWORD)));
    }

    @Test
    void theAuditTrailRecordsActionsWithoutSecrets() {
        service.bootstrap("admin@corp.com", PASSWORD);
        service.login("admin@corp.com", PASSWORD, "1.1.1.1");
        assertEquals(Kind.INVALID_CREDENTIALS, kindOf(() -> service.login("admin@corp.com", "wrong password here", "1.1.1.1")));
        String all = service.audit(50).toString();
        assertTrue(all.contains("user.created") && all.contains("login") && all.contains("login.failed"), all);
        assertFalse(all.contains(PASSWORD) || all.contains("argon2id") || all.contains("wrong password here"));
        assertEquals(1, service.audit(1).size());
        assertEquals("login.failed", service.audit(1).get(0).action(), "newest first");
    }

    @Test
    void anAccountWithNoStatusCountsAsActive() {
        Instant t = Instant.now();
        assertTrue(new User("1", "a@b.c", "", "member", "", false, "", t).active());
        assertTrue(new User("1", "a@b.c", "", "member", "", false, null, t).active());
        assertFalse(new User("1", "a@b.c", "", "member", "", true, "active", t).active());
        assertFalse(new User("1", "a@b.c", "", "member", "", false, "pending", t).active());
        assertFalse(new User("1", "a@b.c", "", "member", "", false, "rejected", t).active());
        assertEquals("active", new User("1", "a@b.c", "", "member", "", false, "", t).statusOrActive());
    }

    // ---- settings -------------------------------------------------------------------------------------------

    @Test
    void accountSettingsHaveSafeDefaultsAndRefuseUnknownWords() {
        Config c = Config.load(Map.of());
        assertEquals("off", c.authSignup(), "sign-up must be off unless switched on");
        assertEquals("auto", c.authCookieSecure());
        assertEquals("lax", c.authCookieSameSite());
        assertEquals(Duration.ofHours(12), c.authSessionTtl());
        assertFalse(c.bootstrapConfigured());
        assertThrows(Config.InvalidConfigException.class, () -> Config.load(Map.of("AUTH_SIGNUP", "open")));
        assertThrows(Config.InvalidConfigException.class, () -> Config.load(Map.of("AUTH_COOKIE_SAMESITE", "wide")));
        assertThrows(Config.InvalidConfigException.class, () -> Config.load(Map.of("AUTH_COOKIE_SECURE", "maybe")));
        assertEquals("approval", Config.load(Map.of("AUTH_SIGNUP", " Approval ")).authSignup());
    }
}
