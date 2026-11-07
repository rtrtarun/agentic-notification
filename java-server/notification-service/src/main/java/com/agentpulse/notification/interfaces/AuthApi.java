package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Accounts.AccountException;
import com.agentpulse.notification.application.Accounts.User;
import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpExchange;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * Sign-in, self sign-up with admin approval, sessions and account management. Paths, status codes and bodies match
 * the Go service. There is no cross-origin (CORS) support in this service yet, so a console on another site cannot
 * use these endpoints from a browser; the Go service is what the console talks to.
 */
final class AuthApi {
    private static final Logger LOG = LoggerFactory.getLogger(AuthApi.class);
    static final String AUTH_PREFIX = "/api/auth";
    static final String USERS_PREFIX = "/api/users";
    static final String SESSION_COOKIE = ApiSupport.SESSION_COOKIE;
    private static final long LOGIN_RETRY_SECONDS = Duration.ofMinutes(15).toSeconds();
    private static final long SIGNUP_RETRY_SECONDS = Duration.ofHours(1).toSeconds();

    private final ApiSupport api;
    private final Accounts.Service accounts;

    AuthApi(ApiSupport api, Accounts.Service accounts) {
        this.api = api;
        this.accounts = accounts;
    }

    /** Routes everything under /api/auth and /api/users. */
    void handle(HttpExchange x) throws IOException {
        String path = x.getRequestURI().getPath();
        if (path.equals(AUTH_PREFIX + "/status")) {
            status(x);
        } else if (path.equals(AUTH_PREFIX + "/login")) {
            login(x);
        } else if (path.equals(AUTH_PREFIX + "/signup")) {
            signUp(x);
        } else if (path.equals(AUTH_PREFIX + "/logout")) {
            logout(x);
        } else if (path.equals(AUTH_PREFIX + "/me")) {
            me(x);
        } else if (path.equals(USERS_PREFIX) || path.startsWith(USERS_PREFIX + "/")) {
            users(x, path.substring(USERS_PREFIX.length()).replaceAll("^/+|/+$", ""));
        } else {
            api.error(x, 404, "not found");
        }
    }

    private void status(HttpExchange x) throws IOException {
        if (!"GET".equals(x.getRequestMethod())) {
            api.methodNotAllowed(x, "GET");
            return;
        }
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("sessions", true);
        body.put("needsSetup", !accounts.hasUsers());
        body.put("sso", Map.of("enabled", false));
        body.put("signup", accounts.signupEnabled());
        api.reply(x, 200, body);
    }

    private void login(HttpExchange x) throws IOException {
        if (!"POST".equals(x.getRequestMethod())) {
            api.methodNotAllowed(x, "POST");
            return;
        }
        Optional<JsonNode> in = api.readJson(x);
        if (in.isEmpty()) {
            return;
        }
        try {
            Accounts.Login login = accounts.login(text(in.get(), "email"), text(in.get(), "password"), clientIp(x));
            setCookie(x, login.token(), login.session().expiresAt());
            Map<String, Object> body = new LinkedHashMap<>();
            body.put("user", view(login.user()));
            body.put("csrfToken", login.session().csrf());
            body.put("expiresAt", DateTimeFormatter.ISO_INSTANT.format(login.session().expiresAt()));
            api.reply(x, 200, body);
        } catch (AccountException e) {
            switch (e.kind()) {
                case LOCKED -> {
                    x.getResponseHeaders().set("Retry-After", Long.toString(LOGIN_RETRY_SECONDS));
                    api.error(x, 429, e.getMessage());
                }
                case INVALID_CREDENTIALS -> api.error(x, 401, e.getMessage());
                case PENDING_APPROVAL -> api.reply(x, 403, Map.of("error", e.getMessage(), "code", "pending"));
                case ACCOUNT_REJECTED -> api.reply(x, 403, Map.of("error", e.getMessage(), "code", "rejected"));
                default -> failed(x, "login", e);
            }
        }
    }

    private void signUp(HttpExchange x) throws IOException {
        if (!"POST".equals(x.getRequestMethod())) {
            api.methodNotAllowed(x, "POST");
            return;
        }
        Optional<JsonNode> in = api.readJson(x);
        if (in.isEmpty()) {
            return;
        }
        try {
            accounts.signUp(text(in.get(), "email"), text(in.get(), "name"), text(in.get(), "password"), clientIp(x));
            api.reply(x, 202, Map.of("status", "pending",
                    "message", "your request was received; an admin must approve it before you can sign in"));
        } catch (AccountException e) {
            switch (e.kind()) {
                case SIGNUP_DISABLED -> api.error(x, 404, e.getMessage());
                case TOO_MANY_SIGNUPS -> {
                    x.getResponseHeaders().set("Retry-After", Long.toString(SIGNUP_RETRY_SECONDS));
                    api.error(x, 429, e.getMessage());
                }
                case INVALID_INPUT, WEAK_PASSWORD -> api.error(x, 400, e.getMessage());
                default -> failed(x, "sign-up", e);
            }
        }
    }

    private void logout(HttpExchange x) throws IOException {
        if (!"POST".equals(x.getRequestMethod())) {
            api.methodNotAllowed(x, "POST");
            return;
        }
        if (api.authenticate(x) == null) {
            return;
        }
        accounts.logout(api.sessionToken(x));
        clearCookie(x);
        api.replyEmpty(x, 204);
    }

    private void me(HttpExchange x) throws IOException {
        if (!"GET".equals(x.getRequestMethod())) {
            api.methodNotAllowed(x, "GET");
            return;
        }
        ApiSupport.Principal p = api.authenticate(x);
        if (p == null) {
            return;
        }
        Map<String, Object> body = new LinkedHashMap<>();
        if (p.service()) {
            body.put("via", "apikey");
            body.put("user", Map.of("id", "", "email", "api-key", "name", "Service", "role", Accounts.ROLE_ADMIN,
                    "disabled", false, "status", Accounts.STATUS_ACTIVE));
        } else {
            body.put("via", "session");
            body.put("user", view(p.user()));
            body.put("csrfToken", p.session().csrf());
        }
        api.reply(x, 200, body);
    }

    private void users(HttpExchange x, String id) throws IOException {
        ApiSupport.Principal p = api.authenticate(x);
        if (p == null) {
            return;
        }
        if (!p.admin()) {
            api.error(x, 403, "admin access required");
            return;
        }
        String method = x.getRequestMethod();
        try {
            if (id.isEmpty() && "GET".equals(method)) {
                List<Map<String, Object>> views = new ArrayList<>();
                for (User u : accounts.users()) {
                    views.add(view(u));
                }
                api.reply(x, 200, Map.of("users", views));
            } else if (id.isEmpty() && "POST".equals(method)) {
                Optional<JsonNode> in = api.readJson(x);
                if (in.isEmpty()) {
                    return;
                }
                String role = text(in.get(), "role");
                User created = accounts.createUser(p.actor(), text(in.get(), "email"), text(in.get(), "name"),
                        role.isEmpty() ? Accounts.ROLE_MEMBER : role, text(in.get(), "password"));
                api.reply(x, 201, view(created));
            } else if (!id.isEmpty() && !id.contains("/") && "PATCH".equals(method)) {
                Optional<JsonNode> in = api.readJson(x);
                if (in.isEmpty()) {
                    return;
                }
                JsonNode n = in.get();
                Accounts.Patch patch = new Accounts.Patch(optional(n, "name"), optional(n, "role"),
                        n.hasNonNull("disabled") ? n.get("disabled").asBoolean() : null, optional(n, "password"), optional(n, "status"));
                api.reply(x, 200, view(accounts.updateUser(p.actor(), id, patch)));
            } else {
                api.error(x, 405, "method not allowed");
            }
        } catch (AccountException e) {
            switch (e.kind()) {
                case INVALID_INPUT, WEAK_PASSWORD -> api.error(x, 400, e.getMessage());
                case EMAIL_TAKEN, LAST_ADMIN -> api.error(x, 409, e.getMessage());
                case NOT_FOUND -> api.error(x, 404, "user not found");
                default -> failed(x, "account request", e);
            }
        }
    }

    // ---- helpers ------------------------------------------------------------------------------------------

    private void failed(HttpExchange x, String what, RuntimeException e) throws IOException {
        LOG.error("{} failed error={}", what, e.getMessage());
        api.error(x, 500, "internal error");
    }

    private static Map<String, Object> view(User u) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", u.id());
        m.put("email", u.email());
        m.put("name", u.name());
        m.put("role", u.role());
        m.put("disabled", u.disabled());
        m.put("status", u.statusOrActive());
        return m;
    }

    private static String text(JsonNode n, String field) {
        JsonNode v = n.get(field);
        return v == null || v.isNull() ? "" : v.asText("");
    }

    private static String optional(JsonNode n, String field) {
        return n.hasNonNull(field) ? n.get(field).asText() : null;
    }

    /** The connecting address. Behind a proxy this is the proxy, so the per-address limits are then looser than intended. */
    private static String clientIp(HttpExchange x) {
        InetSocketAddress a = x.getRemoteAddress();
        return a == null || a.getAddress() == null ? "unknown" : a.getAddress().getHostAddress();
    }

    private void setCookie(HttpExchange x, String token, Instant expires) {
        long maxAge = Math.max(0, Duration.between(Instant.now(), expires).toSeconds());
        String expiresText = DateTimeFormatter.RFC_1123_DATE_TIME.format(expires.atZone(ZoneOffset.UTC));
        x.getResponseHeaders().add("Set-Cookie", SESSION_COOKIE + "=" + token + "; Path=/; Expires=" + expiresText
                + "; Max-Age=" + maxAge + "; HttpOnly" + secureFlag(x) + "; SameSite=" + api.sameSite());
    }

    private void clearCookie(HttpExchange x) {
        x.getResponseHeaders().add("Set-Cookie", SESSION_COOKIE + "=; Path=/; Max-Age=0; HttpOnly" + secureFlag(x)
                + "; SameSite=" + api.sameSite());
    }

    private String secureFlag(HttpExchange x) {
        return api.secureCookie(x) ? "; Secure" : "";
    }
}
