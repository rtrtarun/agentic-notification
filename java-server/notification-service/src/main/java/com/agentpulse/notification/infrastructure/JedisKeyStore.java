package com.agentpulse.notification.infrastructure;

import redis.clients.jedis.JedisPooled;
import redis.clients.jedis.params.SetParams;

import java.time.Duration;
import java.util.List;
import java.util.Optional;

/** Adapts a Jedis client to {@link RedisDeduper.KeyStore}. */
public final class JedisKeyStore implements RedisDeduper.KeyStore, AutoCloseable {
    private static final int TIMEOUT_MS = 2000;
    private static final String DELETE_IF_EQUALS =
            "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0";

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
    public void close() {
        client.close();
    }
}
