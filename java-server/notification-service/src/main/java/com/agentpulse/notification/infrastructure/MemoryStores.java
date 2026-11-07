package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Delivery;
import com.agentpulse.notification.application.Ports.DeliveryStore;
import com.agentpulse.notification.application.Ports.RuleStore;
import com.agentpulse.notification.domain.Rule;

import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/** In-process stores, used without a database and in tests. */
public final class MemoryStores {
    private MemoryStores() {}

    /** Keeps the rule list in memory. */
    public static final class Rules implements RuleStore {
        private List<Rule> rules = List.of();

        @Override
        public synchronized List<Rule> list() {
            return List.copyOf(rules);
        }

        @Override
        public synchronized void replace(List<Rule> list) {
            rules = List.copyOf(list);
        }
    }

    /** Keeps the delivery queue in memory. Mirrors the PostgreSQL store's behaviour. */
    public static final class Deliveries implements DeliveryStore {
        private final Map<String, Delivery.Job> jobs = new LinkedHashMap<>();

        private Delivery.Job find(String eventId, String channel) {
            return jobs.values().stream().filter(j -> j.eventId().equals(eventId) && j.channel().equals(channel))
                    .findFirst().orElse(null);
        }

        @Override
        public synchronized boolean enqueue(Delivery.Job job) {
            if (find(job.eventId(), job.channel()) != null) {
                return false;
            }
            jobs.put(job.id(), job);
            return true;
        }

        @Override
        public synchronized void record(Delivery.Job job) {
            Delivery.Job old = find(job.eventId(), job.channel());
            if (old != null) {
                jobs.remove(old.id());
                job = new Delivery.Job(job.id(), job.eventId(), job.channel(), job.attentionClass(), job.title(),
                        job.message(), job.group(), job.status(), job.attempts(), job.nextAttemptAt(), job.leaseUntil(),
                        job.lastError(), job.direct(), old.createdAt(), job.updatedAt());
            }
            jobs.put(job.id(), job);
        }

        @Override
        public synchronized List<Delivery.Job> claim(Instant now, Duration lease, int limit) {
            List<Delivery.Job> due = new ArrayList<>();
            for (Delivery.Job j : jobs.values()) {
                boolean pending = Delivery.Status.PENDING.equals(j.status()) && !j.nextAttemptAt().isAfter(now);
                boolean expired = Delivery.Status.SENDING.equals(j.status()) && !j.leaseUntil().isAfter(now);
                if (pending || expired) {
                    due.add(j);
                }
            }
            // Ties are broken by id so the order never depends on insertion.
            due.sort(Comparator.comparing(Delivery.Job::nextAttemptAt).thenComparing(Delivery.Job::id));
            List<Delivery.Job> out = new ArrayList<>();
            for (Delivery.Job j : due.subList(0, Math.min(limit, due.size()))) {
                Delivery.Job leased = j.with(Delivery.Status.SENDING, j.attempts() + 1, j.nextAttemptAt(),
                        now.plus(lease), j.lastError(), now);
                jobs.put(leased.id(), leased);
                out.add(leased);
            }
            return out;
        }

        private Delivery.Job get(String id) {
            Delivery.Job j = jobs.get(id);
            if (j == null) {
                throw new Delivery.NotFoundException();
            }
            return j;
        }

        @Override
        public synchronized void complete(String id, Instant now) {
            Delivery.Job j = get(id);
            jobs.put(id, j.with(Delivery.Status.SENT, j.attempts(), j.nextAttemptAt(), j.leaseUntil(), "", now));
        }

        @Override
        public synchronized void fail(String id, String error, Instant retryAt, Instant now) {
            Delivery.Job j = get(id);
            if (retryAt == null) {
                jobs.put(id, j.with(Delivery.Status.DEAD, j.attempts(), j.nextAttemptAt(), j.leaseUntil(), error, now));
            } else {
                jobs.put(id, j.with(Delivery.Status.PENDING, j.attempts(), retryAt, j.leaseUntil(), error, now));
            }
        }

        @Override
        public synchronized void requeue(String id, Instant now) {
            Delivery.Job j = get(id);
            if (!Delivery.Status.DEAD.equals(j.status())) {
                throw new Delivery.NotRetryableException();
            }
            jobs.put(id, j.with(Delivery.Status.PENDING, 0, now, j.leaseUntil(), j.lastError(), now));
        }

        @Override
        public synchronized List<Delivery.Job> list(int limit) {
            List<Delivery.Job> out = new ArrayList<>(jobs.values());
            out.sort(Comparator.comparing(Delivery.Job::createdAt).reversed());
            return out.subList(0, Math.min(limit, out.size()));
        }

        @Override
        public synchronized Map<String, Integer> counts() {
            Map<String, Integer> out = new HashMap<>();
            jobs.values().forEach(j -> out.merge(j.status(), 1, Integer::sum));
            return out;
        }
    }
}
