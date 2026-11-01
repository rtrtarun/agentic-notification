package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address               string
	APIKey                string
	DatabaseURL           string
	RulesPath             string
	DedupWindow           time.Duration
	DefaultNotifyEmail    string
	SMTPHost              string
	SMTPPort              int
	SMTPUsername          string
	SMTPPassword          string
	SMTPSender            string
	SlackWebhookURL       string
	TeamsWebhookURL       string
	GitHubWebhookSecret   string
	AllowUnsignedWebhooks bool
	MaxBodyBytes          int64
	LogLevel              slog.Level
}

func LoadConfig() (*Config, error) {
	port := 8080
	if v := os.Getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT: %w", err)
		}
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("invalid PORT: out of range")
		}
		port = p
	}

	dedupWindow := 10 * time.Minute
	if v := os.Getenv("DEDUP_WINDOW"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid DEDUP_WINDOW: %q", v)
		}
		dedupWindow = d
	}

	smtpPort := 587
	if v := os.Getenv("SMTP_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid SMTP_PORT: %w", err)
		}
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("invalid SMTP_PORT: out of range")
		}
		smtpPort = p
	}

	maxBody := int64(1 << 20)
	if v := os.Getenv("MAX_BODY_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid MAX_BODY_BYTES: %q", v)
		}
		maxBody = n
	}

	logLevel := slog.LevelInfo
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		if err := logLevel.UnmarshalText([]byte(v)); err != nil {
			return nil, fmt.Errorf("invalid LOG_LEVEL: %q", v)
		}
	}

	allowUnsigned := false
	if v := os.Getenv("ALLOW_UNSIGNED_WEBHOOKS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid ALLOW_UNSIGNED_WEBHOOKS: %q", v)
		}
		allowUnsigned = b
	}

	return &Config{
		Address:               ":" + strconv.Itoa(port),
		APIKey:                os.Getenv("API_KEY"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		RulesPath:             os.Getenv("RULES_PATH"),
		DedupWindow:           dedupWindow,
		DefaultNotifyEmail:    os.Getenv("DEFAULT_NOTIFY_EMAIL"),
		SMTPHost:              os.Getenv("SMTP_HOST"),
		SMTPPort:              smtpPort,
		SMTPUsername:          os.Getenv("SMTP_USERNAME"),
		SMTPPassword:          os.Getenv("SMTP_PASSWORD"),
		SMTPSender:            os.Getenv("SMTP_SENDER"),
		SlackWebhookURL:       os.Getenv("SLACK_WEBHOOK_URL"),
		TeamsWebhookURL:       os.Getenv("TEAMS_WEBHOOK_URL"),
		GitHubWebhookSecret:   os.Getenv("GITHUB_WEBHOOK_SECRET"),
		AllowUnsignedWebhooks: allowUnsigned,
		MaxBodyBytes:          maxBody,
		LogLevel:              logLevel,
	}, nil
}

func (c *Config) DefaultEmailRecipients() []string {
	if c.DefaultNotifyEmail == "" {
		return nil
	}
	return strings.Split(c.DefaultNotifyEmail, ",")
}

func (c *Config) SlackWebhookURLs() []string {
	if c.SlackWebhookURL == "" {
		return nil
	}
	return []string{c.SlackWebhookURL}
}
