package server

import (
	"fmt"
	"log/slog"

	"github.com/example/notification-service/pkg/config"
	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/sms"
)

// buildSMSGateway chooses the SMS gateway from configuration. It returns nil
// when SMS is off. Nothing outside this file knows which vendor is behind it.
func buildSMSGateway(cfg *config.Config, logger *slog.Logger) (sms.Gateway, error) {
	switch cfg.SMSGateway {
	case "":
		if len(cfg.SMSRecipients) > 0 {
			logger.Warn("SMS_RECIPIENTS is set but SMS_GATEWAY is not; SMS is off")
		}
		return nil, nil
	case "mock":
		logger.Warn("SMS gateway is the mock: texts are logged and kept in memory, not sent")
		return sms.NewMock(logger), nil
	case "http":
		return sms.NewHTTP(cfg.SMSHTTPURL, cfg.SMSHTTPHeaders, cfg.SMSHTTPBody)
	case "sns":
		return sms.NewSNS(cfg.AWSRegion, sms.Credentials{
			AccessKeyID: cfg.AWSAccessKeyID, SecretAccessKey: cfg.AWSSecretAccessKey, SessionToken: cfg.AWSSessionToken,
		}, "")
	}
	return nil, fmt.Errorf("unknown SMS gateway %q", cfg.SMSGateway)
}

// buildTargets lists where each channel delivers. A channel that cannot send
// (email without SMTP, SMS without a gateway) gets no targets, so it is never routed to.
func buildTargets(cfg *config.Config, gw sms.Gateway) (delivery.Targets, error) {
	t := delivery.Targets{Slack: cfg.SlackWebhookURLs(), Teams: cfg.TeamsWebhookURLs()}
	if (cfg.SMTPHost != "" && cfg.SMTPSender != "") || cfg.NovuAPIKey != "" {
		t.Email = cfg.DefaultEmailRecipients()
	}
	if gw != nil {
		for _, n := range cfg.SMSRecipients {
			if !sms.ValidNumber(n) {
				return t, fmt.Errorf("SMS_RECIPIENTS has an invalid number: %w", sms.ErrInvalidNumber)
			}
		}
		t.SMS = cfg.SMSRecipients
	}
	return t, nil
}

// buildRoutes reads which channels receive standard and background events.
func buildRoutes(cfg *config.Config) (map[string][]string, error) {
	standard, err := delivery.ParseChannels(cfg.StandardChannels)
	if err != nil {
		return nil, fmt.Errorf("NOTIFY_STANDARD_CHANNELS: %w", err)
	}
	background, err := delivery.ParseChannels(cfg.BackgroundChannels)
	if err != nil {
		return nil, fmt.Errorf("NOTIFY_BACKGROUND_CHANNELS: %w", err)
	}
	digest, err := delivery.ParseChannels(cfg.DigestChannels)
	if err != nil {
		return nil, fmt.Errorf("NOTIFY_DIGEST_CHANNELS: %w", err)
	}
	return map[string][]string{"standard": standard, "background": background, "digest": digest}, nil
}

// initDeliveryStore keeps the queue in PostgreSQL when there is a database, otherwise in memory.
func (s *Server) initDeliveryStore() delivery.Store {
	if s.sqlDB == nil {
		return delivery.NewMemoryStore()
	}
	return delivery.NewPostgresStore(s.sqlDB)
}
