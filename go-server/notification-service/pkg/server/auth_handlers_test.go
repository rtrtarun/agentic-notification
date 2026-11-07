package server

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/auth"
	"github.com/agentpulse/notification-service/pkg/config"
)

const (
	adminEmail = "admin@corp.com"
	adminPass  = "correct horse battery"
	memberPass = "another long password"
)

func newAuthServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.APIKey, cfg.AllowUnsignedWebhooks = testAPIKey, true
	cfg.AuthBootstrapEmail, cfg.AuthBootstrapPassword = adminEmail, adminPass
	cfg.CORSAllowedOrigins = []string{"http://console.test"}
	if mutate != nil {
		mutate(cfg)
	}
	s, err := NewServer(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = s.closeAll() })
	return s
}

// call runs a request through the full handler stack.
func call(s *Server, method, path, body string, edit func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if edit != nil {
		edit(req)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func cookieNamed(rr *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

type session struct {
	cookie *http.Cookie
	csrf   string
}

func (se session) apply(withCSRF bool) func(*http.Request) {
	return func(r *http.Request) {
		r.AddCookie(se.cookie)
		if withCSRF {
			r.Header.Set(csrfHeader, se.csrf)
		}
	}
}

func signIn(t *testing.T, s *Server, email, password string) session {
	t.Helper()
	rr := call(s, http.MethodPost, "/api/auth/login", `{"email":"`+email+`","password":"`+password+`"}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("login %s = %d %s", email, rr.Code, rr.Body.String())
	}
	var body struct {
		CSRF string `json:"csrfToken"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return session{cookie: cookieNamed(rr, sessionCookie), csrf: body.CSRF}
}

func withKey(r *http.Request) { r.Header.Set("X-API-Key", testAPIKey) }

func addMember(t *testing.T, s *Server, email string) session {
	t.Helper()
	rr := call(s, http.MethodPost, "/api/users", `{"email":"`+email+`","role":"member","password":"`+memberPass+`"}`, withKey)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create member = %d %s", rr.Code, rr.Body.String())
	}
	return signIn(t, s, email, memberPass)
}

func TestStatusIsPublicAndReportsSetup(t *testing.T) {
	s := newAuthServer(t, nil)
	rr := call(s, http.MethodGet, "/api/auth/status", "", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"needsSetup":false`) || !strings.Contains(rr.Body.String(), `"enabled":false`) {
		t.Fatalf("status = %d %s", rr.Code, rr.Body.String())
	}
	fresh := newAuthServer(t, func(c *config.Config) { c.AuthBootstrapEmail, c.AuthBootstrapPassword = "", "" })
	if body := call(fresh, http.MethodGet, "/api/auth/status", "", nil).Body.String(); !strings.Contains(body, `"needsSetup":true`) {
		t.Fatalf("a service with no accounts must say so: %s", body)
	}
	if rr := call(s, http.MethodPost, "/api/auth/status", "", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", rr.Code)
	}
}

func TestLoginSetsAHardenedCookieAndNeverReturnsTheToken(t *testing.T) {
	s := newAuthServer(t, nil)
	rr := call(s, http.MethodPost, "/api/auth/login", `{"email":"ADMIN@corp.com","password":"`+adminPass+`"}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("login = %d %s", rr.Code, rr.Body.String())
	}
	c := cookieNamed(rr, sessionCookie)
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure {
		t.Fatalf("cookie = %+v", c)
	}
	if strings.Contains(rr.Body.String(), c.Value) || strings.Contains(rr.Body.String(), "argon2") || strings.Contains(rr.Body.String(), "passwordHash") {
		t.Fatalf("response leaks secrets: %s", rr.Body.String())
	}
	secure := call(s, http.MethodPost, "/api/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`,
		func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") })
	if c := cookieNamed(secure, sessionCookie); c == nil || !c.Secure {
		t.Fatalf("behind HTTPS the cookie must be Secure: %+v", c)
	}
}

func TestCookieSettingsFollowConfiguration(t *testing.T) {
	s := newAuthServer(t, func(c *config.Config) { c.AuthCookieSameSite = "none" })
	c := cookieNamed(call(s, http.MethodPost, "/api/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`, nil), sessionCookie)
	if c == nil || c.SameSite != http.SameSiteNoneMode || !c.Secure {
		t.Fatalf("SameSite=None needs Secure: %+v", c)
	}
	s = newAuthServer(t, func(c *config.Config) { c.AuthCookieSameSite, c.AuthCookieSecure = "strict", "true" })
	c = cookieNamed(call(s, http.MethodPost, "/api/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`, nil), sessionCookie)
	if c == nil || c.SameSite != http.SameSiteStrictMode || !c.Secure {
		t.Fatalf("cookie = %+v", c)
	}
}

func TestBadLoginsAreGenericAndEventuallyLocked(t *testing.T) {
	s := newAuthServer(t, nil)
	for _, body := range []string{
		`{"email":"admin@corp.com","password":"wrong password here"}`, `{"email":"nobody@corp.com","password":"` + adminPass + `"}`,
	} {
		rr := call(s, http.MethodPost, "/api/auth/login", body, nil)
		if rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), "incorrect email or password") {
			t.Fatalf("%s = %d %s", body, rr.Code, rr.Body.String())
		}
	}
	for i := 0; i < auth.DefaultMaxFailures; i++ {
		call(s, http.MethodPost, "/api/auth/login", `{"email":"admin@corp.com","password":"wrong password here"}`, nil)
	}
	rr := call(s, http.MethodPost, "/api/auth/login", `{"email":"admin@corp.com","password":"`+adminPass+`"}`, nil)
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("locked login = %d", rr.Code)
	}
	if rr := call(s, http.MethodPost, "/api/auth/login", `not json`, nil); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad body = %d", rr.Code)
	}
	if rr := call(s, http.MethodGet, "/api/auth/login", "", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET login = %d", rr.Code)
	}
}

func TestMeSessionAndAPIKey(t *testing.T) {
	s := newAuthServer(t, nil)
	se := signIn(t, s, adminEmail, adminPass)

	rr := call(s, http.MethodGet, "/api/auth/me", "", se.apply(false))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"via":"session"`) || !strings.Contains(rr.Body.String(), se.csrf) {
		t.Fatalf("me = %d %s", rr.Code, rr.Body.String())
	}
	rr = call(s, http.MethodGet, "/api/auth/me", "", withKey)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"via":"apikey"`) || strings.Contains(rr.Body.String(), "csrfToken") {
		t.Fatalf("me via key = %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(s, http.MethodGet, "/api/auth/me", "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous me = %d", rr.Code)
	}
}

func TestCSRFIsRequiredForCookieWritesOnly(t *testing.T) {
	s := newAuthServer(t, nil)
	se := signIn(t, s, adminEmail, adminPass)
	body := `{"quietHours":{"enabled":false,"start":"22:00","end":"07:00","timezone":"UTC"}}`

	if rr := call(s, http.MethodPut, "/api/preferences", body, se.apply(false)); rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "csrf") {
		t.Fatalf("a write without the token = %d %s", rr.Code, rr.Body.String())
	}
	wrong := session{cookie: se.cookie, csrf: "not-the-token"}
	if rr := call(s, http.MethodPut, "/api/preferences", body, wrong.apply(true)); rr.Code != http.StatusForbidden {
		t.Fatalf("a write with a wrong token = %d", rr.Code)
	}
	if rr := call(s, http.MethodPut, "/api/preferences", body, se.apply(true)); rr.Code != http.StatusOK {
		t.Fatalf("a write with the token = %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(s, http.MethodGet, "/api/preferences", "", se.apply(false)); rr.Code != http.StatusOK {
		t.Fatalf("reads need no token = %d", rr.Code)
	}
	if rr := call(s, http.MethodPut, "/api/preferences", body, withKey); rr.Code != http.StatusOK {
		t.Fatalf("the API key needs no CSRF token = %d", rr.Code)
	}
}

func TestWrongAPIKeyIsRefusedEvenWithAValidSession(t *testing.T) {
	s := newAuthServer(t, nil)
	se := signIn(t, s, adminEmail, adminPass)
	rr := call(s, http.MethodGet, "/api/preferences", "", func(r *http.Request) {
		se.apply(false)(r)
		r.Header.Set("X-API-Key", "wrong")
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestSessionsWorkWithoutAnAPIKeyConfigured(t *testing.T) {
	s := newAuthServer(t, func(c *config.Config) { c.APIKey = "" })
	if rr := call(s, http.MethodGet, "/api/preferences", "", nil); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("anonymous with no key configured must still fail closed, got %d", rr.Code)
	}
	se := signIn(t, s, adminEmail, adminPass)
	if rr := call(s, http.MethodGet, "/api/preferences", "", se.apply(false)); rr.Code != http.StatusOK {
		t.Fatalf("a signed-in admin = %d", rr.Code)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	s := newAuthServer(t, nil)
	se := signIn(t, s, adminEmail, adminPass)
	if rr := call(s, http.MethodPost, "/api/auth/logout", "", se.apply(false)); rr.Code != http.StatusForbidden {
		t.Fatalf("logout without the token = %d (a forged page must not sign you out)", rr.Code)
	}
	rr := call(s, http.MethodPost, "/api/auth/logout", "", se.apply(true))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rr.Code)
	}
	if c := cookieNamed(rr, sessionCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("the cookie must be cleared: %+v", c)
	}
	if rr := call(s, http.MethodGet, "/api/auth/me", "", se.apply(false)); rr.Code != http.StatusUnauthorized {
		t.Fatalf("a logged-out session = %d", rr.Code)
	}
}

func TestMembersCannotChangeConfigurationButCanReadAndWorkTheInbox(t *testing.T) {
	s := newAuthServer(t, nil)
	m := addMember(t, s, "member@corp.com")
	admin := signIn(t, s, adminEmail, adminPass)

	forbidden := []struct{ method, path, body string }{
		{http.MethodPut, "/api/preferences", `{}`},
		{http.MethodPost, "/api/rules", `{"name":"x","then":{"attentionClass":"background"}}`},
		{http.MethodDelete, "/api/rules/anything", ""},
		{http.MethodPost, "/api/rules/reorder", `{}`},
		{http.MethodPost, "/api/channels/slack/test", ""},
		{http.MethodPost, "/api/deliveries/x/retry", ""},
		{http.MethodGet, "/api/users", ""},
		{http.MethodPost, "/api/users", `{"email":"x@corp.com"}`},
		{http.MethodPatch, "/api/users/x", `{}`},
		{http.MethodGet, "/api/audit", ""},
	}
	for _, c := range forbidden {
		if rr := call(s, c.method, c.path, c.body, m.apply(true)); rr.Code != http.StatusForbidden {
			t.Errorf("member %s %s = %d, want 403", c.method, c.path, rr.Code)
		}
	}
	for _, path := range []string{"/api/rules", "/api/preferences", "/api/channels", "/api/deliveries", "/api/notifications", "/api/analytics"} {
		if rr := call(s, http.MethodGet, path, "", m.apply(false)); rr.Code != http.StatusOK {
			t.Errorf("member GET %s = %d, want 200", path, rr.Code)
		}
	}
	if rr := call(s, http.MethodPost, "/api/rules/test", `{"source":"github","eventType":"push"}`, m.apply(true)); rr.Code == http.StatusForbidden {
		t.Error("the rule dry run changes nothing and must stay open to members")
	}
	if rr := call(s, http.MethodGet, "/api/users", "", admin.apply(false)); rr.Code != http.StatusOK {
		t.Errorf("admin GET /api/users = %d", rr.Code)
	}
}

func TestUserManagementEndpoints(t *testing.T) {
	s := newAuthServer(t, nil)
	rr := call(s, http.MethodPost, "/api/users", `{"email":"new@corp.com","name":"New","password":"`+memberPass+`"}`, withKey)
	if rr.Code != http.StatusCreated || !strings.Contains(rr.Body.String(), `"role":"member"`) {
		t.Fatalf("create = %d %s", rr.Code, rr.Body.String())
	}
	var created struct{ ID string }
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if strings.Contains(rr.Body.String(), "argon2") || strings.Contains(rr.Body.String(), memberPass) {
		t.Fatal("a created user must never echo a password or hash")
	}
	for name, c := range map[string]struct {
		body string
		want int
	}{
		"duplicate email": {`{"email":"NEW@corp.com"}`, http.StatusConflict},
		"bad email":       {`{"email":"nope"}`, http.StatusBadRequest},
		"bad role":        {`{"email":"x@corp.com","role":"owner"}`, http.StatusBadRequest},
		"weak password":   {`{"email":"y@corp.com","password":"short"}`, http.StatusBadRequest},
	} {
		if rr := call(s, http.MethodPost, "/api/users", c.body, withKey); rr.Code != c.want {
			t.Errorf("%s = %d, want %d: %s", name, rr.Code, c.want, rr.Body.String())
		}
	}

	list := call(s, http.MethodGet, "/api/users", "", withKey).Body.String()
	if !strings.Contains(list, "new@corp.com") || strings.Contains(list, "argon2") {
		t.Fatalf("list = %s", list)
	}
	if rr := call(s, http.MethodPatch, "/api/users/"+created.ID, `{"role":"admin","name":"Newer"}`, withKey); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"role":"admin"`) {
		t.Fatalf("patch = %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(s, http.MethodPatch, "/api/users/missing", `{"name":"x"}`, withKey); rr.Code != http.StatusNotFound {
		t.Fatalf("patch missing = %d", rr.Code)
	}
	if rr := call(s, http.MethodDelete, "/api/users/"+created.ID, "", withKey); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete = %d (accounts are disabled, never deleted)", rr.Code)
	}
}

func TestTheLastAdminCannotBeRemoved(t *testing.T) {
	s := newAuthServer(t, nil)
	users, _ := s.auth.Users(context.Background())
	id := users[0].ID
	if rr := call(s, http.MethodPatch, "/api/users/"+id, `{"role":"member"}`, withKey); rr.Code != http.StatusConflict {
		t.Fatalf("demote = %d", rr.Code)
	}
	if rr := call(s, http.MethodPatch, "/api/users/"+id, `{"disabled":true}`, withKey); rr.Code != http.StatusConflict {
		t.Fatalf("disable = %d", rr.Code)
	}
}

func TestDisablingAnAccountEndsItsSession(t *testing.T) {
	s := newAuthServer(t, nil)
	m := addMember(t, s, "member@corp.com")
	users, _ := s.auth.Users(context.Background())
	var id string
	for _, u := range users {
		if u.Email == "member@corp.com" {
			id = u.ID
		}
	}
	call(s, http.MethodPatch, "/api/users/"+id, `{"disabled":true}`, withKey)
	if rr := call(s, http.MethodGet, "/api/auth/me", "", m.apply(false)); rr.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled account's session = %d", rr.Code)
	}
}

func TestChangingYourPassword(t *testing.T) {
	s := newAuthServer(t, nil)
	m := addMember(t, s, "member@corp.com")
	next := "a brand new password"

	if rr := call(s, http.MethodPost, "/api/auth/password", `{"current":"wrong password here","next":"`+next+`"}`, m.apply(true)); rr.Code != http.StatusBadRequest {
		t.Fatalf("wrong current = %d", rr.Code)
	}
	if rr := call(s, http.MethodPost, "/api/auth/password", `{"current":"`+memberPass+`","next":"short"}`, m.apply(true)); rr.Code != http.StatusBadRequest {
		t.Fatalf("weak new = %d", rr.Code)
	}
	if rr := call(s, http.MethodPost, "/api/auth/password", `{"current":"`+memberPass+`","next":"`+next+`"}`, m.apply(true)); rr.Code != http.StatusNoContent {
		t.Fatalf("change = %d", rr.Code)
	}
	if rr := call(s, http.MethodGet, "/api/auth/me", "", m.apply(false)); rr.Code != http.StatusUnauthorized {
		t.Fatalf("the old session must have ended: %d", rr.Code)
	}
	signIn(t, s, "member@corp.com", next)
	if rr := call(s, http.MethodPost, "/api/auth/password", `{"current":"x","next":"y"}`, withKey); rr.Code != http.StatusBadRequest {
		t.Fatalf("the API key has no password = %d", rr.Code)
	}
}

func TestAuditTrailRecordsSignInsWithoutSecrets(t *testing.T) {
	s := newAuthServer(t, nil)
	call(s, http.MethodPost, "/api/auth/login", `{"email":"admin@corp.com","password":"wrong password here"}`, nil)
	signIn(t, s, adminEmail, adminPass)
	rr := call(s, http.MethodGet, "/api/audit?limit=50", "", withKey)
	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, `"login.failed"`) || !strings.Contains(body, `"login"`) {
		t.Fatalf("audit = %d %s", rr.Code, body)
	}
	for _, secret := range []string{adminPass, "wrong password here", "argon2"} {
		if strings.Contains(body, secret) {
			t.Fatalf("audit leaks %q", secret)
		}
	}
	if rr := call(s, http.MethodGet, "/api/audit?limit=0", "", withKey); rr.Code != http.StatusBadRequest {
		t.Fatalf("limit 0 = %d", rr.Code)
	}
	if rr := call(s, http.MethodPost, "/api/audit", "", withKey); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST audit = %d", rr.Code)
	}
}

func TestCORSAllowsCredentialsOnlyForExactOrigins(t *testing.T) {
	s := newAuthServer(t, nil)
	rr := call(s, http.MethodOptions, "/api/auth/me", "", func(r *http.Request) { r.Header.Set("Origin", "http://console.test") })
	if rr.Header().Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(rr.Header().Get("Access-Control-Allow-Headers"), "X-CSRF-Token") {
		t.Fatalf("headers = %v", rr.Header())
	}
	wild := newAuthServer(t, func(c *config.Config) { c.CORSAllowedOrigins = []string{"*"} })
	rr = call(wild, http.MethodOptions, "/api/auth/me", "", func(r *http.Request) { r.Header.Set("Origin", "http://anywhere.test") })
	if rr.Header().Get("Access-Control-Allow-Origin") == "" || rr.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("a wildcard origin must never be allowed to send cookies: %v", rr.Header())
	}
}

func TestStartupRejectsABadBootstrapPassword(t *testing.T) {
	cfg, _ := config.LoadConfig()
	cfg.AuthBootstrapEmail, cfg.AuthBootstrapPassword = adminEmail, "short"
	if _, err := NewServer(context.Background(), cfg, quietLogger()); err == nil {
		t.Fatal("a weak bootstrap password must stop start-up")
	}
}

// ---- single sign-on through a fake provider ----

type idp struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	nonce string
	email string
}

func newIdP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{key: key, email: "ann@corp.com"}
	mux := http.NewServeMux()
	p.srv = httptest.NewTLSServer(mux)
	t.Cleanup(p.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize",
			"token_endpoint": p.srv.URL + "/token", "jwks_uri": p.srv.URL + "/keys"})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		claims := map[string]any{"iss": p.srv.URL, "aud": "client-1", "oid": "oid-ann", "name": "Ann", "email": p.email,
			"exp": time.Now().Add(time.Hour).Unix(), "nonce": p.nonce}
		hb, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1"})
		cb, _ := json.Marshal(claims)
		signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
		sum := sha256.Sum256([]byte(signing))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": signing + "." + base64.RawURLEncoding.EncodeToString(sig)})
	})
	return p
}

func newSSOServer(t *testing.T, p *idp, domains []string) *Server {
	s := newAuthServer(t, func(c *config.Config) {
		c.OIDCIssuer, c.OIDCName, c.OIDCClientID, c.OIDCClientSecret = p.srv.URL, "Microsoft", "client-1", "shh"
		c.PublicURL, c.ConsoleURL, c.OIDCAllowedDomains = "https://svc.test", "http://console.test/", domains
		c.AuthSecret = "test-secret"
	})
	s.oidc.WithHTTPClient(p.srv.Client())
	return s
}

func startSSO(t *testing.T, s *Server, p *idp) (state string, txCookie *http.Cookie) {
	t.Helper()
	rr := call(s, http.MethodGet, "/api/auth/oidc/start", "", nil)
	if rr.Code != http.StatusFound {
		t.Fatalf("start = %d %s", rr.Code, rr.Body.String())
	}
	loc, _ := url.Parse(rr.Header().Get("Location"))
	if !strings.HasPrefix(loc.String(), p.srv.URL+"/authorize") || loc.Query().Get("redirect_uri") != "https://svc.test/api/auth/oidc/callback" {
		t.Fatalf("location = %s", loc)
	}
	p.nonce = loc.Query().Get("nonce")
	tx := cookieNamed(rr, oidcCookie)
	if tx == nil || !tx.HttpOnly || tx.Path != "/api/auth/oidc" {
		t.Fatalf("transaction cookie = %+v", tx)
	}
	return loc.Query().Get("state"), tx
}

func TestSingleSignOnProvisionsAMemberAndStartsASession(t *testing.T) {
	p := newIdP(t)
	s := newSSOServer(t, p, []string{"corp.com"})

	status := call(s, http.MethodGet, "/api/auth/status", "", nil).Body.String()
	if !strings.Contains(status, `"name":"Microsoft"`) || !strings.Contains(status, "/api/auth/oidc/start") {
		t.Fatalf("status = %s", status)
	}
	state, tx := startSSO(t, s, p)
	rr := call(s, http.MethodGet, "/api/auth/oidc/callback?code=abc&state="+state, "", func(r *http.Request) { r.AddCookie(tx) })
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "http://console.test/" {
		t.Fatalf("callback = %d -> %s", rr.Code, rr.Header().Get("Location"))
	}
	sc := cookieNamed(rr, sessionCookie)
	if sc == nil || !sc.HttpOnly {
		t.Fatalf("session cookie = %+v", sc)
	}
	me := call(s, http.MethodGet, "/api/auth/me", "", func(r *http.Request) { r.AddCookie(sc) })
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), "ann@corp.com") || !strings.Contains(me.Body.String(), `"role":"member"`) {
		t.Fatalf("me = %d %s", me.Code, me.Body.String())
	}
	if c := cookieNamed(rr, oidcCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("the transaction cookie must be cleared: %+v", c)
	}
}

func TestSingleSignOutRefusalsRedirectWithoutDetail(t *testing.T) {
	p := newIdP(t)
	s := newSSOServer(t, p, []string{"corp.com"})

	// A domain outside the allow-list is refused.
	p.email = "mallory@evil.com"
	state, tx := startSSO(t, s, p)
	rr := call(s, http.MethodGet, "/api/auth/oidc/callback?code=abc&state="+state, "", func(r *http.Request) { r.AddCookie(tx) })
	if loc := rr.Header().Get("Location"); rr.Code != http.StatusFound || !strings.Contains(loc, "signin=denied") || cookieNamed(rr, sessionCookie) != nil {
		t.Fatalf("outside domain = %d -> %s", rr.Code, loc)
	}

	cases := map[string]func() *httptest.ResponseRecorder{
		"wrong state": func() *httptest.ResponseRecorder {
			_, tx := startSSO(t, s, p)
			return call(s, http.MethodGet, "/api/auth/oidc/callback?code=abc&state=forged", "", func(r *http.Request) { r.AddCookie(tx) })
		},
		"no transaction cookie": func() *httptest.ResponseRecorder {
			state, _ := startSSO(t, s, p)
			return call(s, http.MethodGet, "/api/auth/oidc/callback?code=abc&state="+state, "", nil)
		},
		"tampered cookie": func() *httptest.ResponseRecorder {
			state, tx := startSSO(t, s, p)
			bad := *tx
			bad.Value = "x" + tx.Value
			return call(s, http.MethodGet, "/api/auth/oidc/callback?code=abc&state="+state, "", func(r *http.Request) { r.AddCookie(&bad) })
		},
		"provider error": func() *httptest.ResponseRecorder {
			return call(s, http.MethodGet, "/api/auth/oidc/callback?error=access_denied", "", nil)
		},
	}
	for name, run := range cases {
		rr := run()
		if rr.Code != http.StatusFound || !strings.Contains(rr.Header().Get("Location"), "signin=") || cookieNamed(rr, sessionCookie) != nil {
			t.Errorf("%s = %d -> %s", name, rr.Code, rr.Header().Get("Location"))
		}
	}
}

func TestSingleSignOnIsOffUnlessConfigured(t *testing.T) {
	s := newAuthServer(t, nil)
	for _, path := range []string{"/api/auth/oidc/start", "/api/auth/oidc/callback"} {
		if rr := call(s, http.MethodGet, path, "", nil); rr.Code != http.StatusNotFound {
			t.Errorf("%s = %d", path, rr.Code)
		}
	}
}

func TestEntraShortcutBuildsTheTenantIssuer(t *testing.T) {
	cfg, _ := config.LoadConfig()
	cfg.EntraTenantID, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.PublicURL = "contoso.onmicrosoft.com", "c", "s", "https://svc.test"
	s, err := NewServer(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer s.closeAll() //nolint:errcheck
	if s.oidc == nil || s.oidc.Issuer() != "https://login.microsoftonline.com/contoso.onmicrosoft.com/v2.0" || s.oidc.Name() != "Microsoft" {
		t.Fatalf("oidc = %+v", s.oidc)
	}
}

func TestStartupRejectsIncompleteSingleSignOn(t *testing.T) {
	for name, mutate := range map[string]func(*config.Config){
		"no public url":     func(c *config.Config) { c.EntraTenantID, c.OIDCClientID, c.OIDCClientSecret = "t", "c", "s" },
		"no client secret":  func(c *config.Config) { c.EntraTenantID, c.OIDCClientID, c.PublicURL = "t", "c", "https://svc.test" },
		"client without id": func(c *config.Config) { c.OIDCClientID, c.PublicURL = "c", "https://svc.test" },
	} {
		cfg, _ := config.LoadConfig()
		mutate(cfg)
		if _, err := NewServer(context.Background(), cfg, quietLogger()); err == nil {
			t.Errorf("%s: expected a start-up error", name)
		}
	}
}
