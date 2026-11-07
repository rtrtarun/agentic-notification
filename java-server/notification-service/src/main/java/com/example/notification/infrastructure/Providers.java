package com.example.notification.infrastructure;

import com.example.notification.application.Ports.NotificationProvider;
import com.example.notification.application.Ports.Notification;
import com.example.notification.application.Ports.PortException;
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
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Properties;

/** The delivery providers behind {@link NotificationProvider}. Nothing outside this class knows how they send. */
public final class Providers {
    private static final Logger LOG = LoggerFactory.getLogger(Providers.class);
    private static final Duration TIMEOUT = Duration.ofSeconds(10);

    private Providers() {}

    /** Settings for the SMTP provider. */
    public record Smtp(String host, int port, String username, String password, String sender,
                       List<String> recipients, boolean requireStartTls) {}

    /** Sends the notification by email through SMTP. */
    public static NotificationProvider email(Smtp smtp) {
        return new NotificationProvider() {
            @Override
            public void send(Notification n) {
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
                    for (String recipient : smtp.recipients()) {
                        message.addRecipient(Message.RecipientType.TO, new InternetAddress(recipient));
                    }
                    message.setSubject(n.title().replaceAll("[\\r\\n]", " "));
                    message.setText(n.message());
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

    /** Posts the notification to a Slack incoming webhook. */
    public static NotificationProvider slack(String webhookUrl) {
        HttpClient client = HttpClient.newBuilder().connectTimeout(TIMEOUT).build();
        ObjectMapper json = new ObjectMapper();
        return new NotificationProvider() {
            @Override
            public void send(Notification n) {
                try {
                    String body = json.writeValueAsString(Map.of("text", "*" + n.title() + "*\n" + n.message()));
                    HttpRequest request = HttpRequest.newBuilder(URI.create(webhookUrl)).timeout(TIMEOUT)
                            .header("Content-Type", "application/json")
                            .POST(HttpRequest.BodyPublishers.ofString(body)).build();
                    HttpResponse<Void> response = client.send(request, HttpResponse.BodyHandlers.discarding());
                    if (response.statusCode() / 100 != 2) {
                        throw new PortException("slack answered " + response.statusCode(), null);
                    }
                } catch (JsonProcessingException e) {
                    throw new PortException("encode slack message", e);
                } catch (IOException e) {
                    throw new PortException("send slack message", e);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    throw new PortException("interrupted sending slack message", e);
                }
            }

            @Override
            public String name() {
                return "slack";
            }
        };
    }

    /** Only logs. Used when no channel is configured, so a critical event is still visibly not lost silently. */
    public static NotificationProvider log() {
        return new NotificationProvider() {
            @Override
            public void send(Notification n) {
                LOG.warn("no delivery channel configured; critical notification only logged: {}", n.title());
            }

            @Override
            public String name() {
                return "log";
            }
        };
    }

    /**
     * Sends through every provider. A critical event counts as delivered when at least one channel took it, and
     * fails only when all of them failed, so one broken channel never hides a security alert.
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
                        LOG.warn("channel failed channel={} error={}", p.name(), e.getMessage());
                        failures.add(p.name() + ": " + e.getMessage());
                    }
                }
                if (failures.size() == providers.size()) {
                    throw new PortException("every channel failed: " + String.join("; ", failures), null);
                }
            }

            @Override
            public String name() {
                return providers.stream().map(NotificationProvider::name).reduce((a, b) -> a + "," + b).orElse("none");
            }
        };
    }
}
