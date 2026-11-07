// Package config loads service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Defaults for settings that have one. Exported so docs and tests can refer to them.
const (
	DefaultPort            = 8080
	DefaultSMTPPort        = 587
	DefaultDedupWindow     = 10 * time.Minute
	DefaultMaxBodyBytes    = int64(1 << 20)
	DefaultRequestTimeout  = 25 * time.Second
	DefaultEventRetention  = 90 * 24 * time.Hour
	DefaultDBMaxOpenConns  = 25
	DefaultDBMaxIdleConns  = 5
	DefaultDBConnLifetime  = 30 * time.Minute
	defaultWebhookHostList = "hooks.slack.com,outlook.office.com,outlook.office365.com,*.webhook.office.com"
)

// Config holds every runtime setting. Zero values are safe: they fail closed.
type Config struct {
	Address              string
	APIKey               string
	AllowUnauthenticated bool
	DatabaseURL          string
	RedisURL             string
	RulesPath            string
	DedupWindow          time.Duration
	DefaultNotifyEmail   string
	SMTPHost             string
	SMTPPort             int
	SMTPUsername         string
	SMTPPassword         string
	SMTPSender           string
	SlackWebhookURL      string
	TeamsWebhookURL      string
	// SMSGateway selects how texts leave the service: "" (off), "mock", "http" or "sns".
	SMSGateway     string
	SMSRecipients  []string
	SMSHTTPURL     string
	SMSHTTPHeaders map[string]string
	SMSHTTPBody    string
	// SMSDailyLimit caps queued SMS per UTC day. 0 means no cap. Critical events are never capped.
	SMSDailyLimit      int
	AWSRegion          string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AWSSessionToken    string
	// StandardChannels and BackgroundChannels list the channels that receive
	// those attention classes. Empty (the default) means the class is only recorded.
	StandardChannels   []string
	BackgroundChannels []string
	// Accounts and sign-in (all optional). The API key keeps working for services.
	AuthBootstrapEmail    string
	AuthBootstrapPassword string
	AuthSessionTTL        time.Duration
	// AuthCookieSecure is "auto" (secure when the request arrived over HTTPS), "true" or "false".
	AuthCookieSecure string
	// AuthCookieSameSite is "lax" (default), "strict" or "none" (needed when the console is on another site; forces Secure).
	AuthCookieSameSite string
	// AuthSecret signs the short-lived sign-in state. Set it when running several replicas.
	AuthSecret string
	// PublicURL is this service's own public address, used to build the identity provider callback.
	PublicURL string
	// ConsoleURL is where the browser is sent after single sign-on.
	ConsoleURL string
	// Single sign-on through any OpenID Connect provider. EntraTenantID is a shortcut for Microsoft Entra ID.
	EntraTenantID      string
	OIDCIssuer         string
	OIDCName           string
	OIDCClientID       string
	OIDCClientSecret   string
	OIDCAllowedDomains []string
	// OverridesAdminOnly stops members from creating always/never notify overrides (their feedback is then only a signal).
	OverridesAdminOnly bool
	// AttentionClassify lets Attention Intelligence refine events whose rule allows it (default on).
	AttentionClassify bool
	// AttentionDigestEvery is how often a digest is generated; zero means only on request.
	AttentionDigestEvery time.Duration
	// AttentionProvider chooses what powers Attention Intelligence: "heuristic" (default, no model),
	// "ollama" or "claude". A model that is down or invalid falls back to the heuristic.
	AttentionProvider string
	// OllamaURL and OllamaModel locate a local Ollama server (provider "ollama").
	OllamaURL   string
	OllamaModel string
	// AnthropicAPIKey is required for provider "claude" and is never logged or returned by the API.
	AnthropicAPIKey string
	// AttentionModel overrides the Claude model name (provider "claude").
	AttentionModel string
	// AttentionTimeout bounds one model call.
	AttentionTimeout time.Duration
	// AttentionMaxCallsPerMin caps model calls per minute.
	AttentionMaxCallsPerMin int
	// DigestChannels lists the channels a digest is delivered to; empty means digests are only stored.
	DigestChannels        []string
	GitHubWebhookSecret   string
	AllowUnsignedWebhooks bool
	MaxBodyBytes          int64
	RequestTimeout        time.Duration
	CORSAllowedOrigins    []string
	AllowedWebhookHosts   []string
	AllowedEmailDomains   []string
	EventRetention        time.Duration
	DBMaxOpenConns        int
	DBMaxIdleConns        int
	DBConnMaxLifetime     time.Duration
	LogLevel              slog.Level
}

// LoadConfig reads configuration from the environment and validates it.
func LoadConfig() (*Config, error) {
	var (
		cfg Config
		err error
	)

	port, err := envInt("PORT", DefaultPort, 1, 65535)
	if err != nil {
		return nil, err
	}
	cfg.Address = ":" + strconv.Itoa(port)

	if cfg.SMTPPort, err = envInt("SMTP_PORT", DefaultSMTPPort, 1, 65535); err != nil {
		return nil, err
	}
	if cfg.DedupWindow, err = envDuration("DEDUP_WINDOW", DefaultDedupWindow, false); err != nil {
		return nil, err
	}
	if cfg.RequestTimeout, err = envDuration("REQUEST_TIMEOUT", DefaultRequestTimeout, false); err != nil {
		return nil, err
	}
	// Zero disables retention, so zero is a legal value here.
	if cfg.EventRetention, err = envDuration("EVENT_RETENTION", DefaultEventRetention, true); err != nil {
		return nil, err
	}
	if cfg.DBConnMaxLifetime, err = envDuration("DB_CONN_MAX_LIFETIME", DefaultDBConnLifetime, false); err != nil {
		return nil, err
	}
	if cfg.DBMaxOpenConns, err = envInt("DB_MAX_OPEN_CONNS", DefaultDBMaxOpenConns, 1, 1000); err != nil {
		return nil, err
	}
	if cfg.DBMaxIdleConns, err = envInt("DB_MAX_IDLE_CONNS", DefaultDBMaxIdleConns, 0, 1000); err != nil {
		return nil, err
	}
	maxBody, err := envInt("MAX_BODY_BYTES", int(DefaultMaxBodyBytes), 1, 1<<30)
	if err != nil {
		return nil, err
	}
	cfg.MaxBodyBytes = int64(maxBody)

	logLevel := slog.LevelInfo
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		if err := logLevel.UnmarshalText([]byte(v)); err != nil {
			return nil, fmt.Errorf("invalid LOG_LEVEL: %q", v)
		}
	}
	cfg.LogLevel = logLevel

	if cfg.AllowUnsignedWebhooks, err = envBool("ALLOW_UNSIGNED_WEBHOOKS"); err != nil {
		return nil, err
	}
	if cfg.AllowUnauthenticated, err = envBool("ALLOW_UNAUTHENTICATED_API"); err != nil {
		return nil, err
	}

	cfg.APIKey = os.Getenv("API_KEY")
	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	cfg.RedisURL = os.Getenv("REDIS_URL")
	cfg.RulesPath = os.Getenv("RULES_PATH")
	cfg.DefaultNotifyEmail = os.Getenv("DEFAULT_NOTIFY_EMAIL")
	cfg.SMTPHost = os.Getenv("SMTP_HOST")
	cfg.SMTPUsername = os.Getenv("SMTP_USERNAME")
	cfg.SMTPPassword = os.Getenv("SMTP_PASSWORD")
	cfg.SMTPSender = os.Getenv("SMTP_SENDER")
	cfg.SlackWebhookURL = os.Getenv("SLACK_WEBHOOK_URL")
	cfg.TeamsWebhookURL = os.Getenv("TEAMS_WEBHOOK_URL")
	cfg.GitHubWebhookSecret = os.Getenv("GITHUB_WEBHOOK_SECRET")

	cfg.AuthBootstrapEmail = strings.TrimSpace(os.Getenv("AUTH_BOOTSTRAP_EMAIL"))
	cfg.AuthBootstrapPassword = os.Getenv("AUTH_BOOTSTRAP_PASSWORD")
	if (cfg.AuthBootstrapEmail == "") != (cfg.AuthBootstrapPassword == "") {
		return nil, errors.New("AUTH_BOOTSTRAP_EMAIL and AUTH_BOOTSTRAP_PASSWORD must be set together")
	}
	if cfg.AuthSessionTTL, err = envDuration("AUTH_SESSION_TTL", 12*time.Hour, false); err != nil {
		return nil, err
	}
	cfg.AuthCookieSecure = strings.ToLower(strings.TrimSpace(envDefault("AUTH_COOKIE_SECURE", "auto")))
	if cfg.AuthCookieSecure != "auto" && cfg.AuthCookieSecure != "true" && cfg.AuthCookieSecure != "false" {
		return nil, fmt.Errorf("invalid AUTH_COOKIE_SECURE: %q (use auto, true or false)", cfg.AuthCookieSecure)
	}
	cfg.AuthCookieSameSite = strings.ToLower(strings.TrimSpace(envDefault("AUTH_COOKIE_SAMESITE", "lax")))
	switch cfg.AuthCookieSameSite {
	case "lax", "strict", "none":
	default:
		return nil, fmt.Errorf("invalid AUTH_COOKIE_SAMESITE: %q (use lax, strict or none)", cfg.AuthCookieSameSite)
	}
	cfg.AuthSecret = os.Getenv("AUTH_SECRET")
	if cfg.OverridesAdminOnly, err = envBool("OVERRIDES_ADMIN_ONLY"); err != nil {
		return nil, err
	}
	cfg.PublicURL = strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/")
	cfg.ConsoleURL = strings.TrimSpace(os.Getenv("CONSOLE_URL"))
	cfg.EntraTenantID = strings.TrimSpace(os.Getenv("ENTRA_TENANT_ID"))
	cfg.OIDCIssuer = strings.TrimSpace(os.Getenv("OIDC_ISSUER"))
	cfg.OIDCName = strings.TrimSpace(os.Getenv("OIDC_NAME"))
	cfg.OIDCClientID = strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID"))
	cfg.OIDCClientSecret = os.Getenv("OIDC_CLIENT_SECRET")
	cfg.OIDCAllowedDomains = envList("OIDC_ALLOWED_DOMAINS", "")

	cfg.SMSGateway = strings.ToLower(strings.TrimSpace(os.Getenv("SMS_GATEWAY")))
	switch cfg.SMSGateway {
	case "", "mock", "http", "sns":
	default:
		return nil, fmt.Errorf("invalid SMS_GATEWAY: %q (use mock, http or sns)", cfg.SMSGateway)
	}
	cfg.SMSRecipients = envList("SMS_RECIPIENTS", "")
	cfg.SMSHTTPURL = os.Getenv("SMS_HTTP_URL")
	cfg.SMSHTTPBody = os.Getenv("SMS_HTTP_BODY")
	if cfg.SMSHTTPHeaders, err = parseHeaders(os.Getenv("SMS_HTTP_HEADERS")); err != nil {
		return nil, err
	}
	if cfg.SMSDailyLimit, err = envInt("SMS_DAILY_LIMIT", defaultSMSDailyLimit, 0, 100000); err != nil {
		return nil, err
	}
	cfg.AWSRegion = os.Getenv("AWS_REGION")
	cfg.AWSAccessKeyID = os.Getenv("AWS_ACCESS_KEY_ID")
	cfg.AWSSecretAccessKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	cfg.AWSSessionToken = os.Getenv("AWS_SESSION_TOKEN")
	cfg.StandardChannels = envList("NOTIFY_STANDARD_CHANNELS", "")
	cfg.DigestChannels = envList("NOTIFY_DIGEST_CHANNELS", "")
	cfg.AttentionClassify = true
	if v := os.Getenv("ATTENTION_CLASSIFY"); v != "" {
		if cfg.AttentionClassify, err = envBool("ATTENTION_CLASSIFY"); err != nil {
			return nil, err
		}
	}
	if cfg.AttentionDigestEvery, err = envDuration("ATTENTION_DIGEST_EVERY", 0, true); err != nil {
		return nil, err
	}
	cfg.BackgroundChannels = envList("NOTIFY_BACKGROUND_CHANNELS", "")
	cfg.AttentionProvider = strings.ToLower(envDefault("ATTENTION_PROVIDER", ProviderHeuristic))
	switch cfg.AttentionProvider {
	case ProviderHeuristic, ProviderOllama, ProviderClaude:
	default:
		return nil, fmt.Errorf("invalid ATTENTION_PROVIDER: %q (use heuristic, ollama or claude)", cfg.AttentionProvider)
	}
	cfg.OllamaURL = os.Getenv("OLLAMA_URL")
	cfg.OllamaModel = os.Getenv("OLLAMA_MODEL")
	cfg.AnthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")
	cfg.AttentionModel = os.Getenv("ATTENTION_MODEL")
	if cfg.AttentionProvider == ProviderClaude && strings.TrimSpace(cfg.AnthropicAPIKey) == "" {
		return nil, errors.New("ATTENTION_PROVIDER=claude requires ANTHROPIC_API_KEY")
	}
	if cfg.AttentionTimeout, err = envDuration("ATTENTION_TIMEOUT", defaultAttentionTimeout, false); err != nil {
		return nil, err
	}
	if cfg.AttentionMaxCallsPerMin, err = envInt("ATTENTION_MAX_CALLS_PER_MIN", defaultAttentionMaxCalls, 1, 100000); err != nil {
		return nil, err
	}
	cfg.CORSAllowedOrigins = envList("CORS_ALLOWED_ORIGINS", "")
	cfg.AllowedWebhookHosts = envList("NOTIFY_ALLOWED_WEBHOOK_HOSTS", defaultWebhookHostList)
	cfg.AllowedEmailDomains = envList("NOTIFY_ALLOWED_EMAIL_DOMAINS", "")

	return &cfg, nil
}

// DefaultEmailRecipients returns DEFAULT_NOTIFY_EMAIL as a trimmed list.
func (c *Config) DefaultEmailRecipients() []string {
	return splitList(c.DefaultNotifyEmail)
}

// SlackWebhookURLs returns the configured Slack webhook, if any.
func (c *Config) SlackWebhookURLs() []string {
	if c.SlackWebhookURL == "" {
		return nil
	}
	return []string{c.SlackWebhookURL}
}

// TeamsWebhookURLs returns the configured Teams webhook, if any.
func (c *Config) TeamsWebhookURLs() []string {
	if c.TeamsWebhookURL == "" {
		return nil
	}
	return []string{c.TeamsWebhookURL}
}

// WebhookHostAllowList returns the hosts outbound webhooks may target: the
// configured allow-list plus the hosts of the operator-configured webhook URLs.
func (c *Config) WebhookHostAllowList() []string {
	hosts := append([]string{}, c.AllowedWebhookHosts...)
	for _, raw := range []string{c.SlackWebhookURL, c.TeamsWebhookURL} {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			hosts = append(hosts, u.Hostname())
		}
	}
	return hosts
}

func envInt(name string, def, min, max int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("invalid %s: out of range", name)
	}
	return n, nil
}

func envDuration(name string, def time.Duration, allowZero bool) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 || (d == 0 && !allowZero) {
		return 0, fmt.Errorf("invalid %s: %q", name, v)
	}
	return d, nil
}

func envBool(name string) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %q", name, v)
	}
	return b, nil
}

// defaultSMSDailyLimit bounds queued SMS spend until an operator chooses otherwise.
const defaultSMSDailyLimit = 50

// parseHeaders reads "Name=value,Name2=value2" into a map.
func parseHeaders(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range splitList(s) {
		name, value, ok := strings.Cut(pair, "=")
		if name = strings.TrimSpace(name); !ok || name == "" {
			return nil, fmt.Errorf("invalid SMS_HTTP_HEADERS entry %q (use Name=value)", pair)
		}
		out[name] = strings.TrimSpace(value)
	}
	return out, nil
}

func envDefault(name, def string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return def
}

func envList(name, def string) []string {
	v, ok := os.LookupEnv(name)
	if !ok {
		v = def
	}
	return splitList(v)
}

// splitList splits a comma-separated list, trimming blanks and dropping empties.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Attention Intelligence providers for ATTENTION_PROVIDER.
const (
	ProviderHeuristic = "heuristic"
	ProviderOllama    = "ollama"
	ProviderClaude    = "claude"
)

const (
	defaultAttentionTimeout  = 5 * time.Second
	defaultAttentionMaxCalls = 60
)
