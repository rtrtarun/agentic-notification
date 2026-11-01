package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address            string
	APIKey             string
	DatabaseURL        string
	RulesPath          string
	DedupWindow        time.Duration
	DefaultNotifyEmail string
	SMTPHost           string
	SMTPPort           int
	SMTPUsername       string
	SMTPPassword       string
	SMTPSender         string
	SlackWebhookURL    string
	TeamsWebhookURL    string
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

	return &Config{
		Address:            ":" + strconv.Itoa(port),
		APIKey:             os.Getenv("API_KEY"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		RulesPath:          os.Getenv("RULES_PATH"),
		DedupWindow:        dedupWindow,
		DefaultNotifyEmail: os.Getenv("DEFAULT_NOTIFY_EMAIL"),
		SMTPHost:           os.Getenv("SMTP_HOST"),
		SMTPPort:           smtpPort,
		SMTPUsername:       os.Getenv("SMTP_USERNAME"),
		SMTPPassword:       os.Getenv("SMTP_PASSWORD"),
		SMTPSender:         os.Getenv("SMTP_SENDER"),
		SlackWebhookURL:    os.Getenv("SLACK_WEBHOOK_URL"),
		TeamsWebhookURL:    os.Getenv("TEAMS_WEBHOOK_URL"),
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
