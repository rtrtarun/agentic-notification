package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.DeliveryStore;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

/**
 * Sends queued jobs. A failed job is retried with exponential backoff; after {@code maxAttempts}, or on a permanent
 * error, it is dead-lettered and stays visible until a person retries it. Several instances can run against one
 * database: the store hands each job to one of them.
 */
public final class Dispatcher {
    private static final Logger LOG = LoggerFactory.getLogger(Dispatcher.class);

    public static final int DEFAULT_MAX_ATTEMPTS = 5;
    public static final Duration DEFAULT_BACKOFF_BASE = Duration.ofSeconds(30);
    static final Duration MAX_BACKOFF = Duration.ofHours(1);
    static final Duration LEASE = Duration.ofMinutes(2);
    static final Duration POLL = Duration.ofSeconds(2);
    static final int BATCH = 20;

    private final DeliveryStore store;
    private final Targets targets;
    private final Map<String, NotificationProvider> providers;
    private final Clock clock;
    private final int maxAttempts;
    private final Duration backoffBase;
    private ScheduledExecutorService scheduler;

    public Dispatcher(DeliveryStore store, Targets targets, Map<String, NotificationProvider> providers, Clock clock,
                      int maxAttempts, Duration backoffBase) {
        this.store = store;
        this.targets = targets;
        this.providers = providers;
        this.clock = clock;
        this.maxAttempts = maxAttempts > 0 ? maxAttempts : DEFAULT_MAX_ATTEMPTS;
        this.backoffBase = backoffBase == null || backoffBase.isZero() || backoffBase.isNegative()
                ? DEFAULT_BACKOFF_BASE : backoffBase;
    }

    /** Starts polling the queue in the background. */
    public synchronized void start() {
        if (scheduler != null) {
            return;
        }
        scheduler = Executors.newSingleThreadScheduledExecutor(r -> {
            Thread t = new Thread(r, "delivery-dispatcher");
            t.setDaemon(true);
            return t;
        });
        scheduler.scheduleWithFixedDelay(() -> {
            try {
                runOnce();
            } catch (RuntimeException e) {
                LOG.warn("delivery dispatcher pass failed error={}", Delivery.redact(e.getMessage()));
            }
        }, 0, POLL.toMillis(), TimeUnit.MILLISECONDS);
    }

    public synchronized void stop() {
        if (scheduler != null) {
            scheduler.shutdownNow();
            scheduler = null;
        }
    }

    /** Claims and sends the jobs that are due; returns how many it handled. */
    public int runOnce() {
        List<Delivery.Job> jobs = store.claim(clock.instant(), LEASE, BATCH);
        for (Delivery.Job job : jobs) {
            process(job);
        }
        return jobs.size();
    }

    private void process(Delivery.Job job) {
        try {
            send(job);
        } catch (RuntimeException e) {
            Instant now = clock.instant();
            String error = Delivery.redact(e.getMessage());
            if (e instanceof Delivery.PermanentException || job.attempts() >= maxAttempts) {
                record(() -> store.fail(job.id(), error, null, now), job);
                LOG.warn("delivery dead-lettered id={} channel={} attempts={} error={}", job.id(), job.channel(),
                        job.attempts(), error);
            } else {
                Instant retryAt = now.plus(backoff(job.attempts()));
                record(() -> store.fail(job.id(), error, retryAt, now), job);
                LOG.warn("delivery failed; will retry id={} channel={} attempts={} error={}", job.id(), job.channel(),
                        job.attempts(), error);
            }
            return;
        }
        record(() -> store.complete(job.id(), clock.instant()), job);
    }

    private void send(Delivery.Job job) {
        NotificationProvider provider = providers.get(job.channel());
        if (provider == null) {
            throw new Delivery.PermanentException("no provider for channel \"" + job.channel() + "\"");
        }
        Targets only = targets.only(job.channel());
        if (only.forChannel(job.channel()).isEmpty()) {
            throw new Delivery.PermanentException("channel \"" + job.channel() + "\" is not configured");
        }
        provider.send(new Notification(job.title(), job.message(), "attention-orchestration", "", job.group(), only));
    }

    private void record(Runnable update, Delivery.Job job) {
        try {
            update.run();
        } catch (RuntimeException e) {
            LOG.error("record delivery result failed id={} error={}", job.id(), Delivery.redact(e.getMessage()));
        }
    }

    /** Doubles from the base each attempt, up to an hour. */
    Duration backoff(int attempt) {
        Duration wait = backoffBase;
        for (int i = 1; i < attempt && wait.compareTo(MAX_BACKOFF) < 0; i++) {
            wait = wait.multipliedBy(2);
        }
        return wait.compareTo(MAX_BACKOFF) > 0 ? MAX_BACKOFF : wait;
    }
}
