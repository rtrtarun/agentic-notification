package com.example.notification.interfaces;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.MessageDigest;
import java.util.HexFormat;

/** Verifies GitHub's {@code X-Hub-Signature-256}: HMAC-SHA256 of the raw body, compared in constant time. */
public final class Signature {
    private static final String PREFIX = "sha256=";

    private Signature() {}

    public static boolean valid(String secret, byte[] body, String header) {
        if (header == null || !header.startsWith(PREFIX)) {
            return false;
        }
        byte[] got;
        try {
            got = HexFormat.of().parseHex(header.substring(PREFIX.length()));
        } catch (IllegalArgumentException e) {
            return false;
        }
        return MessageDigest.isEqual(got, hmac(secret, body));
    }

    /** The header value GitHub would send for this body; used by tests and the contract runner. */
    public static String sign(String secret, byte[] body) {
        return PREFIX + HexFormat.of().formatHex(hmac(secret, body));
    }

    private static byte[] hmac(String secret, byte[] body) {
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(secret.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
            return mac.doFinal(body);
        } catch (GeneralSecurityException e) {
            throw new IllegalStateException(e);
        }
    }
}
