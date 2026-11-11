package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Classification;
import com.agentpulse.notification.application.Ports.Classifier;
import com.agentpulse.notification.domain.Attention;
import com.agentpulse.notification.domain.DecisionState;
import com.agentpulse.notification.domain.Event;
import com.agentpulse.notification.domain.Status;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

/**
 * The built-in, offline Attention Intelligence: it classifies, summarises and explains. Deterministic: the same
 * event always gets the same score, and every point of it comes with a reason a person can read. A port of the Go
 * service's {@code pkg/attention/heuristic.go}; the shared contract file {@code 13-attention.json} keeps the two the
 * same. It never answers critical and is never given a critical event.
 */
public final class Heuristic implements Classifier {
    /** Names the built-in classifier in an event's decision. */
    public static final String MODEL = "heuristic-v1";
    /** The score of an event nothing is known about. */
    public static final int BASE_SCORE = 50;
    /** Below this score an event is background. */
    public static final int BACKGROUND_BELOW = 35;

    private static final Set<String> SOCIAL_TYPES = Set.of("star", "watch", "fork", "public", "member", "gollum");
    private static final Set<String> CHATTER_ACTIONS = Set.of("labeled", "unlabeled", "assigned", "unassigned", "edited",
            "milestoned", "demilestoned", "synchronize", "converted_to_draft");
    private static final Set<String> CHECK_TYPES = Set.of("check_run", "workflow_run", "check_suite");

    private volatile Tuning.Snapshot tuning;

    /**
     * Lets the heuristic add the approved, learned score offsets in the snapshot. Without one, or while the
     * snapshot is empty or stale, it scores exactly as it always did.
     */
    public Heuristic withTuning(Tuning.Snapshot snapshot) {
        this.tuning = snapshot;
        return this;
    }

    // ---- classify ------------------------------------------------------------------------------------------

    /** Scores an event from what it is and who sent it. Below 35 it is background; otherwise standard. */
    @Override
    public Classification classify(Event event) {
        if (Attention.CRITICAL.equals(event.getAttentionClass())) {
            throw new IllegalArgumentException("attention intelligence never handles critical events");
        }
        int score = BASE_SCORE;
        List<String> reasons = new ArrayList<>();
        Map<String, Object> payload = event.getPayload();
        String type = event.getEventType().toLowerCase(Locale.ROOT);
        String action = str(payload, "action").toLowerCase(Locale.ROOT);

        if (SOCIAL_TYPES.contains(type)) {
            score -= 35;
            reasons.add("social activity");
        }
        if (CHATTER_ACTIONS.contains(action)) {
            score -= 20;
            reasons.add("routine change (" + action + ")");
        }
        if (isBot(payload)) {
            score -= 25;
            reasons.add("automated sender");
        }
        if (payload.get("pull_request") instanceof Map<?, ?> pr && Boolean.TRUE.equals(pr.get("draft"))) {
            score -= 20;
            reasons.add("draft pull request");
        }
        if (action.equals("review_requested")) {
            score += 25;
            reasons.add("someone asked for your review");
        } else if (type.equals("pull_request_review") && str(payload, "review", "state").equalsIgnoreCase("changes_requested")) {
            score += 20;
            reasons.add("changes were requested");
        } else if (CHECK_TYPES.contains(type) && isFailure(payload)) {
            score += 30;
            reasons.add("a check failed");
        } else if ((type.equals("pull_request") || type.equals("issues")) && action.equals("opened")) {
            score += 10;
            reasons.add("something new was opened");
        } else if (type.equals("issue_comment") || type.equals("pull_request_review_comment")) {
            score += 10;
            reasons.add("a person commented");
        }
        String model = MODEL;
        Tuning.Snapshot snapshot = tuning;
        Tuning.Match learned = snapshot == null ? null : snapshot.lookup(event);
        if (learned != null) {
            int delta = learned.adjustment().delta();
            score += delta;
            reasons.add("learned adjustment " + (delta >= 0 ? "+" : "") + delta + ": " + learned.adjustment().summary());
            model = Tuning.tunedModel(learned.version());
        }
        score = Math.max(0, Math.min(100, score));

        String cls = Attention.STANDARD;
        double confidence = 0.6;
        if (score < BACKGROUND_BELOW) {
            cls = Attention.BACKGROUND;
            confidence = clamp(0.55 + (BACKGROUND_BELOW - score) / 100.0, 0.55, 0.95);
        } else if (score > BASE_SCORE) {
            confidence = clamp(0.6 + (score - BASE_SCORE) / 100.0, 0.6, 0.95);
        }
        String reason = reasons.isEmpty() ? "scored " + score : "scored " + score + ": " + String.join(", ", reasons);
        return new Classification(cls, model, confidence, reason, score);
    }

    // ---- summarise -----------------------------------------------------------------------------------------

    /** One digest group: events that belong together. */
    public record Group(String key, String title, String summary, int count, List<String> eventIds) {}

    /** A short description of a set of events, grouped. */
    public record Summary(String title, String text, List<Group> groups) {}

    /** Groups events by source and repository and describes each group. Critical events are skipped. */
    public Summary summarize(List<Event> events) {
        Map<String, String> titles = new LinkedHashMap<>();
        Map<String, List<String>> ids = new LinkedHashMap<>();
        Map<String, Map<String, Integer>> types = new LinkedHashMap<>();
        Map<String, Integer> sources = new LinkedHashMap<>();
        for (Event e : events) {
            if (Attention.CRITICAL.equals(e.getAttentionClass())) {
                continue; // a critical event is never summarised; it was sent at once
            }
            String key = e.getSource();
            String title = e.getSource();
            if (!e.getRepository().isEmpty()) {
                key = e.getSource() + "/" + e.getRepository();
                title = e.getRepository() + " (" + e.getSource() + ")";
            }
            titles.putIfAbsent(key, title);
            ids.computeIfAbsent(key, k -> new ArrayList<>()).add(e.getId());
            types.computeIfAbsent(key, k -> new LinkedHashMap<>()).merge(e.getEventType(), 1, Integer::sum);
            sources.merge(e.getSource(), 1, Integer::sum);
        }
        List<Group> groups = new ArrayList<>();
        int total = 0;
        for (String key : titles.keySet()) {
            int n = ids.get(key).size();
            groups.add(new Group(key, titles.get(key), plural(n, "notification") + ": " + counts(types.get(key), 3), n, ids.get(key)));
            total += n;
        }
        groups.sort(Comparator.comparingInt(Group::count).reversed().thenComparing(Group::key));
        if (total == 0) {
            return new Summary("Nothing held back", "Nothing was held back.", groups);
        }
        String text = plural(total, "notification") + " held back, from " + counts(sources, 3) + ".";
        text += " Busiest: " + groups.get(0).title() + " with " + groups.get(0).count() + ".";
        return new Summary(plural(total, "notification") + " held back", text, groups);
    }

    // ---- explain -------------------------------------------------------------------------------------------

    /** Says in a few sentences why the event was handled the way it was. */
    public String explain(Event event) {
        DecisionState state = event.getDecisionState();
        if (state == null) {
            return "No decision has been recorded for this event.";
        }
        List<String> parts = new ArrayList<>();
        boolean critical = Attention.CRITICAL.equals(event.getAttentionClass());
        if (critical) {
            parts.add("This is critical, so it was sent to every channel at once. No judgement was involved and nothing can mute it.");
            parts.add(ruleSentence(state));
        } else if (Status.MERGED.equals(state.getStatus())) {
            parts.add("This repeated an earlier notification, so it was merged into it instead of interrupting you again.");
            if (!event.getMergedInto().isEmpty()) {
                parts.add("The first one is " + event.getMergedInto() + ".");
            }
        } else if (Status.DIGEST.equals(state.getStatus())) {
            parts.add("It was held for your digest instead of being sent now.");
            parts.add(ruleSentence(state));
        } else if (Status.BUDGET_EXCEEDED.equals(state.getStatus())) {
            parts.add("You had already been interrupted as often as your attention budget allows this hour, so this was held for your next digest instead of interrupting you again. It was not dropped.");
            parts.add(ruleSentence(state));
        } else if (Status.DELAYED.equals(state.getStatus())) {
            parts.add("It was held until quiet hours end.");
            parts.add(ruleSentence(state));
        } else {
            parts.add("It was kept as a " + event.getAttentionClass() + " notification and queued.");
            parts.add(ruleSentence(state));
        }
        if (!state.getModelVersion().isEmpty()) {
            parts.add(String.format(Locale.ROOT, "Attention Intelligence (%s) refined this decision with %.0f%% confidence.",
                    state.getModelVersion(), state.getConfidence() * 100));
        } else if (!critical) {
            parts.add("A rule or a choice decided this; Attention Intelligence was not used.");
        }
        return String.join(" ", parts);
    }

    private static String ruleSentence(DecisionState s) {
        if (!s.getMatchedRule().isEmpty() && !s.getReason().isEmpty()) {
            return "The rule \"" + s.getMatchedRule() + "\" applied: " + trimDots(s.getReason()) + ".";
        }
        if (!s.getReason().isEmpty()) {
            return trimDots(s.getReason()) + ".";
        }
        return "No rule matched, so the default applied.";
    }

    private static String trimDots(String s) {
        int end = s.length();
        while (end > 0 && s.charAt(end - 1) == '.') {
            end--;
        }
        return s.substring(0, end);
    }

    // ---- helpers -------------------------------------------------------------------------------------------

    private static String str(Map<String, Object> m, String... path) {
        Object cur = m;
        for (String p : path) {
            if (!(cur instanceof Map<?, ?> next)) {
                return "";
            }
            cur = next.get(p);
        }
        return cur instanceof String s ? s : "";
    }

    private static boolean isBot(Map<String, Object> payload) {
        if (str(payload, "sender", "type").equalsIgnoreCase("bot")) {
            return true;
        }
        return str(payload, "sender", "login").toLowerCase(Locale.ROOT).endsWith("[bot]");
    }

    private static boolean isFailure(Map<String, Object> payload) {
        for (String key : List.of("check_run", "workflow_run", "check_suite")) {
            String c = str(payload, key, "conclusion").toLowerCase(Locale.ROOT);
            if (c.equals("failure") || c.equals("timed_out") || c.equals("cancelled")) {
                return true;
            }
        }
        return false;
    }

    private static double clamp(double v, double lo, double hi) {
        return Math.max(lo, Math.min(hi, v));
    }

    static String plural(int n, String noun) {
        return n == 1 ? "1 " + noun : n + " " + noun + "s";
    }

    /** Renders "3 pull_request, 2 push" for the biggest entries, with "and N more" when cut. */
    static String counts(Map<String, Integer> m, int top) {
        List<Map.Entry<String, Integer>> list = new ArrayList<>();
        for (Map.Entry<String, Integer> e : m.entrySet()) {
            list.add(Map.entry(e.getKey().isEmpty() ? "other" : e.getKey(), e.getValue()));
        }
        list.sort(Comparator.<Map.Entry<String, Integer>>comparingInt(Map.Entry::getValue).reversed().thenComparing(Map.Entry::getKey));
        List<String> out = new ArrayList<>();
        for (int i = 0; i < list.size(); i++) {
            if (i == top) {
                out.add("and " + (list.size() - top) + " more");
                break;
            }
            out.add(list.get(i).getValue() + " " + list.get(i).getKey());
        }
        return String.join(", ", out);
    }
}
