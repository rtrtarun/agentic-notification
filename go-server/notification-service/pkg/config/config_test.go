package config

import (
	"log/slog"
	"reflect"
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Address != ":8080" || cfg.SMTPPort != 587 {
		t.Fatalf("unexpected port defaults: %q %d", cfg.Address, cfg.SMTPPort)
	}
	if cfg.DedupWindow != 10*time.Minute || cfg.MaxBodyBytes != 1<<20 || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.AllowUnsignedWebhooks || cfg.GitHubWebhookSecret != "" {
		t.Fatal("webhooks must be locked down by default")
	}
}

func TestLoadConfigReadsEnvironment(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("DEDUP_WINDOW", "30s")
	t.Setenv("MAX_BODY_BYTES", "2048")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "shh")
	t.Setenv("ALLOW_UNSIGNED_WEBHOOKS", "true")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Address != ":9090" || cfg.SMTPPort != 2525 || cfg.DedupWindow != 30*time.Second ||
		cfg.MaxBodyBytes != 2048 || cfg.LogLevel != slog.LevelDebug ||
		cfg.GitHubWebhookSecret != "shh" || !cfg.AllowUnsignedWebhooks {
		t.Fatalf("environment not applied: %+v", cfg)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct{ env, value string }{
		{"PORT", "abc"},
		{"PORT", "0"},
		{"PORT", "70000"},
		{"SMTP_PORT", "x"},
		{"SMTP_PORT", "0"},
		{"DEDUP_WINDOW", "soon"},
		{"DEDUP_WINDOW", "-1m"},
		{"MAX_BODY_BYTES", "0"},
		{"MAX_BODY_BYTES", "lots"},
		{"LOG_LEVEL", "loud"},
		{"ALLOW_UNSIGNED_WEBHOOKS", "maybe"},
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

func TestRecipientHelpers(t *testing.T) {
	tests := []struct {
		name  string
		cfg   Config
		email []string
		slack []string
	}{
		{"empty", Config{}, nil, nil},
		{"configured", Config{DefaultNotifyEmail: "a@x.com,b@x.com", SlackWebhookURL: "https://hooks"},
			[]string{"a@x.com", "b@x.com"}, []string{"https://hooks"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.DefaultEmailRecipients(); !reflect.DeepEqual(got, tt.email) {
				t.Fatalf("email = %v, want %v", got, tt.email)
			}
			if got := tt.cfg.SlackWebhookURLs(); !reflect.DeepEqual(got, tt.slack) {
				t.Fatalf("slack = %v, want %v", got, tt.slack)
			}
		})
	}
}
