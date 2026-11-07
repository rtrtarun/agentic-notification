package server

import (
	"errors"
	"net/http"

	"github.com/agentpulse/notification-service/pkg/attention"
	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/preferences"
	"github.com/agentpulse/notification-service/pkg/util"
)

const (
	// scopeMe is the ?scope= value that asks for the signed-in person's own view.
	scopeMe = "me"
	// mePreferencesPath is the signed-in person's own preferences.
	mePreferencesPath = "/api/me/preferences"
)

// person returns the signed-in person. A service holding the API key is not a person,
// so personal views are refused for it with a message that says what to do.
func (s *Server) person(w http.ResponseWriter, r *http.Request) (id, actor string, ok bool) {
	if p, found := principalFrom(r.Context()); found && !p.service && p.user.ID != "" {
		return p.user.ID, p.actor(), true
	}
	util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "personal views are for signed-in people; sign in, or use the workspace view"})
	return "", "", false
}

// mePreferencesHandler reads and saves the signed-in person's own preferences. A person
// who has saved none sees the workspace's, flagged as not personal. What a person saves
// shapes their own digest only; it never changes what the pipeline does for anyone else.
//
//	GET /api/me/preferences
//	PUT /api/me/preferences   the same body as /api/preferences
func (s *Server) mePreferencesHandler(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.person(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, personal, err := s.preferences.GetFor(r.Context(), id)
		if err != nil {
			s.logFor(r.Context()).Error("read personal preferences failed", "error", err)
			util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"preferences": p, "personal": personal})
	case http.MethodPut:
		var in preferences.Preferences
		if !s.decodeJSON(w, r, &in) {
			return
		}
		saved, err := s.preferences.UpdateFor(r.Context(), id, in)
		switch {
		case errors.Is(err, preferences.ErrInvalid):
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
		case err != nil:
			s.logFor(r.Context()).Error("save personal preferences failed", "error", err)
			util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
		default:
			util.WriteJSON(w, http.StatusOK, map[string]any{"preferences": saved, "personal": true})
		}
	default:
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
	}
}

// listPersonalDigests is GET /api/digests?scope=me.
func (s *Server) listPersonalDigests(w http.ResponseWriter, r *http.Request, limit int) {
	id, _, ok := s.person(w, r)
	if !ok {
		return
	}
	list, err := s.digestStore.ListFor(r.Context(), id, limit)
	if err != nil {
		s.attentionFailure(w, r, err)
		return
	}
	if list == nil {
		list = []attention.Digest{}
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"digests": list, "every": "0s", "personal": true})
}

// generatePersonalDigest is POST /api/digests/generate?scope=me. Any signed-in person
// may make their own; it is stored and readable but not delivered.
func (s *Server) generatePersonalDigest(w http.ResponseWriter, r *http.Request) {
	id, actor, ok := s.person(w, r)
	if !ok {
		return
	}
	prefs, _, err := s.preferences.GetFor(r.Context(), id)
	if err != nil {
		s.attentionFailure(w, r, err)
		return
	}
	exclude := func(e event.Event) bool { return prefs.Muted(e) || s.overrides.NeverFor(actor, e) }
	d, made, err := s.digests.GenerateFor(r.Context(), actor, id, exclude)
	if err != nil {
		s.attentionFailure(w, r, err)
		return
	}
	if !made {
		util.WriteJSON(w, http.StatusOK, map[string]any{"generated": false, "digest": nil})
		return
	}
	util.WriteJSON(w, http.StatusCreated, map[string]any{"generated": true, "digest": d})
}
