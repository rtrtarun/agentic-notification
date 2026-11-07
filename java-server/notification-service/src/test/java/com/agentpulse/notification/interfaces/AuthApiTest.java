package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Accounts;
import com.agentpulse.notification.application.Notify;
import com.agentpulse.notification.application.Pipeline;
import com.agentpulse.notification.application.PolicyEngine;
import com.agentpulse.notification.application.Ports.EventRepository;
import com.agentpulse.notification.application.Ports.Notification;
import com.agentpulse.notification.application.Ports.NotificationProvider;
import com.agentpulse.notification.application.RulesService;
import com.agentpulse.notification.domain.Rule;
import com.agentpulse.notification.infrastructure.Config;
import com.agentpulse.notification.infrastructure.InMemoryEventRepository;
import com.agentpulse.notification.infrastructure.MemoryAccountStore;
import com.agentpulse.notification.infrastructure.MemoryStores;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Clock;
import java.time.Duration;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Sign-in, sign-up with admin approval, sessions and roles through a real HTTP server. */
class AuthApiTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final String PASSWORD = "correct horse battery";
    private static final Map<String, String> KEY = Map.of("X-API-Key", "k");
    private static final List<Rule> SEED = List.of(new Rule("", "default", "", new Rule.Condition("github", "", ""),
            new Rule.Outcome("standard", "default routing", false), false, false));
    private static final NotificationProvider OK = new NotificationProvider() {
        @Override public void send(Notification n) { }
        @Override public String name() { return "ok"; }
    };

    private final HttpClient client = HttpClient.newHttpClient();
    private HttpServer server;
    private Accounts.Service accounts;
    private String base;

    @AfterEach
    void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private void start(boolean signup, Map<String, String> extraEnv) throws Exception {
        Map<String, String> env = new HashMap<>(Map.of("API_KEY", "k", "ALLOW_UNSIGNED_WEBHOOKS", "true"));
        env.putAll(extraEnv);
        Config config = Config.load(env);
        EventRepository repo = new InMemoryEventRepository(Clock.systemUTC());
        PolicyEngine engine = new PolicyEngine(List.of());
        RulesService rules = new RulesService(new MemoryStores.Rules(), engine, SEED);
        accounts = new Accounts.Service(new MemoryAccountStore(), Duration.ofHours(1), Clock.systemUTC()).withSignup(signup);
        accounts.bootstrap("admin@corp.com", PASSWORD);
        server = new HttpApi(config, new Pipeline(engine, OK, repo), repo, new Notify.Service(config.notifyLimits(), OK), rules,
                new MemoryStores.Deliveries(), Clock.systemUTC(), accounts).start(0);
        base = "http://localhost:" + server.getAddress().getPort();
    }

    private HttpResponse<String> call(String method, String path, String body, Map<String, String> headers) throws Exception {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(base + path))
                .method(method, HttpRequest.BodyPublishers.ofString(body == null ? "" : body));
        headers.forEach(b::header);
        return client.send(b.build(), HttpResponse.BodyHandlers.ofString());
    }

    private static JsonNode json(HttpResponse<String> r) throws Exception {
        return JSON.readTree(r.body());
    }

    /** A signed-in browser: the cookie and the CSRF token. */
    private record Browser(String cookie, String csrf) {
        Map<String, String> reads() {
            return Map.of("Cookie", cookie);
        }

        Map<String, String> writes() {
            return Map.of("Cookie", cookie, "X-CSRF-Token", csrf);
        }
    }

    private Browser signIn(String email, String password) throws Exception {
        HttpResponse<String> r = call("POST", "/api/auth/login", "{\"email\":\"" + email + "\",\"password\":\"" + password + "\"}", Map.of());
        assertEquals(200, r.statusCode(), r.body());
        String setCookie = r.headers().firstValue("Set-Cookie").orElseThrow();
        return new Browser(setCookie.substring(0, setCookie.indexOf(';')), json(r).get("csrfToken").asText());
    }

    private String idOf(String email) throws Exception {
        for (JsonNode u : json(call("GET", "/api/users", null, KEY)).get("users")) {
            if (u.get("email").asText().equals(email)) {
                return u.get("id").asText();
            }
        }
        throw new AssertionError("no user " + email);
    }

    private void addMember(String email) throws Exception {
        assertEquals(201, call("POST", "/api/users", "{\"email\":\"" + email + "\",\"role\":\"member\",\"password\":\"" + PASSWORD + "\"}", KEY).statusCode());
    }

    @Test
    void statusIsPublicAndSaysWhetherSignUpIsOn() throws Exception {
        start(false, Map.of());
        JsonNode off = json(call("GET", "/api/auth/status", null, Map.of()));
        assertFalse(off.get("signup").asBoolean());
        assertFalse(off.get("needsSetup").asBoolean());
        assertEquals(405, call("POST", "/api/auth/status", "", Map.of()).statusCode());
        accounts.withSignup(true);
        assertTrue(json(call("GET", "/api/auth/status", null, Map.of())).get("signup").asBoolean());
    }

    @Test
    void signUpIsRefusedWhileOff() throws Exception {
        start(false, Map.of());
        assertEquals(404, call("POST", "/api/auth/signup", "{\"email\":\"x@corp.com\",\"password\":\"" + PASSWORD + "\"}", Map.of()).statusCode());
    }

    @Test
    void signUpThenAdminApprovalThenSignIn() throws Exception {
        start(true, Map.of());
        HttpResponse<String> up = call("POST", "/api/auth/signup", "{\"email\":\"New@Corp.com\",\"name\":\"Newbie\",\"password\":\"" + PASSWORD + "\"}", Map.of());
        assertEquals(202, up.statusCode(), up.body());
        assertEquals("pending", json(up).get("status").asText());
        assertTrue(up.headers().firstValue("Set-Cookie").isEmpty(), "sign-up must never start a session");
        assertFalse(up.body().contains(PASSWORD) || up.body().contains("argon2"));

        HttpResponse<String> pending = call("POST", "/api/auth/login", "{\"email\":\"new@corp.com\",\"password\":\"" + PASSWORD + "\"}", Map.of());
        assertEquals(403, pending.statusCode());
        assertEquals("pending", json(pending).get("code").asText());
        assertTrue(pending.headers().firstValue("Set-Cookie").isEmpty());

        String list = call("GET", "/api/users", null, KEY).body();
        assertTrue(list.contains("\"status\":\"pending\""), list);
        HttpResponse<String> approve = call("PATCH", "/api/users/" + idOf("new@corp.com"), "{\"status\":\"active\"}", KEY);
        assertEquals(200, approve.statusCode(), approve.body());
        assertEquals("active", json(approve).get("status").asText());

        Browser b = signIn("new@corp.com", PASSWORD);
        assertEquals(200, call("GET", "/events", null, b.reads()).statusCode(), "an approved member can read events");
    }

    @Test
    void badAndRepeatSignUpsAreHandledWithoutRevealingAccounts() throws Exception {
        start(true, Map.of());
        assertEquals(400, call("POST", "/api/auth/signup", "{\"email\":\"nope\",\"password\":\"" + PASSWORD + "\"}", Map.of()).statusCode());
        assertEquals(400, call("POST", "/api/auth/signup", "{\"email\":\"w@corp.com\",\"password\":\"short\"}", Map.of()).statusCode());
        assertEquals(400, call("POST", "/api/auth/signup", "{}", Map.of()).statusCode());
        assertEquals(405, call("GET", "/api/auth/signup", null, Map.of()).statusCode());
        String body = "{\"email\":\"dup@corp.com\",\"password\":\"" + PASSWORD + "\"}";
        HttpResponse<String> first = call("POST", "/api/auth/signup", body, Map.of());
        HttpResponse<String> second = call("POST", "/api/auth/signup", body, Map.of());
        HttpResponse<String> admin = call("POST", "/api/auth/signup", "{\"email\":\"admin@corp.com\",\"password\":\"" + PASSWORD + "\"}", Map.of());
        assertEquals(first.statusCode(), second.statusCode());
        assertEquals(first.statusCode(), admin.statusCode());
        assertEquals(first.body(), admin.body(), "an existing email must get the same answer as a new one");
        signIn("admin@corp.com", PASSWORD); // the admin account was not touched
    }

    @Test
    void signInSetsAHardenedCookieAndSignOutEndsTheSession() throws Exception {
        start(false, Map.of());
        HttpResponse<String> r = call("POST", "/api/auth/login", "{\"email\":\"admin@corp.com\",\"password\":\"" + PASSWORD + "\"}", Map.of());
        String cookie = r.headers().firstValue("Set-Cookie").orElseThrow();
        assertTrue(cookie.startsWith("attn_session="), cookie);
        assertTrue(cookie.contains("HttpOnly") && cookie.contains("SameSite=Lax") && cookie.contains("Path=/"), cookie);
        assertFalse(cookie.contains("Secure"), "plain http in a test must not demand Secure");
        assertFalse(r.body().contains(cookie.substring(13, cookie.indexOf(';'))), "the token is never in the body");

        Browser b = signIn("admin@corp.com", PASSWORD);
        JsonNode me = json(call("GET", "/api/auth/me", null, b.reads()));
        assertEquals("session", me.get("via").asText());
        assertEquals("admin@corp.com", me.get("user").get("email").asText());
        assertEquals(b.csrf(), me.get("csrfToken").asText());

        assertEquals(403, call("POST", "/api/auth/logout", "", b.reads()).statusCode(), "a write needs the CSRF token");
        assertEquals(204, call("POST", "/api/auth/logout", "", b.writes()).statusCode());
        assertEquals(401, call("GET", "/api/auth/me", null, b.reads()).statusCode(), "the session must really end");
    }

    @Test
    void cookieFlagsFollowConfiguration() throws Exception {
        start(false, Map.of("AUTH_COOKIE_SAMESITE", "none"));
        String none = call("POST", "/api/auth/login", "{\"email\":\"admin@corp.com\",\"password\":\"" + PASSWORD + "\"}", Map.of())
                .headers().firstValue("Set-Cookie").orElseThrow();
        assertTrue(none.contains("SameSite=None") && none.contains("Secure"), "SameSite=None always needs Secure: " + none);
        stop();

        start(false, Map.of("AUTH_COOKIE_SAMESITE", "strict", "AUTH_COOKIE_SECURE", "true"));
        String strict = call("POST", "/api/auth/login", "{\"email\":\"admin@corp.com\",\"password\":\"" + PASSWORD + "\"}", Map.of())
                .headers().firstValue("Set-Cookie").orElseThrow();
        assertTrue(strict.contains("SameSite=Strict") && strict.contains("Secure"), strict);
        stop();

        start(false, Map.of());
        String proxied = call("POST", "/api/auth/login", "{\"email\":\"admin@corp.com\",\"password\":\"" + PASSWORD + "\"}",
                Map.of("X-Forwarded-Proto", "https")).headers().firstValue("Set-Cookie").orElseThrow();
        assertTrue(proxied.contains("Secure"), "behind an https proxy the cookie is Secure by default: " + proxied);
    }

    @Test
    void badLoginsAreGenericAndEventuallyLocked() throws Exception {
        start(false, Map.of());
        String wrong = "{\"email\":\"admin@corp.com\",\"password\":\"wrong password here\"}";
        HttpResponse<String> first = call("POST", "/api/auth/login", wrong, Map.of());
        HttpResponse<String> unknown = call("POST", "/api/auth/login", "{\"email\":\"nobody@corp.com\",\"password\":\"wrong password here\"}", Map.of());
        assertEquals(401, first.statusCode());
        assertEquals(first.body(), unknown.body().replace("nobody", "admin"));
        for (int i = 0; i < 6; i++) {
            call("POST", "/api/auth/login", wrong, Map.of());
        }
        HttpResponse<String> locked = call("POST", "/api/auth/login", "{\"email\":\"admin@corp.com\",\"password\":\"" + PASSWORD + "\"}", Map.of());
        assertEquals(429, locked.statusCode());
        assertNotNull(locked.headers().firstValue("Retry-After").orElse(null));
        assertEquals(405, call("GET", "/api/auth/login", null, Map.of()).statusCode());
    }

    @Test
    void membersCanReadButOnlyAdminsChangeRulesAndManageUsers() throws Exception {
        start(false, Map.of());
        addMember("member@corp.com");
        Browser member = signIn("member@corp.com", PASSWORD);
        assertEquals(200, call("GET", "/api/rules", null, member.reads()).statusCode());
        assertEquals(200, call("GET", "/events", null, member.reads()).statusCode());
        assertEquals(403, call("POST", "/api/rules", "{\"name\":\"x\",\"when\":{},\"then\":{\"attentionClass\":\"standard\"}}", member.writes()).statusCode());
        assertEquals(403, call("GET", "/api/users", null, member.reads()).statusCode());
        assertEquals(403, call("POST", "/api/deliveries/x/retry", "", member.writes()).statusCode());

        Browser admin = signIn("admin@corp.com", PASSWORD);
        assertEquals(200, call("GET", "/api/users", null, admin.reads()).statusCode());
        assertEquals(404, call("POST", "/api/deliveries/missing/retry", "", admin.writes()).statusCode(), "an admin gets past the gate");
    }

    @Test
    void aRejectedAccountsSessionStopsWorkingAtOnce() throws Exception {
        start(false, Map.of());
        addMember("member@corp.com");
        Browser member = signIn("member@corp.com", PASSWORD);
        assertEquals(200, call("GET", "/events", null, member.reads()).statusCode());
        assertEquals(200, call("PATCH", "/api/users/" + idOf("member@corp.com"), "{\"status\":\"rejected\"}", KEY).statusCode());
        assertEquals(401, call("GET", "/events", null, member.reads()).statusCode());
        assertEquals(400, call("PATCH", "/api/users/" + idOf("member@corp.com"), "{\"status\":\"owner\"}", KEY).statusCode());
    }

    @Test
    void userManagementEndpoints() throws Exception {
        start(false, Map.of());
        HttpResponse<String> created = call("POST", "/api/users", "{\"email\":\"new@corp.com\",\"name\":\"New\",\"password\":\"" + PASSWORD + "\"}", KEY);
        assertEquals(201, created.statusCode());
        assertEquals("member", json(created).get("role").asText());
        assertFalse(created.body().contains("argon2") || created.body().contains(PASSWORD));
        assertEquals(409, call("POST", "/api/users", "{\"email\":\"NEW@corp.com\"}", KEY).statusCode());
        assertEquals(400, call("POST", "/api/users", "{\"email\":\"nope\"}", KEY).statusCode());
        assertEquals(400, call("POST", "/api/users", "{\"email\":\"x@corp.com\",\"role\":\"owner\"}", KEY).statusCode());
        assertEquals(400, call("POST", "/api/users", "{\"email\":\"y@corp.com\",\"password\":\"short\"}", KEY).statusCode());
        String id = json(created).get("id").asText();
        assertEquals("admin", json(call("PATCH", "/api/users/" + id, "{\"role\":\"admin\",\"name\":\"Newer\"}", KEY)).get("role").asText());
        assertEquals(404, call("PATCH", "/api/users/missing", "{\"name\":\"x\"}", KEY).statusCode());
        assertEquals(405, call("DELETE", "/api/users/" + id, "", KEY).statusCode(), "accounts are disabled, never deleted");
        // Two admins now exist, so disabling one is allowed; the last one never is (covered in AccountsTest).
        assertEquals(200, call("PATCH", "/api/users/" + idOf("admin@corp.com"), "{\"disabled\":true}", KEY).statusCode());
        assertEquals(409, call("PATCH", "/api/users/" + id, "{\"disabled\":true}", KEY).statusCode(), "the last enabled admin must stay");
    }

    @Test
    void theApiKeyStillWorksAndAWrongOneIsRefusedEvenWithASession() throws Exception {
        start(false, Map.of());
        assertEquals(200, call("GET", "/events", null, KEY).statusCode());
        Browser admin = signIn("admin@corp.com", PASSWORD);
        Map<String, String> both = new HashMap<>(admin.reads());
        both.put("X-API-Key", "wrong");
        assertEquals(401, call("GET", "/events", null, both).statusCode(), "a wrong key is refused even with a valid cookie");
        assertEquals(401, call("GET", "/events", null, Map.of()).statusCode());
        JsonNode me = json(call("GET", "/api/auth/me", null, KEY));
        assertEquals("apikey", me.get("via").asText());
        assertNull(me.get("csrfToken"));
    }

    @Test
    void anUnknownCookieIsRefused() throws Exception {
        start(false, Map.of());
        assertEquals(401, call("GET", "/events", null, Map.of("Cookie", "attn_session=not-a-real-token")).statusCode());
        assertEquals(404, call("GET", "/api/auth/nothing", null, Map.of()).statusCode());
    }

    @Test
    void healthReportsTheRelease() throws Exception {
        start(false, Map.of("APP_VERSION", "1.2.3"));
        JsonNode health = json(call("GET", "/health", null, Map.of()));
        assertEquals("ok", health.get("status").asText());
        assertEquals("1.2.3", health.get("version").asText());
        stop();
        start(false, Map.of());
        assertEquals("dev", json(call("GET", "/health", null, Map.of())).get("version").asText(), "unset means dev");
    }
}
