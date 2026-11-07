package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Dedup;
import com.agentpulse.notification.application.MemoryDeduper;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.domain.Event;

import java.time.Duration;
import java.util.Optional;

/**
 * Shares the dedup window across replicas through a {@link KeyStore} (Redis in production). It records the event
 * atomically (SET NX with a time-to-live), so two replicas seeing the same event cannot both think it is new.
 * It throws {@link PortException} when the store fails; the pipeline then treats the event as unique (fail open),
 * so an unavailable Redis can never suppress an event.
 */
public final class RedisDeduper implements Dedup.Deduper {
    static final String KEY_PREFIX = "dedup:";

    /** The slice of Redis the deduper needs, kept small so it can be faked. */
    public interface KeyStore {
        /** Stores value under key with a time-to-live if the key is absent; reports whether it did. */
        boolean setIfAbsent(String key, String value, Duration ttl);

        Optional<String> get(String key);

        /** Deletes key only when it still holds value. */
        void deleteIfEquals(String key, String value);
    }

    private final KeyStore store;
    private final Duration window;

    public RedisDeduper(KeyStore store, Duration window) {
        this.store = store;
        this.window = window == null || window.isZero() || window.isNegative() ? MemoryDeduper.DEFAULT_WINDOW : window;
    }

    @Override
    public Dedup.Result check(Event event) {
        String key = Dedup.dedupKey(event);
        String thread = Dedup.threadId(event);
        try {
            if (store.setIfAbsent(KEY_PREFIX + key, event.getId(), window)) {
                return new Dedup.Result(key, thread, false, "");
            }
            Optional<String> original = store.get(KEY_PREFIX + key);
            return original.map(id -> new Dedup.Result(key, thread, true, id))
                    .orElseGet(() -> new Dedup.Result(key, thread, false, ""));
        } catch (RuntimeException e) {
            throw new PortException("dedup store: " + e.getMessage(), e);
        }
    }

    @Override
    public void forget(String key, String id) {
        try {
            store.deleteIfEquals(KEY_PREFIX + key, id);
        } catch (RuntimeException e) {
            throw new PortException("dedup store: " + e.getMessage(), e);
        }
    }
}
