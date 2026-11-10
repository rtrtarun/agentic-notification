package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Budgets;
import redis.clients.jedis.JedisPooled;
import redis.clients.jedis.params.SetParams;

import java.time.Duration;
import java.util.List;
import java.util.Optional;

/** Adapts a Jedis client to {@link RedisDeduper.KeyStore}. */
public final class JedisKeyStore implements RedisDeduper.KeyStore, Budgets.SlidingWindow, AutoCloseable {
    private static final int TIMEOUT_MS = 2000;
    private static final String DELETE_IF_EQUALS =
            "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0";

    /** The same script the Go service runs, so both count one budget: drop old entries, add one if there is room. */
    private static final String SLIDE = """
            redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', tonumber(ARGV[1]) - tonumber(ARGV[2]))
            local n = redis.call('ZCARD', KEYS[1])
            if n < tonumber(ARGV[3]) then
              redis.call('ZADD', KEYS[1], ARGV[1], ARGV[4])
              redis.call('PEXPIRE', KEYS[1], ARGV[2])
              return {1, n + 1, 0}
            end
            local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
            return {0, n, tonumber(oldest[2])}
            """;

    private final JedisPooled client;

    private JedisKeyStore(JedisPooled client) {
        this.client = client;
    }

    /** Connects to {@code redis://host:port/db} and checks the server answers; throws when it does not. */
    public static JedisKeyStore connect(String url) {
        JedisPooled client = new JedisPooled(java.net.URI.create(url), TIMEOUT_MS);
        try {
            client.ping();
        } catch (RuntimeException e) {
            client.close();
            throw e;
        }
        return new JedisKeyStore(client);
    }

    @Override
    public boolean setIfAbsent(String key, String value, Duration ttl) {
        return "OK".equals(client.set(key, value, SetParams.setParams().nx().px(ttl.toMillis())));
    }

    @Override
    public Optional<String> get(String key) {
        return Optional.ofNullable(client.get(key));
    }

    @Override
    public void deleteIfEquals(String key, String value) {
        client.eval(DELETE_IF_EQUALS, List.of(key), List.of(value));
    }

    @Override
    public Budgets.Slide slide(String key, long nowMillis, long windowMillis, int limit, String member) {
        Object raw = client.eval(SLIDE, List.of(key), List.of(Long.toString(nowMillis), Long.toString(windowMillis),
                Integer.toString(limit), member));
        if (!(raw instanceof List<?> r) || r.size() != 3 || !(r.get(0) instanceof Long allowed) || !(r.get(1) instanceof Long used)) {
            throw new IllegalStateException("unexpected reply from the budget script");
        }
        return new Budgets.Slide(allowed == 1L, used.intValue());
    }

    @Override
    public void close() {
        client.close();
    }
}
