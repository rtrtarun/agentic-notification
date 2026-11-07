package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Dedup;
import com.agentpulse.notification.application.Ports.PortException;
import com.agentpulse.notification.domain.Event;
import org.junit.jupiter.api.Test;

import java.time.Duration;
import java.util.Map;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/**
 * Redis deduplication against a real Redis. Set TEST_REDIS_URL, for example {@code redis://localhost:6379/0}; without
 * it the tests are skipped and CI (which starts Redis) runs them.
 */
class JedisKeyStoreTest {
    private final String run = UUID.randomUUID().toString().substring(0, 8);

    private static String url() {
        String url = System.getenv("TEST_REDIS_URL");
        assumeTrue(url != null && !url.isEmpty(), "TEST_REDIS_URL not set; skipping the Redis tests");
        return url;
    }

    private Event pr(String id, int number) {
        return new Event(id, "github", "pull_request", "java-test-" + run,
                new java.util.HashMap<>(Map.of("action", "opened", "number", number)));
    }

    @Test
    void twoReplicasSharingARedisSeeEachOthersEvents() {
        try (JedisKeyStore a = JedisKeyStore.connect(url()); JedisKeyStore b = JedisKeyStore.connect(url())) {
            RedisDeduper replicaA = new RedisDeduper(a, Duration.ofMinutes(1));
            RedisDeduper replicaB = new RedisDeduper(b, Duration.ofMinutes(1));

            assertFalse(replicaA.check(pr("e1", 1)).duplicate());
            Dedup.Result repeat = replicaB.check(pr("e2", 1));
            assertTrue(repeat.duplicate(), "the second replica sees what the first recorded");
            assertEquals("e1", repeat.originalId());
            assertFalse(replicaB.check(pr("e3", 2)).duplicate(), "a different pull request is its own thing");
        }
    }

    @Test
    void forgetOnlyRemovesTheKeyTheSameEventCreatedAndTheKeyExpires() throws Exception {
        try (JedisKeyStore store = JedisKeyStore.connect(url())) {
            RedisDeduper d = new RedisDeduper(store, Duration.ofMillis(400));
            Event first = pr("e1", 10);
            d.check(first);

            d.forget(Dedup.dedupKey(first), "someone-else");
            assertTrue(d.check(pr("e2", 10)).duplicate(), "another event's forget changes nothing");

            Thread.sleep(600);
            assertFalse(d.check(pr("e3", 10)).duplicate(), "the window has passed, so it is new again");

            d.forget(Dedup.dedupKey(pr("e3", 10)), "e3");
            assertFalse(d.check(pr("e4", 10)).duplicate(), "forgotten by the event that made it");
            d.forget(Dedup.dedupKey(pr("e4", 10)), "e4");
        }
    }

    @Test
    void anUnreachableRedisFailsToConnectAndAnUnreachableOneAtRuntimeIsReported() {
        assertThrows(RuntimeException.class, () -> JedisKeyStore.connect("redis://localhost:1/0"));
        JedisKeyStore store = JedisKeyStore.connect(url());
        RedisDeduper d = new RedisDeduper(store, Duration.ofMinutes(1));
        store.close();
        assertThrows(PortException.class, () -> d.check(pr("e1", 99)), "the pipeline then fails open");
    }
}
