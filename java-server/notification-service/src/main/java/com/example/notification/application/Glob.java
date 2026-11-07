package com.example.notification.application;

import java.util.regex.Pattern;

/**
 * The wildcard used in rule fields. {@code *} matches any run of characters except a slash, so "acme/*" matches
 * "acme/api" but not "acme/api/v2"; {@code ?} matches one character except a slash. Character classes and
 * escapes are not supported and are rejected when rules are loaded.
 */
public final class Glob {
    private Glob() {}

    /** Returns null when the pattern is usable, otherwise a short reason. */
    public static String validate(String pattern) {
        if (pattern.indexOf('[') >= 0 || pattern.indexOf('\\') >= 0) {
            return "character classes and escapes are not supported";
        }
        return null;
    }

    /** An empty pattern matches anything; otherwise the whole value must match. */
    public static boolean matches(String pattern, String value) {
        if (pattern.isEmpty()) {
            return true;
        }
        if (validate(pattern) != null) {
            return pattern.equals(value);
        }
        StringBuilder regex = new StringBuilder();
        for (char c : pattern.toCharArray()) {
            switch (c) {
                case '*' -> regex.append("[^/]*");
                case '?' -> regex.append("[^/]");
                default -> regex.append(Pattern.quote(String.valueOf(c)));
            }
        }
        return Pattern.compile(regex.toString()).matcher(value).matches();
    }
}
