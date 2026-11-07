package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/agentpulse/notification-service/pkg/preferences"
	"github.com/agentpulse/notification-service/pkg/stream"
	"github.com/agentpulse/notification-service/pkg/util"
)

const (
	// heartbeatInterval keeps proxies from closing an idle stream.
	heartbeatInterval = 25 * time.Second
	// streamWriteWindow is how long one write to a live stream may take before the client is dropped.
	streamWriteWindow  = 10 * time.Second
	maxLiveConnections = 100
)

// preferencesHandler serves GET and PUT /api/preferences.
//
// Preferences apply to standard and background events only; critical events are
// never delayed, muted or downgraded (see pkg/preferences).
func (s *Server) preferencesHandler(w http.ResponseWriter, r *http.Request) {
	if s.preferences == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "preferences are not available"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		util.WriteJSON(w, http.StatusOK, s.preferences.Get())
	case http.MethodPut:
		if !s.requireAdmin(w, r) {
			return
		}
		var in preferences.Preferences
		if !s.decodeJSON(w, r, &in) {
			return
		}
		saved, err := s.preferences.Update(r.Context(), in)
		switch {
		case errors.Is(err, preferences.ErrInvalid):
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
		case err != nil:
			s.logFor(r.Context()).Error("save preferences failed", "error", err)
			util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		default:
			s.publish(stream.Message{Type: stream.TypeInbox})
			util.WriteJSON(w, http.StatusOK, saved)
		}
	default:
		methodNotAllowed(w, "GET, PUT")
	}
}

// publish tells live consoles something changed. It never blocks and does nothing without a broker.
func (s *Server) publish(m stream.Message) {
	if s.broker != nil {
		s.broker.Publish(m)
	}
}

// streamHandler serves GET /api/stream as server-sent events. Messages carry no
// notification content: they only tell the console to refresh. The browser
// EventSource API cannot send the API key header, so the console reads this
// stream with fetch; the key never goes in the URL.
func (s *Server) streamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if s.broker == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "live updates are not available"})
		return
	}
	sub, err := s.broker.Subscribe()
	if err != nil {
		w.Header().Set("Retry-After", "30")
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: err.Error()})
		return
	}
	defer sub.Close()

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // tell nginx not to buffer the stream

	write := func(format string, args ...any) bool {
		// The server write timeout is for normal requests; give each write its own window.
		if err := rc.SetWriteDeadline(time.Now().Add(streamWriteWindow)); err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write("retry: 5000\n\n") || !writeMessage(write, stream.Message{Type: stream.TypeReady}) {
		return
	}

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdown:
			return
		case m := <-sub.C:
			if !writeMessage(write, m) {
				return
			}
		case <-heartbeat.C:
			if !write(": keep-alive\n\n") {
				return
			}
		}
	}
}

func writeMessage(write func(string, ...any) bool, m stream.Message) bool {
	data, err := json.Marshal(m)
	if err != nil {
		return false
	}
	return write("event: %s\ndata: %s\n\n", m.Type, data)
}
