package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Budget;
import com.agentpulse.notification.application.Ports.BudgetResult;
import com.agentpulse.notification.application.Ports.PortException;

import java.security.SecureRandom;
import java.time.Clock;
import java.time.Duration;
import java.util.ArrayDeque;
import java.util.Deque;
import java.util.HexFormat;

/**
 * The attention budget: a rolling cap on how many standard notifications interrupt the workspace. When it is used
 * up, further standard events are held for the next digest, never dropped, and it is never consulted for a
 * critical event. A port of the Go service's {@code pkg/budget}; the Redis form uses the same key and script, so
 * replicas of either service count one budget together.
 */
public final class Budgets {
    /** Interruptions allowed per window when nothing else is configured. */
    public static final int DEFAULT_PER_HOUR = 10;
    /** The rolling window the cap applies to. */
    public static final Duration DEFAULT_WINDOW = Duration.ofHours(1);

    private Budgets() {}

    /** Counts interruptions in process memory. Correct for one replica; use {@link Redis} to share a cap. */
    public static final class Memory implements Budget {
        private final int limit;
        private final Duration window;
        private final Clock clock;
        private final Deque<Long> times = new ArrayDeque<>();

        public Memory(int limit, Duration window, Clock clock) {
            this.limit = limit <= 0 ? DEFAULT_PER_HOUR : limit;
            this.window = window == null || window.isZero() || window.isNegative() ? DEFAULT_WINDOW : window;
            this.clock = clock;
        }

        @Override
        public synchronized BudgetResult take() {
            long now = clock.millis();
            long cutoff = now - window.toMillis();
            while (!times.isEmpty() && times.peekFirst() <= cutoff) {
                times.pollFirst();
            }
            if (times.size() >= limit) {
                return new BudgetResult(false, times.size(), limit);
            }
            times.addLast(now);
            return new BudgetResult(true, times.size(), limit);
        }
    }

    /** The slice of Redis the shared budget needs, kept small so it can be faked. */
    public interface SlidingWindow {
        /**
         * Atomically drops entries older than the window and, if fewer than {@code limit} remain, adds one under
         * {@code member}. Reports whether it added one and how many entries the window then holds.
         */
        Slide slide(String key, long nowMillis, long windowMillis, int limit, String member);
    }

    /** What {@link SlidingWindow#slide} decided. */
    public record Slide(boolean allowed, int used) {}

    /** Shares one rolling window across replicas through Redis. */
    public static final class Redis implements Budget {
        static final String KEY = "attention:budget:workspace";
        private static final SecureRandom RANDOM = new SecureRandom();

        private final SlidingWindow store;
        private final int limit;
        private final Duration window;
        private final Clock clock;

        public Redis(SlidingWindow store, int limit, Duration window, Clock clock) {
            this.store = store;
            this.limit = limit <= 0 ? DEFAULT_PER_HOUR : limit;
            this.window = window == null || window.isZero() || window.isNegative() ? DEFAULT_WINDOW : window;
            this.clock = clock;
        }

        @Override
        public BudgetResult take() {
            byte[] raw = new byte[8];
            RANDOM.nextBytes(raw);
            try {
                Slide s = store.slide(KEY, clock.millis(), window.toMillis(), limit, HexFormat.of().formatHex(raw));
                return new BudgetResult(s.allowed(), s.used(), limit);
            } catch (RuntimeException e) {
                throw new PortException("attention budget: " + e.getMessage(), e);
            }
        }
    }
}
