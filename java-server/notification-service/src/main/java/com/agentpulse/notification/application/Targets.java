package com.agentpulse.notification.application;

import java.util.ArrayList;
import java.util.List;
import java.util.Set;

/**
 * Where each channel delivers. A channel with no targets is not configured and is never routed to. Targets come
 * from configuration (or, for /notify, from the validated request); they are never stored with a queued job.
 */
public record Targets(List<String> email, List<String> slack, List<String> teams) {
    public static final String EMAIL = "email";
    public static final String SLACK = "slack";
    public static final String TEAMS = "teams";
    private static final List<String> ORDER = List.of(EMAIL, SLACK, TEAMS);

    public Targets {
        email = email == null ? List.of() : List.copyOf(email);
        slack = slack == null ? List.of() : List.copyOf(slack);
        teams = teams == null ? List.of() : List.copyOf(teams);
    }

    public static Targets none() {
        return new Targets(List.of(), List.of(), List.of());
    }

    /** A channel's targets; empty for an unknown channel. */
    public List<String> forChannel(String channel) {
        return switch (channel) {
            case EMAIL -> email;
            case SLACK -> slack;
            case TEAMS -> teams;
            default -> List.of();
        };
    }

    /** Only the given channel's targets, so a notification built from it reaches that channel alone. */
    public Targets only(String channel) {
        return new Targets(EMAIL.equals(channel) ? email : null, SLACK.equals(channel) ? slack : null,
                TEAMS.equals(channel) ? teams : null);
    }

    /** The configured channels in a stable order. */
    public List<String> channels() {
        List<String> out = new ArrayList<>();
        for (String c : ORDER) {
            if (!forChannel(c).isEmpty()) {
                out.add(c);
            }
        }
        return out;
    }

    /** Turns {@code "slack, email"} into channel ids and rejects unknown names. */
    public static List<String> parseChannels(List<String> names) {
        List<String> out = new ArrayList<>();
        for (String raw : names) {
            String name = raw.trim().toLowerCase();
            if (name.isEmpty()) {
                continue;
            }
            if (!Set.copyOf(ORDER).contains(name)) {
                throw new IllegalArgumentException("unknown delivery channel \"" + raw + "\" (use email, slack or teams)");
            }
            out.add(name);
        }
        return out;
    }
}
