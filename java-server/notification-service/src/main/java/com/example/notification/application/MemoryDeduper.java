package com.example.notification.application;

import com.example.notification.domain.Event;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.Map;

/** An in-memory {@link Dedup.Deduper} with a time window. It deduplicates within one process only. */
public final class MemoryDeduper implements Dedup.Deduper {
    /** How long an identical event counts as a duplicate. */
    public static final Duration DEFAULT_WINDOW = Duration.ofMinutes(10);
    /** When full, new keys are not recorded: a duplicate may slip through, a distinct event is never suppressed. */
    static final int MAX_ENTRIES = 100_000;

    private record Seen(String id, Instant at) {}

    private final Duration window;
    private final Clock clock;
    private final Map<String, Seen> seen = new HashMap<>();
    private Instant nextSweep = Instant.MIN;

    public MemoryDeduper(Duration window, Clock clock) {
        this.window = window == null || window.isZero() || window.isNegative() ? DEFAULT_WINDOW : window;
        this.clock = clock;
    }

    @Override
    public synchronized Dedup.Result check(Event event) {
        String key = Dedup.dedupKey(event);
        String thread = Dedup.threadId(event);
        Instant now = clock.instant();
        sweep(now);

        Seen previous = seen.get(key);
        if (previous != null && Duration.between(previous.at(), now).compareTo(window) <= 0) {
            return new Dedup.Result(key, thread, true, previous.id());
        }
        if (seen.size() < MAX_ENTRIES) {
            seen.put(key, new Seen(event.getId(), now));
        }
        return new Dedup.Result(key, thread, false, "");
    }

    @Override
    public synchronized void forget(String key, String id) {
        Seen previous = seen.get(key);
        if (previous != null && previous.id().equals(id)) {
            seen.remove(key);
        }
    }

    /** Drops expired entries at most once per half window so the map stays bounded by the window. */
    private void sweep(Instant now) {
        if (now.isBefore(nextSweep) && seen.size() < MAX_ENTRIES) {
            return;
        }
        seen.values().removeIf(s -> Duration.between(s.at(), now).compareTo(window) > 0);
        nextSweep = now.plus(window.dividedBy(2));
    }
}
