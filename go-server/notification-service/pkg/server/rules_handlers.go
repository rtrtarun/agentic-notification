package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/example/notification-service/pkg/policy"
	"github.com/example/notification-service/pkg/rules"
	"github.com/example/notification-service/pkg/util"
)

const rulesPrefix = "/api/rules"

// rulesHandler routes /api/rules and everything under it.
//
//	GET    /api/rules              list in evaluation order
//	POST   /api/rules              create (appended after existing rules)
//	POST   /api/rules/reorder      {ids: [...]} every id exactly once, locked rules first
//	POST   /api/rules/test         dry run a sample event; nothing is stored or delivered
//	PUT    /api/rules/{id}         replace an unlocked rule
//	DELETE /api/rules/{id}         remove an unlocked rule
func (s *Server) rulesHandler(w http.ResponseWriter, r *http.Request) {
	if s.rules == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "rules are not available"})
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, rulesPrefix), "/")
	switch {
	case rest == "":
		s.rulesCollection(w, r)
	case rest == "reorder":
		s.rulesReorder(w, r)
	case rest == "test":
		s.rulesTest(w, r)
	case !strings.Contains(rest, "/"):
		s.rulesItem(w, r, rest)
	default:
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
	}
}

func (s *Server) rulesCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.rules.List(r.Context())
		if err != nil {
			s.rulesFailed(w, r, "list rules", err)
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"rules": nonNil(list)})
	case http.MethodPost:
		var in policy.Rule
		if !s.decodeJSON(w, r, &in) {
			return
		}
		created, err := s.rules.Create(r.Context(), in)
		if err != nil {
			s.rulesFailed(w, r, "create rule", err)
			return
		}
		util.WriteJSON(w, http.StatusCreated, created)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (s *Server) rulesItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPut:
		var in policy.Rule
		if !s.decodeJSON(w, r, &in) {
			return
		}
		updated, err := s.rules.Update(r.Context(), id, in)
		if err != nil {
			s.rulesFailed(w, r, "update rule", err)
			return
		}
		util.WriteJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := s.rules.Delete(r.Context(), id); err != nil {
			s.rulesFailed(w, r, "delete rule", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w, "PUT, DELETE")
	}
}

func (s *Server) rulesReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	if err := s.rules.Reorder(r.Context(), in.IDs); err != nil {
		s.rulesFailed(w, r, "reorder rules", err)
		return
	}
	list, err := s.rules.List(r.Context())
	if err != nil {
		s.rulesFailed(w, r, "list rules", err)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"rules": nonNil(list)})
}

func (s *Server) rulesTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var in rules.Sample
	if !s.decodeJSON(w, r, &in) {
		return
	}
	res, err := s.rules.Test(r.Context(), in)
	if err != nil {
		s.rulesFailed(w, r, "test rules", err)
		return
	}
	util.WriteJSON(w, http.StatusOK, res)
}

// policiesHandler serves GET /api/policies: the effective policy, read only.
func (s *Server) policiesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	var list []policy.Rule
	if s.rules != nil {
		var err error
		if list, err = s.rules.List(r.Context()); err != nil {
			s.rulesFailed(w, r, "list rules", err)
			return
		}
	}
	util.WriteJSON(w, http.StatusOK, policy.Describe(s.cfg.DedupWindow, s.cfg.EventRetention, list))
}

func (s *Server) rulesFailed(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, rules.ErrNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, rules.ErrLocked):
		util.WriteJSON(w, http.StatusConflict, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, rules.ErrInvalid):
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
	default:
		s.logFor(r.Context()).Error(what+" failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	}
}

// nonNil makes an empty list encode as [] instead of null.
func nonNil(list []policy.Rule) []policy.Rule {
	if list == nil {
		return []policy.Rule{}
	}
	return list
}
