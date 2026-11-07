package com.agentpulse.notification.application;

import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.fasterxml.jackson.annotation.JsonIgnoreProperties;

import java.net.URI;
import java.net.URISyntaxException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.regex.Pattern;

/**
 * The generic agent notification entry point ({@code POST /notify}): the request, its validation and its delivery.
 * Caller-supplied webhook URLs and recipients are untrusted input: without limits they would let any caller make
 * the server POST to internal hosts (SSRF) or send mail anywhere. The limits and messages are the Go service's.
 */
public final class Notify {
    private Notify() {}

    /** Who is sending. */
    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Agent(String name, String owner, String group) {}

    /**
     * The request body. {@code smsRecipients} exists only so it can be refused: text messages cost money and reach
     * a phone, so callers may not choose the numbers.
     */
    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Request(Agent agent, String title, String message, List<String> emailRecipients,
                          List<String> slackWebhookUrls, List<String> teamsWebhookUrls, List<String> smsRecipients) {
        public Request {
            agent = agent == null ? new Agent("", "", "") : new Agent(nz(agent.name()), nz(agent.owner()), nz(agent.group()));
            title = nz(title);
            message = nz(message);
            emailRecipients = emailRecipients == null ? List.of() : List.copyOf(emailRecipients);
            slackWebhookUrls = slackWebhookUrls == null ? List.of() : List.copyOf(slackWebhookUrls);
            teamsWebhookUrls = teamsWebhookUrls == null ? List.of() : List.copyOf(teamsWebhookUrls);
            smsRecipients = smsRecipients == null ? List.of() : List.copyOf(smsRecipients);
        }

        private static String nz(String s) {
            return s == null ? "" : s;
        }
    }

    /** A request rejected for a reason the caller can fix; the HTTP layer answers 400 with the message. */
    public static final class InvalidRequestException extends RuntimeException {
        public InvalidRequestException(String detail) {
            super("invalid notification request: " + detail);
        }
    }

    /** What a caller may ask the service to contact. */
    public record Limits(List<String> allowedWebhookHosts, List<String> allowedEmailDomains, int maxRecipients,
                         int maxWebhooks) {
        public static final int DEFAULT_MAX_RECIPIENTS = 20;
        public static final int DEFAULT_MAX_WEBHOOKS = 10;
        static final int MAX_TITLE_BYTES = 200;
        static final int MAX_MESSAGE_BYTES = 20_000;
        // A plain address with no display name, no quoting and no line breaks (the Go service requires the same).
        private static final Pattern ADDRESS = Pattern.compile("^[^\\s@<>(),;:\\\\\"\\[\\]]+@[^\\s@<>(),;:\\\\\"\\[\\]]+$");

        public Limits(List<String> allowedWebhookHosts, List<String> allowedEmailDomains) {
            this(allowedWebhookHosts, allowedEmailDomains, DEFAULT_MAX_RECIPIENTS, DEFAULT_MAX_WEBHOOKS);
        }

        /** Throws {@link InvalidRequestException} when the request breaks a limit. */
        public void validate(Request req) {
            if (req.agent().name().isEmpty()) {
                throw new InvalidRequestException("agent name required");
            }
            if (req.title().isEmpty()) {
                throw new InvalidRequestException("title required");
            }
            if (bytes(req.title()) > MAX_TITLE_BYTES || hasLineBreak(req.title())) {
                throw new InvalidRequestException("title must be a single line of at most " + MAX_TITLE_BYTES + " characters");
            }
            if (bytes(req.message()) > MAX_MESSAGE_BYTES) {
                throw new InvalidRequestException("message exceeds " + MAX_MESSAGE_BYTES + " characters");
            }
            if (!req.smsRecipients().isEmpty()) {
                throw new InvalidRequestException("sms recipients are configured on the server and cannot be supplied");
            }
            validateRecipients(req.emailRecipients());
            List<String> webhooks = new ArrayList<>(req.slackWebhookUrls());
            webhooks.addAll(req.teamsWebhookUrls());
            validateWebhooks(webhooks);
        }

        private void validateRecipients(List<String> recipients) {
            if (recipients.size() > orDefault(maxRecipients, DEFAULT_MAX_RECIPIENTS)) {
                throw new InvalidRequestException("too many email recipients");
            }
            for (String rcpt : recipients) {
                if (rcpt == null || hasLineBreak(rcpt) || rcpt.length() > 254 || !ADDRESS.matcher(rcpt).matches()) {
                    throw new InvalidRequestException("invalid email recipient");
                }
                if (!allowedEmailDomains.isEmpty() && !matchesAny(rcpt.substring(rcpt.lastIndexOf('@') + 1).toLowerCase(Locale.ROOT),
                        allowedEmailDomains)) {
                    throw new InvalidRequestException("email domain not allowed");
                }
            }
        }

        private void validateWebhooks(List<String> urls) {
            if (urls.size() > orDefault(maxWebhooks, DEFAULT_MAX_WEBHOOKS)) {
                throw new InvalidRequestException("too many webhook urls");
            }
            for (String raw : urls) {
                URI u = parse(raw);
                if (u == null || !"https".equals(u.getScheme()) || u.getHost() == null || u.getUserInfo() != null) {
                    throw new InvalidRequestException("webhook url must be an https url without credentials");
                }
                if (u.getPort() != -1 && u.getPort() != 443) {
                    throw new InvalidRequestException("webhook url must use the default https port");
                }
                if (!matchesAny(u.getHost().toLowerCase(Locale.ROOT), allowedWebhookHosts)) {
                    throw new InvalidRequestException("webhook host not allowed");
                }
            }
        }

        private static URI parse(String raw) {
            try {
                return raw == null ? null : new URI(raw);
            } catch (URISyntaxException e) {
                return null;
            }
        }

        private static int bytes(String s) {
            return s.getBytes(StandardCharsets.UTF_8).length;
        }

        private static boolean hasLineBreak(String s) {
            return s.indexOf('\r') >= 0 || s.indexOf('\n') >= 0;
        }

        private static int orDefault(int v, int def) {
            return v <= 0 ? def : v;
        }

        /** True when the host equals a pattern or matches a "*.suffix" pattern. */
        static boolean matchesAny(String host, List<String> patterns) {
            for (String raw : patterns) {
                String p = raw.trim().toLowerCase(Locale.ROOT);
                if (p.equals(host) || (p.startsWith("*.") && host.endsWith(p.substring(1)))) {
                    return true;
                }
            }
            return false;
        }
    }

    /** Validates a request and delivers it to the targets it names. */
    public static final class Service {
        private final Limits limits;
        private final NotificationProvider provider;

        public Service(Limits limits, NotificationProvider provider) {
            this.limits = limits;
            this.provider = provider;
        }

        /**
         * @throws InvalidRequestException when the request breaks a limit
         * @throws Ports.PortException when delivery fails; every channel is still attempted
         */
        public void send(Request req) {
            limits.validate(req);
            provider.send(new Notification(req.title(), req.message(), req.agent().name(), req.agent().owner(),
                    req.agent().group(), new Targets(req.emailRecipients(), req.slackWebhookUrls(), req.teamsWebhookUrls())));
        }
    }
}
