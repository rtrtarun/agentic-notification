package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Accounts.AccountException;
import com.agentpulse.notification.application.Accounts.AuditEntry;
import com.agentpulse.notification.application.Accounts.Session;
import com.agentpulse.notification.application.Accounts.Store;
import com.agentpulse.notification.application.Accounts.User;

import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/** The in-process account store, used without a database and in tests. Safe for concurrent use. */
public final class MemoryAccountStore implements Store {
    private static final int MAX_AUDIT = 5000;

    private final Map<String, User> users = new HashMap<>();
    private final Map<String, Session> sessions = new HashMap<>();
    private final List<AuditEntry> audit = new ArrayList<>();

    @Override
    public synchronized void createUser(User user) {
        for (User other : users.values()) {
            if (other.email().equals(user.email())) {
                throw new AccountException(AccountException.Kind.EMAIL_TAKEN, "an account with that email already exists");
            }
        }
        users.put(user.id(), user);
    }

    @Override
    public synchronized Optional<User> userById(String id) {
        return Optional.ofNullable(users.get(id));
    }

    @Override
    public synchronized Optional<User> userByEmail(String email) {
        return users.values().stream().filter(u -> u.email().equals(email)).findFirst();
    }

    @Override
    public synchronized List<User> listUsers() {
        return users.values().stream().sorted(Comparator.comparing(User::email)).toList();
    }

    @Override
    public synchronized void updateUser(User user) {
        if (!users.containsKey(user.id())) {
            throw new AccountException(AccountException.Kind.NOT_FOUND, "not found");
        }
        users.put(user.id(), user);
    }

    @Override
    public synchronized int countUsers() {
        return users.size();
    }

    @Override
    public synchronized int countActiveAdmins() {
        return (int) users.values().stream().filter(u -> "admin".equals(u.role()) && u.active()).count();
    }

    @Override
    public synchronized void createSession(Session session) {
        sessions.put(session.hash(), session);
    }

    @Override
    public synchronized Optional<Session> sessionByHash(String hash) {
        return Optional.ofNullable(sessions.get(hash));
    }

    @Override
    public synchronized void deleteSession(String hash) {
        sessions.remove(hash);
    }

    @Override
    public synchronized void deleteUserSessions(String userId) {
        sessions.values().removeIf(s -> s.userId().equals(userId));
    }

    @Override
    public synchronized void deleteExpiredSessions(Instant now) {
        sessions.values().removeIf(s -> !s.expiresAt().isAfter(now));
    }

    @Override
    public synchronized void addAudit(AuditEntry entry) {
        audit.add(entry);
        if (audit.size() > MAX_AUDIT) {
            audit.subList(0, audit.size() - MAX_AUDIT).clear();
        }
    }

    @Override
    public synchronized List<AuditEntry> listAudit(int limit) {
        List<AuditEntry> out = new ArrayList<>(audit);
        java.util.Collections.reverse(out);
        return limit > 0 && out.size() > limit ? new ArrayList<>(out.subList(0, limit)) : out;
    }
}
