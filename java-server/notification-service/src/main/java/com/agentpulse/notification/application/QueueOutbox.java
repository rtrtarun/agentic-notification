package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.DeliveryStore;
import com.agentpulse.notification.application.Ports.Outbox;
import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.Event;

import java.time.Clock;
import java.time.Instant;
import java.util.List;
import java.util.Map;

/**
 * Turns processed events into queued jobs by a plain route table: which channels receive which attention class.
 * No model is in this path. Critical events are never queued; they are delivered inline and only logged here.
 */
public final class QueueOutbox implements Outbox {
    private final DeliveryStore store;
    private final Targets targets;
    private final Map<String, List<String>> routes;
    private final Clock clock;

    /** @param routes maps an attention class ({@code standard}, {@code background}) to channel ids */
    public QueueOutbox(DeliveryStore store, Targets targets, Map<String, List<String>> routes, Clock clock) {
        this.store = store;
        this.targets = targets;
        this.routes = routes;
        this.clock = clock;
    }

    @Override
    public boolean routed(String attentionClass) {
        return routes.getOrDefault(attentionClass, List.of()).stream().anyMatch(c -> !targets.forChannel(c).isEmpty());
    }

    @Override
    public boolean routed(String attentionClass, List<String> override) {
        return channelsFor(attentionClass, override).stream().anyMatch(c -> !targets.forChannel(c).isEmpty());
    }

    /** A rule's own channels when it names any, otherwise the channels the class is routed to. */
    private List<String> channelsFor(String attentionClass, List<String> override) {
        return override.isEmpty() ? routes.getOrDefault(attentionClass, List.of()) : override;
    }

    @Override
    public void enqueue(Event event) {
        if (Attention.CRITICAL.equals(event.getAttentionClass())) {
            return;
        }
        Instant now = clock.instant();
        for (String channel : channelsFor(event.getAttentionClass(), event.getRouteChannels())) {
            if (targets.forChannel(channel).isEmpty()) {
                continue;
            }
            store.enqueue(Delivery.Job.pending(event.getId(), channel, event.getAttentionClass(), title(event),
                    body(event), event.getSource(), now));
        }
    }

    @Override
    public void recordDirect(Event event, String channels, RuntimeException sendError) {
        Instant now = clock.instant();
        String status = sendError == null ? Delivery.Status.SENT : Delivery.Status.DEAD;
        String error = sendError == null ? "" : Delivery.redact(sendError.getMessage());
        store.record(new Delivery.Job(Delivery.newId(), event.getId(), channels, event.getAttentionClass(), title(event),
                "", event.getSource(), status, 1, now, Instant.EPOCH, error, true, now, now));
    }

    static String title(Event e) {
        return "[" + e.getAttentionClass().toUpperCase() + "] " + e.getSource() + "/" + e.getEventType();
    }

    static String body(Event e) {
        String reason = e.getDecisionState() == null ? "" : e.getDecisionState().getReason();
        return e.getAttentionClass() + "\nSource: " + e.getSource() + "\nEvent: " + e.getEventType()
                + "\nRepository: " + e.getRepository() + "\nReason: " + reason;
    }
}
