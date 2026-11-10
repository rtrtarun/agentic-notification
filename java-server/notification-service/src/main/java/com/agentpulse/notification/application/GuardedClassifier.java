package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Classification;
import com.agentpulse.notification.application.Ports.Classifier;
import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;

/**
 * Wraps a classifier so it can never see a critical event and can never answer with anything but standard or
 * background. Every classifier the pipeline uses goes through it, so a future model cannot break the promise that
 * critical events never touch AI.
 */
public final class GuardedClassifier implements Classifier {
    private final Classifier inner;

    private GuardedClassifier(Classifier inner) {
        this.inner = inner;
    }

    /** Wraps a classifier; null stays null. */
    public static Classifier of(Classifier inner) {
        return inner == null ? null : new GuardedClassifier(inner);
    }

    @Override
    public Classification classify(Event event) {
        if (Attention.CRITICAL.equals(event.getAttentionClass())) {
            throw new IllegalArgumentException("attention intelligence never handles critical events");
        }
        Classification out = inner.classify(event);
        if (!Attention.STANDARD.equals(out.attentionClass()) && !Attention.BACKGROUND.equals(out.attentionClass())) {
            throw new IllegalStateException("classifier returned \"" + out.attentionClass()
                    + "\"; only standard or background are allowed");
        }
        if (out.score() < 0 || out.score() > 100 || out.confidence() < 0 || out.confidence() > 1) {
            throw new IllegalStateException("classifier returned a score or confidence out of range");
        }
        return out;
    }
}
