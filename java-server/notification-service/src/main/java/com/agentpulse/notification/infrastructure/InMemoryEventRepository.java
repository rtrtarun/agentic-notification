package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.domain.Event;

import java.time.Clock;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;

/** Keeps events in memory. Used when no database is configured, and in tests. */
public final class InMemoryEventRepository implements EventRepository {
    private final Clock clock;
    private final List<Event> events = new ArrayList<>();

    public InMemoryEventRepository(Clock clock) {
        this.clock = clock;
    }

    @Override
    public synchronized void save(Event event) {
        if (exists(event.getId())) {
            return;
        }
        Event stored = event.copy();
        if (stored.getCreatedAt() == null) {
            stored.setCreatedAt(clock.instant());
        }
        events.add(stored);
    }

    @Override
    public synchronized boolean exists(String id) {
        return events.stream().anyMatch(e -> e.getId().equals(id));
    }

    /**
     * Newest first in the PostgreSQL order ({@code created_at DESC, id DESC}), whatever order events were saved in,
     * so a run without a database orders events stored at the same instant exactly as one with it does (and as Go).
     */
    @Override
    public synchronized List<Event> listRecent(int limit) {
        return events.stream()
                .sorted(Comparator.comparing(Event::getCreatedAt).thenComparing(Event::getId).reversed())
                .limit(Math.max(0, limit))
                .map(Event::copy)
                .toList();
    }
}
