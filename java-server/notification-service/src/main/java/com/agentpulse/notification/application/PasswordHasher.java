package com.agentpulse.notification.application;

import org.bouncycastle.crypto.generators.Argon2BytesGenerator;
import org.bouncycastle.crypto.params.Argon2Parameters;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Argon2id password hashing in the same self-describing format as the Go service
 * ({@code argon2id$m=19456,t=2,p=1$<salt>$<hash>}, raw standard base64), so an account created by one service
 * signs in on the other. The parameters are OWASP's minimum profile: 19 MiB, 2 passes, 1 lane.
 */
public final class PasswordHasher {
    public static final int MIN_LENGTH = 12;
    public static final int MAX_LENGTH = 256;

    private static final int MEMORY_KIB = 19 * 1024;
    private static final int ITERATIONS = 2;
    private static final int PARALLELISM = 1;
    private static final int KEY_LENGTH = 32;
    private static final int SALT_LENGTH = 16;
    private static final int MAX_HASH_BYTES = 64;
    private static final int MAX_MEMORY_KIB = 1 << 20;
    private static final Pattern PARAMS = Pattern.compile("m=(\\d+),t=(\\d+),p=(\\d+)");
    private static final Base64.Encoder ENCODER = Base64.getEncoder().withoutPadding();
    private static final Base64.Decoder DECODER = Base64.getDecoder();
    private static final SecureRandom RANDOM = new SecureRandom();

    private PasswordHasher() {}

    /** True when a new password is acceptable: length matters, composition rules do not. */
    public static boolean acceptable(String password) {
        return password != null && password.codePointCount(0, password.length()) >= MIN_LENGTH
                && password.getBytes(StandardCharsets.UTF_8).length <= MAX_LENGTH;
    }

    /** Returns a hash with a fresh random salt. */
    public static String hash(String password) {
        byte[] salt = new byte[SALT_LENGTH];
        RANDOM.nextBytes(salt);
        byte[] key = derive(password, salt, ITERATIONS, MEMORY_KIB, PARALLELISM, KEY_LENGTH);
        return "argon2id$m=" + MEMORY_KIB + ",t=" + ITERATIONS + ",p=" + PARALLELISM + "$"
                + ENCODER.encodeToString(salt) + "$" + ENCODER.encodeToString(key);
    }

    /** Checks a password against a hash in constant time. A malformed hash never verifies. */
    public static boolean verify(String hash, String password) {
        if (hash == null || password == null || password.getBytes(StandardCharsets.UTF_8).length > MAX_LENGTH) {
            return false;
        }
        String[] parts = hash.split("\\$", -1);
        if (parts.length != 4 || !parts[0].equals("argon2id")) {
            return false;
        }
        Matcher m = PARAMS.matcher(parts[1]);
        if (!m.matches()) {
            return false;
        }
        try {
            long memory = Long.parseLong(m.group(1));
            long iterations = Long.parseLong(m.group(2));
            long lanes = Long.parseLong(m.group(3));
            if (memory <= 0 || iterations <= 0 || lanes <= 0 || memory > MAX_MEMORY_KIB || iterations > 64 || lanes > 255) {
                return false;
            }
            byte[] salt = DECODER.decode(parts[2]);
            byte[] want = DECODER.decode(parts[3]);
            if (want.length == 0 || want.length > MAX_HASH_BYTES || salt.length == 0) {
                return false;
            }
            byte[] got = derive(password, salt, (int) iterations, (int) memory, (int) lanes, want.length);
            return MessageDigest.isEqual(got, want);
        } catch (IllegalArgumentException e) {
            return false; // bad base64 or numbers
        }
    }

    private static byte[] derive(String password, byte[] salt, int iterations, int memoryKib, int lanes, int length) {
        Argon2Parameters params = new Argon2Parameters.Builder(Argon2Parameters.ARGON2_id)
                .withVersion(Argon2Parameters.ARGON2_VERSION_13)
                .withIterations(iterations)
                .withMemoryAsKB(memoryKib)
                .withParallelism(lanes)
                .withSalt(salt)
                .build();
        Argon2BytesGenerator generator = new Argon2BytesGenerator();
        generator.init(params);
        byte[] out = new byte[length];
        generator.generateBytes(password.getBytes(StandardCharsets.UTF_8), out);
        return out;
    }
}
