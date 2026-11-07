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

func TestSMSAndRoutingSettings(t *testing.T) {
	t.Setenv("SMS_GATEWAY", " Mock ")
	t.Setenv("SMS_RECIPIENTS", "+14155550100, +14155550101")
	t.Setenv("SMS_HTTP_HEADERS", "Authorization=Bearer abc, X-Key=1")
	t.Setenv("SMS_DAILY_LIMIT", "5")
	t.Setenv("NOTIFY_STANDARD_CHANNELS", "slack, email")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMSGateway != "mock" || len(cfg.SMSRecipients) != 2 || cfg.SMSDailyLimit != 5 {
		t.Fatalf("sms settings = %+v", cfg)
	}
	if cfg.SMSHTTPHeaders["Authorization"] != "Bearer abc" || cfg.SMSHTTPHeaders["X-Key"] != "1" {
		t.Fatalf("headers = %v", cfg.SMSHTTPHeaders)
	}
	if len(cfg.StandardChannels) != 2 || len(cfg.BackgroundChannels) != 0 {
		t.Fatalf("routes = %v / %v", cfg.StandardChannels, cfg.BackgroundChannels)
	}
}

func TestSMSSettingsAreValidated(t *testing.T) {
	t.Setenv("SMS_GATEWAY", "carrier-pigeon")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("an unknown gateway must be rejected")
	}
	t.Setenv("SMS_GATEWAY", "")
	t.Setenv("SMS_HTTP_HEADERS", "no-equals-sign")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("a malformed header must be rejected")
	}
}

func TestSMSDefaults(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMSGateway != "" || cfg.SMSDailyLimit != 50 || len(cfg.StandardChannels) != 0 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestAttentionProviderSettings(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AttentionProvider != ProviderHeuristic || cfg.AttentionTimeout != 5*time.Second || cfg.AttentionMaxCallsPerMin != 60 {
		t.Fatalf("defaults wrong: %q %v %d", cfg.AttentionProvider, cfg.AttentionTimeout, cfg.AttentionMaxCallsPerMin)
	}

	t.Setenv("ATTENTION_PROVIDER", "Ollama")
	t.Setenv("OLLAMA_URL", "http://ollama:11434")
	t.Setenv("OLLAMA_MODEL", "llama3.2")
	t.Setenv("ATTENTION_TIMEOUT", "8s")
	t.Setenv("ATTENTION_MAX_CALLS_PER_MIN", "10")
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AttentionProvider != ProviderOllama || cfg.OllamaURL != "http://ollama:11434" || cfg.OllamaModel != "llama3.2" ||
		cfg.AttentionTimeout != 8*time.Second || cfg.AttentionMaxCallsPerMin != 10 {
		t.Fatalf("settings not read: %+v", cfg)
	}
}

func TestAttentionProviderValidation(t *testing.T) {
	cases := map[string]map[string]string{
		"unknown provider":   {"ATTENTION_PROVIDER": "gpt"},
		"claude without key": {"ATTENTION_PROVIDER": "claude"},
		"claude blank key":   {"ATTENTION_PROVIDER": "claude", "ANTHROPIC_API_KEY": "  "},
		"bad timeout":        {"ATTENTION_TIMEOUT": "soon"},
		"zero timeout":       {"ATTENTION_TIMEOUT": "0s"},
		"zero calls per min": {"ATTENTION_MAX_CALLS_PER_MIN": "0"},
		"calls not a number": {"ATTENTION_MAX_CALLS_PER_MIN": "many"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := LoadConfig(); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	t.Setenv("ATTENTION_PROVIDER", "claude")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if _, err := LoadConfig(); err != nil {
		t.Fatalf("claude with a key should load: %v", err)
	}
}

func TestLearningSettings(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil || cfg.LearningMinSignals != 5 || cfg.LearningLookback != 30*24*time.Hour {
		t.Fatalf("defaults wrong: %v %+v", err, cfg)
	}
	t.Setenv("LEARNING_MIN_SIGNALS", "8")
	t.Setenv("LEARNING_LOOKBACK", "168h")
	cfg, err = LoadConfig()
	if err != nil || cfg.LearningMinSignals != 8 || cfg.LearningLookback != 168*time.Hour {
		t.Fatalf("settings not read: %v %+v", err, cfg)
	}
	for name, env := range map[string]map[string]string{
		"zero signals": {"LEARNING_MIN_SIGNALS": "0"},
		"bad lookback": {"LEARNING_LOOKBACK": "forever"},
		"zero look":    {"LEARNING_LOOKBACK": "0s"},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := LoadConfig(); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestAttentionWorkerAndBudgetSettings(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AttentionAsync || cfg.AttentionWorkerInterval != 2*time.Second || cfg.AttentionBudgetPerHour != 10 {
		t.Fatalf("defaults wrong: %v %+v", err, cfg)
	}
	t.Setenv("ATTENTION_ASYNC", "true")
	t.Setenv("ATTENTION_WORKER_INTERVAL", "500ms")
	t.Setenv("ATTENTION_BUDGET_PER_HOUR", "0")
	cfg, err = LoadConfig()
	if err != nil || !cfg.AttentionAsync || cfg.AttentionWorkerInterval != 500*time.Millisecond || cfg.AttentionBudgetPerHour != 0 {
		t.Fatalf("settings not read: %v %+v", err, cfg)
	}
	for name, env := range map[string]map[string]string{
		"bad async":      {"ATTENTION_ASYNC": "maybe"},
		"bad interval":   {"ATTENTION_WORKER_INTERVAL": "often"},
		"zero interval":  {"ATTENTION_WORKER_INTERVAL": "0s"},
		"negative cap":   {"ATTENTION_BUDGET_PER_HOUR": "-3"},
		"cap not number": {"ATTENTION_BUDGET_PER_HOUR": "lots"},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := LoadConfig(); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
