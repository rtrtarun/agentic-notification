package com.example.notification.application;

import com.example.notification.application.Ports.Adjustment;
import com.example.notification.application.Ports.Budget;
import com.example.notification.application.Ports.BudgetResult;
import com.example.notification.application.Ports.Classification;
import com.example.notification.application.Ports.Classifier;
import com.example.notification.application.Ports.EventRepository;
import com.example.notification.application.Ports.Notification;
import com.example.notification.application.Ports.NotificationProvider;
import com.example.notification.application.Ports.Outbox;
import com.example.notification.application.Ports.PortException;
import com.example.notification.application.Ports.Preferences;
import com.example.notification.domain.Event;
import com.example.notification.domain.Rule;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/** Test doubles that record what the pipeline did with them. */
final class Fakes {
    private Fakes() {}

    /** The rules every pipeline test uses: two locked critical rules, a background rule, and a standard default. */
    static PolicyEngine engine() {
        return new PolicyEngine(List.of(
                new Rule("1", "advisory", "", new Rule.Condition("github", "security_advisory", ""),
                        new Rule.Outcome("critical", "security vulnerability detected", true), false, true),
                new Rule("2", "star", "", new Rule.Condition("github", "star", ""),
                        new Rule.Outcome("background", "social signal", false), false, false),
                new Rule("3", "explicit", "", new Rule.Condition("github", "release", ""),
                        new Rule.Outcome("standard", "explicit choice", false), false, false),
                new Rule("4", "default", "", new Rule.Condition("github", "", ""),
                        new Rule.Outcome("standard", "default routing", true), false, false)));
    }

    static Event event(String id, String type, Map<String, Object> payload) {
        return new Event(id, "github", type, "acme/api", payload);
    }

    static Event event(String id, String type) {
        return event(id, type, new LinkedHashMap<>());
    }

    static Event pullRequest(String id, int number) {
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("action", "opened");
        payload.put("number", number);
        return event(id, "pull_request", payload);
    }

    static final class RecordingProvider implements NotificationProvider {
        final List<Notification> sent = new ArrayList<>();
        final List<String> log;
        boolean fail;

        RecordingProvider(List<String> log) {
            this.log = log;
        }

        @Override
        public void send(Notification n) {
            if (fail) {
                throw new PortException("channel down", null);
            }
            sent.add(n);
            log.add("deliver");
        }

        @Override
        public String name() {
            return "recording";
        }
    }

    static final class RecordingRepository implements EventRepository {
        final List<Event> saved = new ArrayList<>();
        final List<String> log;
        boolean failSave;

        RecordingRepository(List<String> log) {
            this.log = log;
        }

        @Override
        public void save(Event event) {
            if (failSave) {
                throw new PortException("database down", null);
            }
            saved.add(event);
            log.add("save");
        }

        @Override
        public boolean exists(String id) {
            return saved.stream().anyMatch(e -> e.getId().equals(id));
        }

        @Override
        public List<Event> listRecent(int limit) {
            return saved;
        }
    }

    static final class CountingClassifier implements Classifier {
        int calls;
        String answer = "background";
        boolean fail;

        @Override
        public Classification classify(Event event) {
            calls++;
            if (fail) {
                throw new PortException("model down", null);
            }
            return new Classification(answer, "test-model", 0.9, "answered " + answer);
        }
    }

    static final class CountingPreferences implements Preferences {
        int calls;
        Adjustment answer;

        @Override
        public Optional<Adjustment> decide(Event event) {
            calls++;
            return Optional.ofNullable(answer);
        }
    }

    static final class CountingBudget implements Budget {
        int calls;
        boolean allow = true;
        boolean fail;

        @Override
        public BudgetResult take() {
            calls++;
            if (fail) {
                throw new PortException("budget store down", null);
            }
            return new BudgetResult(allow, allow ? 1 : 10, 10);
        }
    }

    static final class CountingOutbox implements Outbox {
        int enqueued;
        boolean routedStandard = true;
        boolean routedBackground = true;
        boolean fail;

        @Override
        public boolean routed(String attentionClass) {
            return "background".equals(attentionClass) ? routedBackground : routedStandard;
        }

        @Override
        public void enqueue(Event event) {
            if (fail) {
                throw new PortException("queue down", null);
            }
            enqueued++;
        }
    }
}
