package com.agentpulse.notification.infrastructure;

import com.agentpulse.notification.application.Delivery;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.Ports.PortException;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import jakarta.mail.Authenticator;
import jakarta.mail.Message;
import jakarta.mail.MessagingException;
import jakarta.mail.PasswordAuthentication;
import jakarta.mail.Session;
import jakarta.mail.Transport;
import jakarta.mail.internet.InternetAddress;
import jakarta.mail.internet.MimeMessage;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.InetAddress;
import java.net.URI;
import java.net.UnknownHostException;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Properties;

/**
 * The delivery providers behind {@link NotificationProvider}. Each sends to the targets the notification carries
 * and does nothing when it carries none for its channel. Nothing outside this class knows how they send.
 */
public final class Providers {
    private static final Logger LOG = LoggerFactory.getLogger(Providers.class);
    private static final Duration TIMEOUT = Duration.ofSeconds(10);
    private static final ObjectMapper JSON = new ObjectMapper();

    private Providers() {}

    /** Settings for the SMTP provider. */
    public record Smtp(String host, int port, String username, String password, String sender, boolean requireStartTls) {}

    /** Sends the notification by email to its email targets. A no-op without recipients. */
    public static NotificationProvider email(Smtp smtp) {
        return new NotificationProvider() {
            @Override
            public void send(Notification n) {
                if (n.targets().email().isEmpty()) {
                    return;
                }
                Properties props = new Properties();
                props.put("mail.smtp.host", smtp.host());
                props.put("mail.smtp.port", String.valueOf(smtp.port()));
                props.put("mail.smtp.auth", String.valueOf(!smtp.username().isEmpty()));
                props.put("mail.smtp.starttls.enable", "true");
                props.put("mail.smtp.starttls.required", String.valueOf(smtp.requireStartTls()));
                props.put("mail.smtp.connectiontimeout", String.valueOf(TIMEOUT.toMillis()));
                props.put("mail.smtp.timeout", String.valueOf(TIMEOUT.toMillis()));
                Session session = Session.getInstance(props, new Authenticator() {
                    @Override
                    protected PasswordAuthentication getPasswordAuthentication() {
                        return new PasswordAuthentication(smtp.username(), smtp.password());
                    }
                });
                try {
                    MimeMessage message = new MimeMessage(session);
                    message.setFrom(new InternetAddress(smtp.sender()));
                    for (String recipient : n.targets().email()) {
                        message.addRecipient(Message.RecipientType.TO, new InternetAddress(recipient, false));
                    }
                    message.setSubject(oneLine(n.title()));
                    message.setText(n.message() + "\r\n\r\nAgent: " + n.agentName() + "\r\nOwner: " + n.agentOwner()
                            + "\r\nGroup: " + n.agentGroup());
                    Transport.send(message);
                } catch (MessagingException e) {
                    throw new PortException("send email: " + e.getMessage(), e);
                }
            }

            @Override
            public String name() {
                return "email";
            }
        };
    }

    /**
     * Posts {@code {"text": ...}} to every Slack and Teams webhook URL on the notification. A failing URL does not
     * stop the others. Redirects are never followed (they could point at a host the allow-list never saw). With
     * {@code blockPrivate}, a URL whose host resolves to a loopback, private or link-local address is refused.
     */
    public static NotificationProvider webhook(boolean blockPrivate) {
        HttpClient client = HttpClient.newBuilder().connectTimeout(TIMEOUT).followRedirects(HttpClient.Redirect.NEVER).build();
        return new NotificationProvider() {
            @Override
            public void send(Notification n) {
                List<String> urls = new ArrayList<>(n.targets().slack());
                urls.addAll(n.targets().teams());
                if (urls.isEmpty()) {
                    return;
                }
                String body;
                try {
                    body = JSON.writeValueAsString(Map.of("text", n.message() + "\n\nAgent: " + n.agentName()
                            + "\nGroup: " + n.agentGroup()));
                } catch (JsonProcessingException e) {
                    throw new PortException("encode webhook message", e);
                }
                List<String> failures = new ArrayList<>();
                for (String url : urls) {
                    try {
                        post(url, body);
                    } catch (RuntimeException e) {
                        failures.add(e.getMessage());
                    }
                }
                if (!failures.isEmpty()) {
                    throw new PortException(String.join("; ", failures), null);
                }
            }

            private void post(String url, String body) {
                URI uri = URI.create(url);
                if (blockPrivate) {
                    refusePrivate(uri.getHost());
                }
                try {
                    HttpRequest request = HttpRequest.newBuilder(uri).timeout(TIMEOUT)
                            .header("Content-Type", "application/json")
                            .POST(HttpRequest.BodyPublishers.ofString(body)).build();
                    HttpResponse<Void> response = client.send(request, HttpResponse.BodyHandlers.discarding());
                    if (response.statusCode() / 100 != 2) {
                        throw new PortException("webhook returned status " + response.statusCode(), null);
                    }
                } catch (IOException e) {
                    throw new PortException("post webhook " + Delivery.redact(url) + ": " + e.getClass().getSimpleName(), e);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    throw new PortException("interrupted posting webhook", e);
                }
            }

            @Override
            public String name() {
                return "webhook";
            }
        };
    }

    /**
     * Resolves the host and refuses any address that is not public. This is checked just before connecting, not at
     * dial time as the Go service does, so a DNS answer that changes in between is not caught; the allow-list of
     * hosts is the first defence and this is the second.
     */
    static void refusePrivate(String host) {
        try {
            for (InetAddress a : InetAddress.getAllByName(host)) {
                if (a.isLoopbackAddress() || a.isSiteLocalAddress() || a.isLinkLocalAddress() || a.isAnyLocalAddress()
                        || a.isMulticastAddress()) {
                    throw new PortException("webhook target resolves to a blocked address", null);
                }
            }
        } catch (UnknownHostException e) {
            throw new PortException("webhook host cannot be resolved", e);
        }
    }

    /**
     * Sends through every provider and attempts all of them even when one fails, so a broken email channel cannot
     * stop a critical Slack alert. It then fails if any failed (the failures are joined), exactly as the Go
     * service's multi-provider does, so the caller never records a delivery that only partly happened.
     */
    public static NotificationProvider all(List<NotificationProvider> providers) {
        return new NotificationProvider() {
            @Override
            public void send(Notification n) {
                List<String> failures = new ArrayList<>();
                for (NotificationProvider p : providers) {
                    try {
                        p.send(n);
                    } catch (RuntimeException e) {
                        LOG.warn("channel failed channel={} error={}", p.name(), Delivery.redact(e.getMessage()));
                        failures.add(p.name() + ": " + e.getMessage());
                    }
                }
                if (!failures.isEmpty()) {
                    throw new PortException(String.join("; ", failures), null);
                }
            }

            @Override
            public String name() {
                return String.join("+", providers.stream().map(NotificationProvider::name).toList());
            }
        };
    }

    private static String oneLine(String s) {
        return s.replace('\r', ' ').replace('\n', ' ');
    }
}
