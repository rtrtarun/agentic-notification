package com.agentpulse.notification.domain;

/** The three attention classes. Critical events are decided by rules alone and never reach AI. */
public final class Attention {
    public static final String CRITICAL = "critical";
    public static final String STANDARD = "standard";
    public static final String BACKGROUND = "background";

    private Attention() {}

    /** Reports whether the value is one of the three known classes. */
    public static boolean isKnown(String value) {
        return CRITICAL.equals(value) || STANDARD.equals(value) || BACKGROUND.equals(value);
    }
}
