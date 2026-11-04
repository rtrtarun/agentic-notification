package config

import (
	"reflect"
	"testing"
	"time"
)

func TestHardeningDefaultsFailClosed(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.AllowUnauthenticated {
		t.Fatal("unauthenticated API access must be opt-in")
	}
	if len(cfg.CORSAllowedOrigins) != 0 {
		t.Fatalf("CORS must be closed by default, got %v", cfg.CORSAllowedOrigins)
	}
	if cfg.RequestTimeout != DefaultRequestTimeout || cfg.EventRetention != DefaultEventRetention {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.DBMaxOpenConns != DefaultDBMaxOpenConns || cfg.DBMaxIdleConns != DefaultDBMaxIdleConns ||
		cfg.DBConnMaxLifetime != DefaultDBConnLifetime {
		t.Fatalf("unexpected pool defaults: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.AllowedWebhookHosts, []string{
		"hooks.slack.com", "outlook.office.com", "outlook.office365.com", "*.webhook.office.com",
	}) {
		t.Fatalf("unexpected webhook host defaults: %v", cfg.AllowedWebhookHosts)
	}
}

func TestHardeningEnvironmentIsApplied(t *testing.T) {
	t.Setenv("ALLOW_UNAUTHENTICATED_API", "true")
	t.Setenv("CORS_ALLOWED_ORIGINS", " http://localhost:5173 , https://app.example.com ")
	t.Setenv("NOTIFY_ALLOWED_WEBHOOK_HOSTS", "hooks.example.com")
	t.Setenv("NOTIFY_ALLOWED_EMAIL_DOMAINS", "example.com,corp.example.com")
	t.Setenv("REQUEST_TIMEOUT", "5s")
	t.Setenv("EVENT_RETENTION", "720h")
	t.Setenv("DB_MAX_OPEN_CONNS", "40")
	t.Setenv("DB_MAX_IDLE_CONNS", "10")
	t.Setenv("DB_CONN_MAX_LIFETIME", "10m")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.AllowUnauthenticated || cfg.RequestTimeout != 5*time.Second || cfg.EventRetention != 720*time.Hour ||
		cfg.DBMaxOpenConns != 40 || cfg.DBMaxIdleConns != 10 || cfg.DBConnMaxLifetime != 10*time.Minute ||
		cfg.RedisURL != "redis://localhost:6379/0" {
		t.Fatalf("environment not applied: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.CORSAllowedOrigins, []string{"http://localhost:5173", "https://app.example.com"}) {
		t.Fatalf("origins not trimmed: %v", cfg.CORSAllowedOrigins)
	}
	if !reflect.DeepEqual(cfg.AllowedWebhookHosts, []string{"hooks.example.com"}) {
		t.Fatalf("webhook hosts: %v", cfg.AllowedWebhookHosts)
	}
	if !reflect.DeepEqual(cfg.AllowedEmailDomains, []string{"example.com", "corp.example.com"}) {
		t.Fatalf("email domains: %v", cfg.AllowedEmailDomains)
	}
}

func TestZeroRetentionDisablesPurging(t *testing.T) {
	t.Setenv("EVENT_RETENTION", "0")
	cfg, err := LoadConfig()
	if err != nil || cfg.EventRetention != 0 {
		t.Fatalf("EVENT_RETENTION=0 must be accepted: %v %v", cfg, err)
	}
}

func TestEmptyListVariablesOverrideDefaults(t *testing.T) {
	t.Setenv("NOTIFY_ALLOWED_WEBHOOK_HOSTS", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.AllowedWebhookHosts) != 0 {
		t.Fatalf("an explicitly empty allow-list must reject all webhooks, got %v", cfg.AllowedWebhookHosts)
	}
}

func TestHardeningRejectsInvalidValues(t *testing.T) {
	tests := []struct{ env, value string }{
		{"ALLOW_UNAUTHENTICATED_API", "maybe"},
		{"REQUEST_TIMEOUT", "0"},
		{"REQUEST_TIMEOUT", "soon"},
		{"EVENT_RETENTION", "-1h"},
		{"EVENT_RETENTION", "forever"},
		{"DB_MAX_OPEN_CONNS", "0"},
		{"DB_MAX_OPEN_CONNS", "many"},
		{"DB_MAX_IDLE_CONNS", "-1"},
		{"DB_CONN_MAX_LIFETIME", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.env+"="+tt.value, func(t *testing.T) {
			t.Setenv(tt.env, tt.value)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("expected error for %s=%s", tt.env, tt.value)
			}
		})
	}
}

func TestRecipientListIsTrimmed(t *testing.T) {
	cfg := Config{DefaultNotifyEmail: " a@x.com , b@x.com ,, "}
	if got := cfg.DefaultEmailRecipients(); !reflect.DeepEqual(got, []string{"a@x.com", "b@x.com"}) {
		t.Fatalf("recipients = %v", got)
	}
}

func TestTeamsWebhookURLs(t *testing.T) {
	if (&Config{}).TeamsWebhookURLs() != nil {
		t.Fatal("unset Teams URL must yield nil")
	}
	got := (&Config{TeamsWebhookURL: "https://x.webhook.office.com/a"}).TeamsWebhookURLs()
	if !reflect.DeepEqual(got, []string{"https://x.webhook.office.com/a"}) {
		t.Fatalf("got %v", got)
	}
}

func TestWebhookHostAllowListIncludesConfiguredURLs(t *testing.T) {
	cfg := Config{
		AllowedWebhookHosts: []string{"hooks.slack.com"},
		SlackWebhookURL:     "https://chat.internal.example.com/hook",
		TeamsWebhookURL:     "not a url ://",
	}
	got := cfg.WebhookHostAllowList()
	want := []string{"hooks.slack.com", "chat.internal.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("allow-list = %v, want %v", got, want)
	}
	if len(cfg.AllowedWebhookHosts) != 1 {
		t.Fatal("WebhookHostAllowList must not mutate the config slice")
	}
}
