package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	keyOnce sync.Once
	keyPool [2]*rsa.PrivateKey
)

// testKey returns one of two RSA keys shared by every test: generating 2048-bit keys is slow.
func testKey(t *testing.T, i int) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		for n := range keyPool {
			k, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				panic(err)
			}
			keyPool[n] = k
		}
	})
	return keyPool[i]
}

// fakeIdP is a minimal OpenID Connect provider that signs real RS256 tokens.
type fakeIdP struct {
	srv       *httptest.Server
	key       *rsa.PrivateKey
	kid       string
	claims    map[string]any // overrides for the next token
	tokenCode int
	gotForm   url.Values
	jwksHits  int
}

func newIdP(t *testing.T) (*fakeIdP, *OIDC) {
	t.Helper()
	key := testKey(t, 0)
	f := &fakeIdP{key: key, kid: "k1", tokenCode: http.StatusOK}
	mux := http.NewServeMux()
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		f.jwksHits++
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": f.kid,
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.gotForm = r.PostForm
		if f.tokenCode != http.StatusOK {
			w.WriteHeader(f.tokenCode)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": f.sign(f.claims)})
	})

	o, err := NewOIDC(OIDCConfig{Name: "Microsoft", Issuer: f.srv.URL, ClientID: "client-1", ClientSecret: "shh", RedirectURL: "https://svc/cb"})
	if err != nil {
		t.Fatal(err)
	}
	o.client = f.srv.Client()
	return f, o
}

func (f *fakeIdP) sign(overrides map[string]any) string {
	claims := map[string]any{
		"iss": f.srv.URL, "aud": "client-1", "sub": "sub-1", "oid": "oid-1",
		"exp": time.Now().Add(time.Hour).Unix(), "email": "ann@corp.com", "name": "Ann", "nonce": "",
	}
	for k, v := range overrides {
		claims[k] = v
	}
	return f.signRaw(map[string]any{"alg": "RS256", "kid": f.kid, "typ": "JWT"}, claims, f.key)
}

func (f *fakeIdP) signRaw(header, claims map[string]any, key *rsa.PrivateKey) string {
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	digest := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// signIn runs Begin and Complete the way a browser round trip would.
func signIn(t *testing.T, f *fakeIdP, o *OIDC, claims map[string]any) (Claims, error) {
	t.Helper()
	authURL, tx, err := o.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if claims == nil {
		claims = map[string]any{}
	}
	if _, set := claims["nonce"]; !set {
		claims["nonce"] = q.Get("nonce")
	}
	f.claims = claims
	return o.Complete(context.Background(), tx, q.Get("state"), "the-code")
}

func TestBeginBuildsAPKCEAuthorizationURL(t *testing.T) {
	_, o := newIdP(t)
	authURL, tx, err := o.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != "client-1" || q.Get("redirect_uri") != "https://svc/cb" ||
		q.Get("code_challenge_method") != "S256" || q.Get("state") != tx.State || q.Get("nonce") != tx.Nonce {
		t.Fatalf("query = %v", q)
	}
	sum := sha256.Sum256([]byte(tx.Verifier))
	if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("the challenge must be the SHA-256 of the verifier")
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("scope = %q", q.Get("scope"))
	}
	if strings.Contains(authURL, "shh") || strings.Contains(authURL, tx.Verifier) {
		t.Fatal("neither the client secret nor the verifier may be sent to the browser")
	}
}

func TestCompleteVerifiesAndReturnsClaims(t *testing.T) {
	f, o := newIdP(t)
	c, err := signIn(t, f, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "oid-1" || c.Email != "ann@corp.com" || c.Name != "Ann" || c.Issuer != f.srv.URL {
		t.Fatalf("claims = %+v", c)
	}
	if f.gotForm.Get("code") != "the-code" || f.gotForm.Get("client_secret") != "shh" || f.gotForm.Get("code_verifier") == "" {
		t.Fatalf("token request = %v", f.gotForm)
	}
}

func TestCompleteUsesPreferredUsernameWhenThereIsNoEmailClaim(t *testing.T) {
	f, o := newIdP(t)
	c, err := signIn(t, f, o, map[string]any{"email": "", "preferred_username": "bob@corp.com", "oid": ""})
	if err != nil || c.Email != "bob@corp.com" || c.Subject != "sub-1" {
		t.Fatalf("claims = %+v, %v", c, err)
	}
}

func TestIDTokenChecksRejectBadTokens(t *testing.T) {
	cases := map[string]map[string]any{
		"wrong issuer":   {"iss": "https://evil.example"},
		"wrong audience": {"aud": "someone-else"},
		"expired":        {"exp": time.Now().Add(-time.Hour).Unix()},
		"no expiry":      {"exp": 0},
		"not yet valid":  {"nbf": time.Now().Add(time.Hour).Unix()},
		"wrong nonce":    {"nonce": "replayed"},
		"no subject":     {"sub": "", "oid": ""},
	}
	for name, claims := range cases {
		f, o := newIdP(t)
		if _, err := signIn(t, f, o, claims); err == nil {
			t.Errorf("%s: the token must be rejected", name)
		}
	}
	f, o := newIdP(t)
	if c, err := signIn(t, f, o, map[string]any{"aud": []string{"x", "client-1"}}); err != nil || c.Subject == "" {
		t.Fatalf("an audience list containing the client must be accepted: %v", err)
	}
}

func TestSignatureAndAlgorithmAttacksAreRejected(t *testing.T) {
	f, o := newIdP(t)
	authURL, tx, _ := o.Begin(context.Background())
	nonce := mustQuery(authURL, "nonce")
	claims := map[string]any{"iss": f.srv.URL, "aud": "client-1", "sub": "s", "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce}
	other := testKey(t, 1)

	forged := f.signRaw(map[string]any{"alg": "RS256", "kid": f.kid}, claims, other)
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"k1"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"`+f.srv.URL+`","aud":"client-1","sub":"s","exp":9999999999}`)) + "."
	hs := f.signRaw(map[string]any{"alg": "HS256", "kid": f.kid}, claims, f.key)
	unknownKid := f.signRaw(map[string]any{"alg": "RS256", "kid": "nope"}, claims, f.key)

	for name, tok := range map[string]string{
		"signed by another key": forged, "alg none": none, "alg HS256": hs, "unknown key id": unknownKid, "garbage": "a.b", "empty": "",
	} {
		if _, err := o.verifyIDToken(context.Background(), tok, tx.Nonce); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
}

func mustQuery(raw, key string) string {
	u, _ := url.Parse(raw)
	return u.Query().Get(key)
}

func TestCompleteRejectsBadStateAndExpiredTransactions(t *testing.T) {
	f, o := newIdP(t)
	_, tx, _ := o.Begin(context.Background())
	f.claims = map[string]any{"nonce": tx.Nonce}
	ctx := context.Background()

	if _, err := o.Complete(ctx, tx, "other-state", "code"); !errors.Is(err, ErrOIDCState) {
		t.Fatalf("wrong state = %v", err)
	}
	if _, err := o.Complete(ctx, tx, tx.State, ""); !errors.Is(err, ErrOIDCState) {
		t.Fatalf("no code = %v", err)
	}
	if _, err := o.Complete(ctx, Transaction{}, "", "code"); !errors.Is(err, ErrOIDCState) {
		t.Fatalf("empty transaction = %v", err)
	}
	old := tx
	old.Expires = time.Now().Add(-time.Minute).Unix()
	if _, err := o.Complete(ctx, old, tx.State, "code"); !errors.Is(err, ErrOIDCState) {
		t.Fatalf("expired = %v", err)
	}
	f.tokenCode = http.StatusBadRequest
	if _, err := o.Complete(ctx, tx, tx.State, "code"); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("token endpoint failure = %v", err)
	}
}

func TestSigningKeysAreCachedAndRefetchIsThrottled(t *testing.T) {
	f, o := newIdP(t)
	signIn(t, f, o, nil)
	signIn(t, f, o, nil)
	if f.jwksHits != 1 {
		t.Fatalf("keys fetched %d times, want 1", f.jwksHits)
	}
	f.kid = "rotated" // the provider rotated its key; tokens now carry an unknown id
	f.jwksHits = 0
	o.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := signIn(t, f, o, nil); err != nil {
		t.Fatalf("after a key rotation the keys must be refetched: %v", err)
	}
	f.kid = "another"
	if _, err := signIn(t, f, o, nil); err == nil {
		t.Fatal("an unknown key id right after a refetch must not trigger another fetch")
	}
}

func TestNewOIDCValidatesConfiguration(t *testing.T) {
	for name, cfg := range map[string]OIDCConfig{
		"http issuer": {Issuer: "http://x", ClientID: "a", ClientSecret: "b", RedirectURL: "c"},
		"no issuer":   {ClientID: "a", ClientSecret: "b", RedirectURL: "c"},
		"no secret":   {Issuer: "https://x", ClientID: "a", RedirectURL: "c"},
		"no redirect": {Issuer: "https://x", ClientID: "a", ClientSecret: "b"},
	} {
		if _, err := NewOIDC(cfg); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	o, err := NewOIDC(OIDCConfig{Issuer: "https://x", ClientID: "a", ClientSecret: "b", RedirectURL: "c"})
	if err != nil || o.Name() != "Single sign-on" || o.Issuer() != "https://x" {
		t.Fatalf("defaults = %v %v", o, err)
	}
	if got := EntraIssuer("contoso.onmicrosoft.com"); got != "https://login.microsoftonline.com/contoso.onmicrosoft.com/v2.0" {
		t.Fatalf("entra issuer = %s", got)
	}
}

func TestDiscoveryMustMatchTheConfiguredIssuer(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": "https://someone-else", "authorization_endpoint": "a", "token_endpoint": "b", "jwks_uri": "c"})
	}))
	defer srv.Close()
	o, _ := NewOIDC(OIDCConfig{Issuer: srv.URL, ClientID: "a", ClientSecret: "b", RedirectURL: "c"})
	o.client = srv.Client()
	if _, _, err := o.Begin(context.Background()); err == nil {
		t.Fatal("a discovery document for another issuer must be refused")
	}
}

func TestTransactionsAreSignedAndTamperProof(t *testing.T) {
	secret := []byte("secret-1")
	tx := Transaction{State: "s", Nonce: "n", Verifier: "v", Expires: 123}
	sealed := SealTransaction(secret, tx)
	got, err := OpenTransaction(secret, sealed)
	if err != nil || got != tx {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if _, err := OpenTransaction([]byte("other"), sealed); err == nil {
		t.Fatal("a different secret must not open it")
	}
	payload, sig, _ := strings.Cut(sealed, ".")
	forgedBody, _ := json.Marshal(Transaction{State: "x", Nonce: "n", Verifier: "v", Expires: 999999999999})
	forged := base64.RawURLEncoding.EncodeToString(forgedBody) + "." + sig
	for name, s := range map[string]string{"tampered payload": forged, "no signature": payload, "empty": "", "bad base64": "!!." + sig} {
		if _, err := OpenTransaction(secret, s); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}
