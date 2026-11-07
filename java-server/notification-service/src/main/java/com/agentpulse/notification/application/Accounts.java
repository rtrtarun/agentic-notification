package com.agentpulse.notification.application;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.security.SecureRandom;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayDeque;
import java.util.Base64;
import java.util.Deque;
import java.util.HexFormat;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;
import java.util.regex.Pattern;

/**
 * People who can sign in: accounts, sessions and self sign-up with admin approval. Behaviour, messages and
 * the stored shape match the Go service ({@code pkg/auth}), which owns the schema.
 */
public final class Accounts {
    public static final String ROLE_ADMIN = "admin";
    public static final String ROLE_MEMBER = "member";
    public static final String STATUS_ACTIVE = "active";
    public static final String STATUS_PENDING = "pending";
    public static final String STATUS_REJECTED = "rejected";

    private Accounts() {}

    /** A person who can sign in. {@code status} empty means active, so older accounts keep working. */
    public record User(String id, String email, String name, String role, String passwordHash, boolean disabled,
                       String status, Instant createdAt) {
        /** Whether the account may sign in: not disabled, and approved. */
        public boolean active() {
            return !disabled && (status == null || status.isEmpty() || STATUS_ACTIVE.equals(status));
        }

        public String statusOrActive() {
            return status == null || status.isEmpty() ? STATUS_ACTIVE : status;
        }
    }

    /** A signed-in browser. {@code hash} is the SHA-256 of the cookie token, so a database leak hands out no live session. */
    public record Session(String hash, String userId, String csrf, Instant expiresAt, Instant createdAt) {}

    /** One security-relevant action. Details never hold secrets. */
    public record AuditEntry(Instant at, String actor, String action, String target, String ip, String detail) {}

    /** The result of a successful sign-in; {@code token} is shown once and never stored. */
    public record Login(String token, Session session, User user) {}

    /** A signed-in person's identity, as found from a cookie. */
    public record Authenticated(User user, Session session) {}

    /** A partial account update; null fields are left alone. */
    public record Patch(String name, String role, Boolean disabled, String password, String status) {
        public static Patch none() {
            return new Patch(null, null, null, null, null);
        }

        public Patch withStatus(String value) {
            return new Patch(name, role, disabled, password, value);
        }

        public Patch withRole(String value) {
            return new Patch(name, value, disabled, password, status);
        }

        public Patch withDisabled(Boolean value) {
            return new Patch(name, role, value, password, status);
        }

        public Patch withName(String value) {
            return new Patch(value, role, disabled, password, status);
        }

        public Patch withPassword(String value) {
            return new Patch(name, role, disabled, value, status);
        }
    }

    /** Where users, sessions and the audit trail live. Implementations: in memory, and PostgreSQL. */
    public interface Store {
        /** Adds a user; throws {@link AccountException} of kind EMAIL_TAKEN when the email exists. */
        void createUser(User user);

        Optional<User> userById(String id);

        Optional<User> userByEmail(String email);

        List<User> listUsers();

        /** Replaces name, role, password hash, disabled and status; throws NOT_FOUND for an unknown id. */
        void updateUser(User user);

        int countUsers();

        /** Counts enabled, approved admins, so the last one can never be removed. */
        int countActiveAdmins();

        void createSession(Session session);

        Optional<Session> sessionByHash(String hash);

        void deleteSession(String hash);

        void deleteUserSessions(String userId);

        void deleteExpiredSessions(Instant now);

        void addAudit(AuditEntry entry);

        /** Newest first. */
        List<AuditEntry> listAudit(int limit);
    }

    /** A refusal the HTTP layer maps to a status code. Messages match the Go service word for word. */
    public static final class AccountException extends RuntimeException {
        public enum Kind {
            INVALID_CREDENTIALS, LOCKED, PENDING_APPROVAL, ACCOUNT_REJECTED, SIGNUP_DISABLED, TOO_MANY_SIGNUPS,
            INVALID_INPUT, WEAK_PASSWORD, EMAIL_TAKEN, LAST_ADMIN, NOT_FOUND, UNAUTHENTICATED
        }

        private final Kind kind;

        public AccountException(Kind kind, String message) {
            super(message);
            this.kind = kind;
        }

        public Kind kind() {
            return kind;
        }
    }

    static AccountException invalid(String message) {
        return new AccountException(AccountException.Kind.INVALID_INPUT, "invalid input: " + message);
    }

    static AccountException weakPassword() {
        return new AccountException(AccountException.Kind.WEAK_PASSWORD,
                "password must be between " + PasswordHasher.MIN_LENGTH + " and " + PasswordHasher.MAX_LENGTH + " characters");
    }

    /** Counts failures and locks a key after too many inside a window. Per-address limits are looser than per-account ones. */
    static final class Limiter {
        private static final int IP_FACTOR = 5;
        private final int max;
        private final Duration window;
        private final Clock clock;
        private final Map<String, Deque<Instant>> failures = new HashMap<>();
        private static final int MAX_TRACKED = 10_000;

        Limiter(int max, Duration window, Clock clock) {
            this.max = max;
            this.window = window;
            this.clock = clock;
        }

        synchronized boolean locked(String... keys) {
            for (String key : keys) {
                if (recent(key) >= maxFor(key)) {
                    return true;
                }
            }
            return false;
        }

        synchronized void fail(String... keys) {
            Instant now = clock.instant();
            for (String key : keys) {
                if (failures.size() >= MAX_TRACKED && !failures.containsKey(key)) {
                    prune(now);
                    if (failures.size() >= MAX_TRACKED) {
                        continue; // bounded memory: a spray of keys cannot grow it without limit
                    }
                }
                failures.computeIfAbsent(key, k -> new ArrayDeque<>()).addLast(now);
            }
        }

        synchronized void reset(String key) {
            failures.remove(key);
        }

        private int maxFor(String key) {
            return key.startsWith("ip:") ? max * IP_FACTOR : max;
        }

        private int recent(String key) {
            Deque<Instant> q = failures.get(key);
            if (q == null) {
                return 0;
            }
            Instant cutoff = clock.instant().minus(window);
            while (!q.isEmpty() && q.peekFirst().isBefore(cutoff)) {
                q.removeFirst();
            }
            if (q.isEmpty()) {
                failures.remove(key);
                return 0;
            }
            return q.size();
        }

        private void prune(Instant now) {
            Instant cutoff = now.minus(window);
            failures.values().removeIf(q -> q.peekLast() == null || q.peekLast().isBefore(cutoff));
        }
    }

    /** The use cases: sign-in, sign-up, sessions and account management. */
    public static final class Service {
        private static final int MAX_FAILURES = 5;
        private static final Duration LOCKOUT = Duration.ofMinutes(15);
        private static final int SIGNUP_MAX = 20;
        private static final Duration SIGNUP_WINDOW = Duration.ofHours(1);
        private static final Pattern EMAIL = Pattern.compile("^[^@\\s<>\",;:()\\[\\]\\\\]+@[^@\\s<>\",;:()\\[\\]\\\\]+\\.[^@\\s<>\",;:()\\[\\]\\\\]+$");
        private static final SecureRandom RANDOM = new SecureRandom();

        /** Verified against when an account does not exist, so an unknown email takes as long as a real one. */
        private static final class DummyHash {
            static final String VALUE = PasswordHasher.hash("not-a-real-password");
        }

        private final Store store;
        private final Duration ttl;
        private final Clock clock;
        private final Limiter limiter;
        private final Limiter signupLimiter;
        private volatile boolean signup;

        public Service(Store store, Duration ttl, Clock clock) {
            this.store = store;
            this.ttl = ttl.isZero() || ttl.isNegative() ? Duration.ofHours(12) : ttl;
            this.clock = clock;
            this.limiter = new Limiter(MAX_FAILURES, LOCKOUT, clock);
            this.signupLimiter = new Limiter(SIGNUP_MAX, SIGNUP_WINDOW, clock);
        }

        /** Switches self sign-up on or off. When on, a new account is pending until an admin approves it. */
        public Service withSignup(boolean enabled) {
            this.signup = enabled;
            return this;
        }

        public boolean signupEnabled() {
            return signup;
        }

        public Duration sessionTtl() {
            return ttl;
        }

        public boolean hasUsers() {
            return store.countUsers() > 0;
        }

        /** Creates the first admin when there are no users; does nothing otherwise, so a restart cannot reset a password. */
        public boolean bootstrap(String email, String password) {
            if (store.countUsers() > 0) {
                return false;
            }
            create("system", email, "", ROLE_ADMIN, password);
            return true;
        }

        public Login login(String email, String password, String ip) {
            String normalized = normalize(email);
            if (limiter.locked("email:" + normalized, "ip:" + ip)) {
                audit(normalized, "login.locked", "", ip, "");
                throw new AccountException(AccountException.Kind.LOCKED, "too many failed sign-ins; try again later");
            }
            Optional<User> found = store.userByEmail(normalized);
            String hash = found.map(User::passwordHash).filter(h -> !h.isEmpty()).orElse(DummyHash.VALUE);
            boolean verified = PasswordHasher.verify(hash, password == null ? "" : password);
            if (!verified || found.isEmpty() || found.get().passwordHash().isEmpty() || found.get().disabled()) {
                limiter.fail("email:" + normalized, "ip:" + ip);
                audit(normalized, "login.failed", "", ip, "");
                throw new AccountException(AccountException.Kind.INVALID_CREDENTIALS, "incorrect email or password");
            }
            User user = found.get();
            limiter.reset("email:" + normalized);
            if (STATUS_PENDING.equals(user.status())) {
                audit(user.email(), "login.denied", "", ip, "pending approval");
                throw new AccountException(AccountException.Kind.PENDING_APPROVAL, "your account is waiting for approval");
            }
            if (STATUS_REJECTED.equals(user.status())) {
                audit(user.email(), "login.denied", "", ip, "sign-up rejected");
                throw new AccountException(AccountException.Kind.ACCOUNT_REJECTED, "your sign-up was not approved");
            }
            Login login = startSession(user);
            audit(user.email(), "login", "", ip, "password");
            return login;
        }

        /**
         * Records a request for an account. It starts as a pending member whose password is already hashed, and
         * nothing can be done with it until an admin approves it. A repeat request for an existing email succeeds
         * silently and changes nothing, so the caller learns nothing about who has an account.
         */
        public void signUp(String email, String name, String password, String ip) {
            if (!signup) {
                throw new AccountException(AccountException.Kind.SIGNUP_DISABLED, "sign-up is not available");
            }
            String key = "signup:" + ip;
            if (signupLimiter.locked(key)) {
                audit(normalize(email), "signup.throttled", "", ip, "");
                throw new AccountException(AccountException.Kind.TOO_MANY_SIGNUPS, "too many sign-up requests; try again later");
            }
            signupLimiter.fail(key); // every attempt counts, successful or not
            String normalized = normalize(email);
            requireEmail(normalized);
            if (!PasswordHasher.acceptable(password)) {
                throw weakPassword();
            }
            User user = new User(newId(), normalized, name == null ? "" : name.trim(), ROLE_MEMBER,
                    PasswordHasher.hash(password), false, STATUS_PENDING, clock.instant());
            try {
                store.createUser(user);
            } catch (AccountException e) {
                if (e.kind() == AccountException.Kind.EMAIL_TAKEN) {
                    audit(normalized, "signup.duplicate", "", ip, "");
                    return;
                }
                throw e;
            }
            audit(normalized, "signup.requested", user.id(), ip, "pending approval");
        }

        /** Finds who a cookie belongs to. Disabled, pending and rejected accounts never authenticate. */
        public Optional<Authenticated> authenticate(String token) {
            if (token == null || token.isEmpty()) {
                return Optional.empty();
            }
            Optional<Session> session = store.sessionByHash(hashToken(token));
            if (session.isEmpty() || !session.get().expiresAt().isAfter(clock.instant())) {
                return Optional.empty();
            }
            Optional<User> user = store.userById(session.get().userId());
            if (user.isEmpty() || !user.get().active()) {
                return Optional.empty();
            }
            return Optional.of(new Authenticated(user.get(), session.get()));
        }

        public void logout(String token) {
            if (token != null && !token.isEmpty()) {
                store.deleteSession(hashToken(token));
            }
        }

        /** Adds an account; {@code actor} is the email of the admin doing it. */
        public User createUser(String actor, String email, String name, String role, String password) {
            return create(actor, email, name, role, password);
        }

        /** Changes an account. Demoting or disabling the last active admin is refused. */
        public User updateUser(String actor, String id, Patch patch) {
            User user = store.userById(id).orElseThrow(() -> new AccountException(AccountException.Kind.NOT_FOUND, "not found"));
            boolean wasActiveAdmin = ROLE_ADMIN.equals(user.role()) && user.active();
            boolean endSessions = false;
            String name = user.name();
            String role = user.role();
            boolean disabled = user.disabled();
            String hash = user.passwordHash();
            String status = user.status();
            if (patch.name() != null) {
                name = patch.name().trim();
            }
            if (patch.role() != null) {
                if (!ROLE_ADMIN.equals(patch.role()) && !ROLE_MEMBER.equals(patch.role())) {
                    throw invalid("role must be admin or member");
                }
                role = patch.role();
            }
            if (patch.disabled() != null) {
                endSessions = endSessions || (patch.disabled() && !disabled);
                disabled = patch.disabled();
            }
            if (patch.status() != null) {
                if (!STATUS_ACTIVE.equals(patch.status()) && !STATUS_REJECTED.equals(patch.status())) {
                    throw invalid("status must be active or rejected");
                }
                boolean unchanged = patch.status().equals(status) || ((status == null || status.isEmpty()) && STATUS_ACTIVE.equals(patch.status()));
                endSessions = endSessions || !unchanged;
                status = patch.status();
            }
            if (patch.password() != null) {
                if (!PasswordHasher.acceptable(patch.password())) {
                    throw weakPassword();
                }
                hash = PasswordHasher.hash(patch.password());
                endSessions = true;
            }
            User updated = new User(user.id(), user.email(), name, role, hash, disabled, status, user.createdAt());
            if (wasActiveAdmin && (!ROLE_ADMIN.equals(role) || !updated.active()) && store.countActiveAdmins() <= 1) {
                throw new AccountException(AccountException.Kind.LAST_ADMIN, "there must be at least one enabled admin");
            }
            store.updateUser(updated);
            if (endSessions) {
                store.deleteUserSessions(updated.id());
            }
            audit(actor, "user.updated", updated.id(), "", describe(patch));
            return updated;
        }

        public List<User> users() {
            return store.listUsers();
        }

        public List<AuditEntry> audit(int limit) {
            return store.listAudit(limit);
        }

        // ---- internals ----------------------------------------------------------------------------------------

        private User create(String actor, String email, String name, String role, String password) {
            String normalized = normalize(email);
            requireEmail(normalized);
            if (!ROLE_ADMIN.equals(role) && !ROLE_MEMBER.equals(role)) {
                throw invalid("role must be admin or member");
            }
            String hash = "";
            if (password != null && !password.isEmpty()) {
                if (!PasswordHasher.acceptable(password)) {
                    throw weakPassword();
                }
                hash = PasswordHasher.hash(password);
            }
            User user = new User(newId(), normalized, name == null ? "" : name.trim(), role, hash, false, STATUS_ACTIVE, clock.instant());
            store.createUser(user);
            audit(actor, "user.created", user.id(), "", "role " + role);
            return user;
        }

        private void requireEmail(String email) {
            if (email.length() > 254 || !EMAIL.matcher(email).matches()) {
                throw invalid("a valid email is required");
            }
        }

        private Login startSession(User user) {
            String token = randomToken();
            Session session = new Session(hashToken(token), user.id(), randomToken(), clock.instant().plus(ttl), clock.instant());
            store.createSession(session);
            store.deleteExpiredSessions(clock.instant());
            return new Login(token, session, user);
        }

        private void audit(String actor, String action, String target, String ip, String detail) {
            try {
                store.addAudit(new AuditEntry(clock.instant(), actor, action, target, ip, detail));
            } catch (RuntimeException ignored) {
                // an audit failure must never block a sign-in; the store logs its own problems
            }
        }

        private static String describe(Patch p) {
            StringBuilder out = new StringBuilder();
            append(out, p.name() != null, "name");
            append(out, p.role() != null, "role " + p.role());
            append(out, p.disabled() != null, "disabled=" + p.disabled());
            append(out, p.status() != null, "status " + p.status());
            append(out, p.password() != null, "password"); // never the value
            return out.toString();
        }

        private static void append(StringBuilder out, boolean when, String text) {
            if (when) {
                if (!out.isEmpty()) {
                    out.append(", ");
                }
                out.append(text);
            }
        }
    }

    /** Lowercases and trims an address for storage and lookup. */
    public static String normalize(String email) {
        return email == null ? "" : email.trim().toLowerCase(Locale.ROOT);
    }

    static String newId() {
        byte[] b = new byte[12];
        RANDOM_BYTES.nextBytes(b);
        return HexFormat.of().formatHex(b);
    }

    static String randomToken() {
        byte[] b = new byte[32];
        RANDOM_BYTES.nextBytes(b);
        return Base64.getUrlEncoder().withoutPadding().encodeToString(b);
    }

    /** The SHA-256 of a cookie token, in hex: what is stored instead of the token. */
    public static String hashToken(String token) {
        try {
            return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(token.getBytes(StandardCharsets.UTF_8)));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    private static final SecureRandom RANDOM_BYTES = new SecureRandom();
}
