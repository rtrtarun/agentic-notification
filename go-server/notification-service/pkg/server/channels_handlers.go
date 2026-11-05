package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/example/notification-service/pkg/channels"
	"github.com/example/notification-service/pkg/integrations"
	"github.com/example/notification-service/pkg/util"
)

const (
	channelsPrefix = "/api/channels"
	// channelTestGap is the shortest time between tests of one channel.
	channelTestGap = 10 * time.Second
)

func (s *Server) channelSettings() channels.Settings {
	return channels.Settings{
		SMTPHost: s.cfg.SMTPHost, SMTPSender: s.cfg.SMTPSender, EmailRecipients: s.cfg.DefaultEmailRecipients(),
		SlackWebhookURL: s.cfg.SlackWebhookURL, TeamsWebhookURL: s.cfg.TeamsWebhookURL,
	}
}

// channelsHandler routes /api/channels and /api/channels/{id}/test.
//
//	GET  /api/channels            every channel with a masked target and whether it is configured
//	POST /api/channels/{id}/test  send one labelled test message through that channel only
func (s *Server) channelsHandler(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, channelsPrefix), "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"channels": channels.Describe(s.channelSettings())})
		return
	}
	id, action, ok := strings.Cut(rest, "/")
	if !ok || action != "test" {
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if s.channelTester == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "channel tests are not available"})
		return
	}
	switch err := s.channelTester.Test(r.Context(), id); {
	case err == nil:
		util.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	case errors.Is(err, channels.ErrUnknown):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, channels.ErrNotConfigured):
		util.WriteJSON(w, http.StatusConflict, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, channels.ErrTooSoon):
		w.Header().Set("Retry-After", "10")
		util.WriteJSON(w, http.StatusTooManyRequests, util.ErrorResponse{Error: err.Error()})
	default:
		// The send error can name hosts or addresses; log it, tell the caller only that it failed.
		s.logFor(r.Context()).Error("channel test failed", "channel", id, "error", err)
		util.WriteJSON(w, http.StatusBadGateway, util.ErrorResponse{Error: "the test message could not be delivered; check the service logs"})
	}
}

// integrationsHandler serves GET /api/integrations.
func (s *Server) integrationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	events, err := s.eventRepo.List(r.Context())
	if err != nil {
		s.logFor(r.Context()).Error("list events for integrations failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		return
	}
	github := integrations.GitHub(integrations.GitHubSettings{
		SecretConfigured: s.cfg.GitHubWebhookSecret != "",
		AllowUnsigned:    s.cfg.AllowUnsignedWebhooks,
	}, events)
	util.WriteJSON(w, http.StatusOK, map[string]any{"integrations": []integrations.Integration{github}})
}
