package auth

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// EntraIssuer returns the issuer URL for a Microsoft Entra ID (Azure AD) tenant.
// Use the tenant's ID or domain, not "common": the multi-tenant endpoints sign
// tokens with a per-tenant issuer that cannot be checked against one address.
func EntraIssuer(tenant string) string {
	return "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/v2.0"
}

// OIDCConfig configures any OpenID Connect provider. Microsoft Entra ID is
// the first; Google, GitHub (via an OIDC bridge) and others differ only in these values.
type OIDCConfig struct {
	// Name is shown on the sign-in button, for example "Microsoft".
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is the service's own callback address registered with the provider.
	RedirectURL string
	Scopes      []string
}

// OIDC runs the authorization-code flow with PKCE and verifies the ID token.
type OIDC struct {
	cfg    OIDCConfig
	client *http.Client
	now    func() time.Time

	mu        sync.Mutex
	discovery *discovery
	keys      map[string]*rsa.PublicKey
	keysAt    time.Time
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// Claims are the verified identity claims the service uses.
type Claims struct {
	Issuer  string
	Subject string
	Email   string
	Name    string
}

// Errors from the sign-in flow. Messages are safe to log, not to show a person.
var (
	ErrOIDCState = errors.New("sign-in session expired or was tampered with")
	ErrOIDCToken = errors.New("identity token rejected")
)

// NewOIDC validates the configuration and returns a provider. Nothing is fetched until first use.
func NewOIDC(cfg OIDCConfig) (*OIDC, error) {
	u, err := url.Parse(cfg.Issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("oidc issuer must be an https address")
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc needs a client id, client secret and redirect url")
	}
	if cfg.Name == "" {
		cfg.Name = "Single sign-on"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "email", "profile"}
	}
	return &OIDC{
		cfg: cfg, now: time.Now, keys: map[string]*rsa.PublicKey{},
		client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

// WithHTTPClient replaces the client used to reach the provider, for a proxy or for tests.
func (o *OIDC) WithHTTPClient(c *http.Client) *OIDC {
	if c != nil {
		o.client = c
	}
	return o
}

// Name is the label for the sign-in button.
func (o *OIDC) Name() string { return o.cfg.Name }

// Issuer is the configured issuer.
func (o *OIDC) Issuer() string { return o.cfg.Issuer }

// Transaction is what must survive the round trip to the provider. The server
// keeps it in a signed cookie, so no server-side state is needed.
type Transaction struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Expires  int64  `json:"e"`
}

// Begin starts a sign-in: it returns the provider address to send the browser to
// and the transaction to remember.
func (o *OIDC) Begin(ctx context.Context) (string, Transaction, error) {
	d, err := o.discover(ctx)
	if err != nil {
		return "", Transaction{}, err
	}
	state, err1 := randomToken()
	nonce, err2 := randomToken()
	verifier, err3 := randomToken()
	if err := errors.Join(err1, err2, err3); err != nil {
		return "", Transaction{}, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {o.cfg.ClientID},
		"redirect_uri":          {o.cfg.RedirectURL},
		"scope":                 {strings.Join(o.cfg.Scopes, " ")},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	tx := Transaction{State: state, Nonce: nonce, Verifier: verifier, Expires: o.now().Add(10 * time.Minute).Unix()}
	return d.AuthorizationEndpoint + "?" + q.Encode(), tx, nil
}

// Complete finishes a sign-in: it checks the state, trades the code for tokens and verifies the ID token.
func (o *OIDC) Complete(ctx context.Context, tx Transaction, state, code string) (Claims, error) {
	if tx.State == "" || tx.Expires < o.now().Unix() || !hmac.Equal([]byte(tx.State), []byte(state)) || code == "" {
		return Claims{}, ErrOIDCState
	}
	d, err := o.discover(ctx)
	if err != nil {
		return Claims{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {o.cfg.RedirectURL},
		"client_id":     {o.cfg.ClientID},
		"client_secret": {o.cfg.ClientSecret},
		"code_verifier": {tx.Verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Claims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.client.Do(req)
	if err != nil {
		return Claims{}, errors.New("identity provider token request failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return Claims{}, fmt.Errorf("identity provider token request returned status %d", resp.StatusCode)
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.IDToken == "" {
		return Claims{}, ErrOIDCToken
	}
	return o.verifyIDToken(ctx, tok.IDToken, tx.Nonce)
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Issuer    string          `json:"iss"`
	Subject   string          `json:"sub"`
	Audience  json.RawMessage `json:"aud"`
	Expires   float64         `json:"exp"`
	NotBefore float64         `json:"nbf"`
	Nonce     string          `json:"nonce"`
	Email     string          `json:"email"`
	Username  string          `json:"preferred_username"`
	Name      string          `json:"name"`
	// Entra ID's stable per-tenant object id; the subject is only unique per application.
	OID string `json:"oid"`
}

const clockLeeway = time.Minute

func (o *OIDC) verifyIDToken(ctx context.Context, raw, nonce string) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, ErrOIDCToken
	}
	headBytes, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	claimBytes, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	sig, err3 := base64.RawURLEncoding.DecodeString(parts[2])
	if errors.Join(err1, err2, err3) != nil {
		return Claims{}, ErrOIDCToken
	}
	var head jwtHeader
	if json.Unmarshal(headBytes, &head) != nil || head.Alg != "RS256" { // never "none", never HMAC
		return Claims{}, ErrOIDCToken
	}
	key, err := o.key(ctx, head.Kid)
	if err != nil {
		return Claims{}, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return Claims{}, ErrOIDCToken
	}

	var c jwtClaims
	if json.Unmarshal(claimBytes, &c) != nil {
		return Claims{}, ErrOIDCToken
	}
	d, err := o.discover(ctx)
	if err != nil {
		return Claims{}, err
	}
	now := o.now()
	switch {
	case c.Issuer != d.Issuer || c.Issuer != o.cfg.Issuer:
		return Claims{}, fmt.Errorf("%w: wrong issuer", ErrOIDCToken)
	case !audienceHas(c.Audience, o.cfg.ClientID):
		return Claims{}, fmt.Errorf("%w: wrong audience", ErrOIDCToken)
	case c.Expires == 0 || now.After(time.Unix(int64(c.Expires), 0).Add(clockLeeway)):
		return Claims{}, fmt.Errorf("%w: expired", ErrOIDCToken)
	case c.NotBefore != 0 && now.Add(clockLeeway).Before(time.Unix(int64(c.NotBefore), 0)):
		return Claims{}, fmt.Errorf("%w: not yet valid", ErrOIDCToken)
	case nonce == "" || !hmac.Equal([]byte(c.Nonce), []byte(nonce)):
		return Claims{}, fmt.Errorf("%w: wrong nonce", ErrOIDCToken)
	}
	subject := c.Subject
	if c.OID != "" {
		subject = c.OID
	}
	email := c.Email
	if email == "" && strings.Contains(c.Username, "@") {
		email = c.Username
	}
	if subject == "" {
		return Claims{}, fmt.Errorf("%w: no subject", ErrOIDCToken)
	}
	return Claims{Issuer: c.Issuer, Subject: subject, Email: email, Name: c.Name}, nil
}

// audienceHas accepts "aud" as a string or a list.
func audienceHas(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == want {
				return true
			}
		}
	}
	return false
}

func (o *OIDC) discover(ctx context.Context) (*discovery, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.discovery != nil {
		return o.discovery, nil
	}
	var d discovery
	if err := o.getJSON(ctx, strings.TrimRight(o.cfg.Issuer, "/")+"/.well-known/openid-configuration", &d); err != nil {
		return nil, fmt.Errorf("identity provider discovery failed: %w", err)
	}
	if d.Issuer != o.cfg.Issuer || d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, errors.New("identity provider discovery document does not match the configured issuer")
	}
	o.discovery = &d
	return o.discovery, nil
}

// key returns the signing key for kid, refetching the key set at most once a minute when the id is unknown.
func (o *OIDC) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	d, err := o.discover(ctx)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if k, ok := o.keys[kid]; ok {
		return k, nil
	}
	if o.now().Sub(o.keysAt) < time.Minute && len(o.keys) > 0 {
		return nil, ErrOIDCToken
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := o.getJSON(ctx, d.JWKSURI, &set); err != nil {
		return nil, fmt.Errorf("identity provider key fetch failed: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		nb, err1 := base64.RawURLEncoding.DecodeString(k.N)
		eb, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if k.Kty != "RSA" || err1 != nil || err2 != nil || len(eb) == 0 || len(eb) > 4 {
			continue
		}
		e := 0
		for _, b := range eb {
			e = e<<8 | int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}
	}
	o.keys, o.keysAt = keys, o.now()
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, ErrOIDCToken
}

func (o *OIDC) getJSON(ctx context.Context, address string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return errors.New("request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into)
}

// SealTransaction signs a transaction for storage in a cookie.
func SealTransaction(secret []byte, tx Transaction) string {
	body, _ := json.Marshal(tx)
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + sign(secret, payload)
}

// OpenTransaction verifies and decodes a sealed transaction.
func OpenTransaction(secret []byte, sealed string) (Transaction, error) {
	payload, sig, ok := strings.Cut(sealed, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(sign(secret, payload))) {
		return Transaction{}, ErrOIDCState
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	var tx Transaction
	if err != nil || json.Unmarshal(body, &tx) != nil {
		return Transaction{}, ErrOIDCState
	}
	return tx, nil
}

func sign(secret []byte, payload string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
