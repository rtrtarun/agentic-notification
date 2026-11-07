package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** What a /notify caller may ask the service to contact, and how a valid request is delivered. */
class NotifyTest {
    private static final Notify.Limits LIMITS = new Notify.Limits(
            List.of("hooks.slack.com", "*.webhook.office.com"), List.of());

    private static Notify.Request request(String name, String title, List<String> email, List<String> slack,
                                          List<String> teams, List<String> sms) {
        return new Notify.Request(new Notify.Agent(name, "", "grp"), title, "body", email, slack, teams, sms);
    }

    private static Notify.Request ok() {
        return request("agent", "Build failed", null, null, null, null);
    }

    private static String problem(Notify.Request r) {
        return assertThrows(Notify.InvalidRequestException.class, () -> LIMITS.validate(r)).getMessage();
    }

    @Test
    void aMinimalRequestIsValid() {
        LIMITS.validate(ok());
    }

    @Test
    void agentAndTitleAreRequired() {
        assertEquals("invalid notification request: agent name required", problem(request("", "t", null, null, null, null)));
        assertEquals("invalid notification request: title required", problem(request("a", "", null, null, null, null)));
        assertEquals("invalid notification request: agent name required",
                problem(new Notify.Request(null, null, null, null, null, null, null)), "a null body is an empty request");
    }

    @Test
    void theTitleIsOneShortLine() {
        String expected = "invalid notification request: title must be a single line of at most 200 characters";
        assertEquals(expected, problem(request("a", "two\nlines", null, null, null, null)));
        assertEquals(expected, problem(request("a", "x".repeat(201), null, null, null, null)));
        LIMITS.validate(request("a", "x".repeat(200), null, null, null, null));
        assertEquals(expected, problem(request("a", "é".repeat(101), null, null, null, null)), "the limit counts bytes, as in Go");
    }

    @Test
    void theMessageHasALimit() {
        Notify.Request big = new Notify.Request(new Notify.Agent("a", "", ""), "t", "x".repeat(20_001), null, null, null, null);
        assertEquals("invalid notification request: message exceeds 20000 characters", problem(big));
    }

    @Test
    void callersCannotChooseSmsNumbers() {
        assertEquals("invalid notification request: sms recipients are configured on the server and cannot be supplied",
                problem(request("a", "t", null, null, null, List.of("+15551234567"))));
    }

    @Test
    void recipientsAreCheckedForCountSyntaxAndDomain() {
        assertEquals("invalid notification request: too many email recipients",
                problem(request("a", "t", java.util.Collections.nCopies(21, "a@x.com"), null, null, null)));
        for (String bad : List.of("nobody", "a@", "@x.com", "Name <a@x.com>", "a@x.com\nBcc: b@x.com", "a b@x.com", "a@x@y.com")) {
            assertEquals("invalid notification request: invalid email recipient",
                    problem(request("a", "t", List.of(bad), null, null, null)), bad);
        }
        Notify.Limits restricted = new Notify.Limits(List.of(), List.of("corp.com"));
        restricted.validate(request("a", "t", List.of("a@corp.com"), null, null, null));
        assertEquals("invalid notification request: email domain not allowed",
                assertThrows(Notify.InvalidRequestException.class,
                        () -> restricted.validate(request("a", "t", List.of("a@other.com"), null, null, null))).getMessage());
    }

    @Test
    void webhookUrlsMustBeHttpsOnTheDefaultPortToAnAllowedHostWithoutCredentials() {
        String shape = "invalid notification request: webhook url must be an https url without credentials";
        for (String bad : List.of("http://hooks.slack.com/x", "ftp://hooks.slack.com/x", "https://user:pw@hooks.slack.com/x",
                "not a url", "https:///nohost", "//hooks.slack.com/x")) {
            assertEquals(shape, problem(request("a", "t", null, List.of(bad), null, null)), bad);
        }
        assertEquals("invalid notification request: webhook url must use the default https port",
                problem(request("a", "t", null, List.of("https://hooks.slack.com:8443/x"), null, null)));
        assertEquals("invalid notification request: webhook host not allowed",
                problem(request("a", "t", null, List.of("https://evil.example.com/x"), null, null)));
        assertEquals("invalid notification request: webhook host not allowed",
                problem(request("a", "t", null, List.of("https://hooks.slack.com.evil.com/x"), null, null)), "no suffix tricks");
        assertEquals("invalid notification request: webhook host not allowed",
                problem(request("a", "t", null, null, List.of("https://169.254.169.254/latest"), null)));
        assertEquals("invalid notification request: too many webhook urls",
                problem(request("a", "t", null, java.util.Collections.nCopies(6, "https://hooks.slack.com/x"),
                        java.util.Collections.nCopies(5, "https://hooks.slack.com/y"), null)));
        LIMITS.validate(request("a", "t", null, List.of("https://hooks.slack.com/services/T/B/x", "https://HOOKS.SLACK.COM:443/y"),
                List.of("https://acme.webhook.office.com/webhookb2/x"), null));
    }

    @Test
    void aValidRequestIsDeliveredToExactlyTheTargetsItNamed() {
        List<Notification> sent = new ArrayList<>();
        NotificationProvider provider = new NotificationProvider() {
            @Override public void send(Notification n) { sent.add(n); }
            @Override public String name() { return "test"; }
        };
        new Notify.Service(LIMITS, provider).send(request("agent-9", "Deploy done", List.of("a@x.com"),
                List.of("https://hooks.slack.com/x"), null, null));

        assertEquals(1, sent.size());
        Notification n = sent.get(0);
        assertEquals("Deploy done", n.title());
        assertEquals("agent-9", n.agentName());
        assertEquals("grp", n.agentGroup());
        assertEquals(List.of("a@x.com"), n.targets().email());
        assertEquals(List.of("https://hooks.slack.com/x"), n.targets().slack());
        assertTrue(n.targets().teams().isEmpty());
    }

    @Test
    void aRequestThatBreaksALimitIsNeverSentAndADeliveryFailureIsRaised() {
        List<Notification> sent = new ArrayList<>();
        NotificationProvider failing = new NotificationProvider() {
            @Override public void send(Notification n) { sent.add(n); throw new PortException("down", null); }
            @Override public String name() { return "test"; }
        };
        Notify.Service service = new Notify.Service(LIMITS, failing);
        assertThrows(Notify.InvalidRequestException.class, () -> service.send(request("", "t", null, null, null, null)));
        assertTrue(sent.isEmpty());
        assertThrows(PortException.class, () -> service.send(ok()));
    }
}
