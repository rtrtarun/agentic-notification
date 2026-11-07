package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agentpulse/notification-service/pkg/inbox"
	"github.com/agentpulse/notification-service/pkg/stream"
	"github.com/agentpulse/notification-service/pkg/util"
)

const inboxPrefix = "/api/notifications"

// inboxHandler routes /api/notifications and everything under it. The router in
// handlers.go predates path parameters, so the few routes are matched here.
//
//	GET    /api/notifications                       list with filters, sort, paging
//	POST   /api/notifications/mark-all-read
//	POST   /api/notifications/bulk-actions
//	GET    /api/notifications/{id}                  detail with trail and related events
//	PATCH  /api/notifications/{id}                  read, archived, priority
//	DELETE /api/notifications/{id}                  hide the notification (the event is kept)
//	POST   /api/notifications/{id}/acknowledge|resolve|snooze
func (s *Server) inboxHandler(w http.ResponseWriter, r *http.Request) {
	if s.inbox == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "inbox is not available"})
		return
	}
	if r.Method != http.MethodGet {
		// A change made here should show up in other open consoles.
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			if rec.status() < http.StatusMultipleChoices {
				s.publish(stream.Message{Type: stream.TypeInbox})
			}
		}()
		w = rec
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, inboxPrefix), "/")
	parts := strings.Split(rest, "/")

	switch {
	case rest == "":
		s.inboxList(w, r)
	case rest == "mark-all-read":
		s.inboxMarkAllRead(w, r)
	case rest == "bulk-actions":
		s.inboxBulk(w, r)
	case len(parts) == 1:
		s.inboxItem(w, r, parts[0])
	case len(parts) == 2:
		s.inboxAction(w, r, parts[0], parts[1])
	default:
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
	}
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
}

func (s *Server) inboxList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	q, err := parseInboxQuery(r)
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
		return
	}
	page, err := s.inbox.List(r.Context(), q)
	if err != nil {
		s.inboxFailed(w, r, "list notifications", err)
		return
	}
	util.WriteJSON(w, http.StatusOK, page)
}

func (s *Server) inboxMarkAllRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	n, err := s.inbox.MarkAllRead(r.Context())
	if err != nil {
		s.inboxFailed(w, r, "mark all read", err)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]int{"changed": n})
}

type bulkRequest struct {
	IDs      []string `json:"ids"`
	Action   string   `json:"action"`
	Minutes  int      `json:"minutes"`
	Priority string   `json:"priority"`
}

func (s *Server) inboxBulk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var req bulkRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	action, err := buildAction(req.Action, req.Minutes, req.Priority, time.Now().UTC())
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
		return
	}
	results, err := s.inbox.Bulk(r.Context(), req.IDs, action)
	if err != nil {
		s.inboxFailed(w, r, "bulk action", err)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

type patchRequest struct {
	Read     *bool   `json:"read"`
	Archived *bool   `json:"archived"`
	Priority *string `json:"priority"`
}

func (s *Server) inboxItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		detail, err := s.inbox.Get(r.Context(), id)
		if err != nil {
			s.inboxFailed(w, r, "get notification", err)
			return
		}
		util.WriteJSON(w, http.StatusOK, detail)
	case http.MethodPatch:
		var req patchRequest
		if !s.decodeJSON(w, r, &req) {
			return
		}
		if req.Read == nil && req.Archived == nil && req.Priority == nil {
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "nothing to change: send read, archived or priority"})
			return
		}
		var actions []inbox.Action
		if req.Read != nil {
			actions = append(actions, inbox.Action{Type: pick(*req.Read, inbox.ActionRead, inbox.ActionUnread)})
		}
		if req.Archived != nil {
			actions = append(actions, inbox.Action{Type: pick(*req.Archived, inbox.ActionArchive, inbox.ActionUnarchive)})
		}
		if req.Priority != nil {
			actions = append(actions, inbox.Action{Type: inbox.ActionPriority, Priority: inbox.Level(*req.Priority)})
		}
		for _, a := range actions {
			if err := s.inbox.Apply(r.Context(), id, a); err != nil {
				s.inboxFailed(w, r, "update notification", err)
				return
			}
		}
		if req.Read != nil && *req.Read {
			s.recordOpened(r, id) // a signal for learning: someone looked at it
		}
		s.respondWithDetail(w, r, id)
	case http.MethodDelete:
		if err := s.inbox.Apply(r.Context(), id, inbox.Action{Type: inbox.ActionDelete}); err != nil {
			s.inboxFailed(w, r, "delete notification", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w, "GET, PATCH, DELETE")
	}
}

type snoozeRequest struct {
	Minutes int    `json:"minutes"`
	Until   string `json:"until"`
}

func (s *Server) inboxAction(w http.ResponseWriter, r *http.Request, id, name string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var action inbox.Action
	switch name {
	case "acknowledge":
		action = inbox.Action{Type: inbox.ActionAcknowledge}
	case "resolve":
		action = inbox.Action{Type: inbox.ActionResolve}
	case "snooze":
		var req snoozeRequest
		if !s.decodeJSON(w, r, &req) {
			return
		}
		until, err := snoozeUntil(req, time.Now().UTC())
		if err != nil {
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
			return
		}
		action = inbox.Action{Type: inbox.ActionSnooze, Until: until}
	default:
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
		return
	}
	if err := s.inbox.Apply(r.Context(), id, action); err != nil {
		s.inboxFailed(w, r, name, err)
		return
	}
	s.respondWithDetail(w, r, id)
}

func (s *Server) respondWithDetail(w http.ResponseWriter, r *http.Request, id string) {
	detail, err := s.inbox.Get(r.Context(), id)
	if err != nil {
		s.inboxFailed(w, r, "get notification", err)
		return
	}
	util.WriteJSON(w, http.StatusOK, detail)
}

// inboxFailed maps service errors to status codes; storage errors stay out of the response.
func (s *Server) inboxFailed(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, inbox.ErrNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, inbox.ErrInvalid):
		util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: err.Error()})
	default:
		s.logFor(r.Context()).Error(what+" failed", "error", err)
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	}
}

func pick(cond bool, yes, no inbox.ActionType) inbox.ActionType {
	if cond {
		return yes
	}
	return no
}

func snoozeUntil(req snoozeRequest, now time.Time) (time.Time, error) {
	switch {
	case req.Until != "":
		t, err := time.Parse(time.RFC3339, req.Until)
		if err != nil {
			return time.Time{}, errors.New("until must be an RFC 3339 time")
		}
		return t, nil
	case req.Minutes > 0:
		return now.Add(time.Duration(req.Minutes) * time.Minute), nil
	default:
		return time.Time{}, errors.New("send minutes or until")
	}
}

func buildAction(name string, minutes int, priority string, now time.Time) (inbox.Action, error) {
	switch inbox.ActionType(name) {
	case inbox.ActionRead, inbox.ActionUnread, inbox.ActionArchive, inbox.ActionUnarchive,
		inbox.ActionDelete, inbox.ActionAcknowledge, inbox.ActionResolve, inbox.ActionUnsnooze:
		return inbox.Action{Type: inbox.ActionType(name)}, nil
	case inbox.ActionSnooze:
		until, err := snoozeUntil(snoozeRequest{Minutes: minutes}, now)
		if err != nil {
			return inbox.Action{}, err
		}
		return inbox.Action{Type: inbox.ActionSnooze, Until: until}, nil
	case inbox.ActionPriority:
		return inbox.Action{Type: inbox.ActionPriority, Priority: inbox.Level(priority)}, nil
	default:
		return inbox.Action{}, fmt.Errorf("unknown action %q", name)
	}
}

func parseInboxQuery(r *http.Request) (inbox.Query, error) {
	v := r.URL.Query()
	q := inbox.Query{
		Text: strings.TrimSpace(v.Get("q")), Source: v.Get("source"), Repository: v.Get("repo"),
		Sort: v.Get("sort"), Cursor: v.Get("cursor"),
	}
	if len(q.Text) > 200 {
		return q, errors.New("q is too long")
	}
	switch q.Sort {
	case "", inbox.SortNewest, inbox.SortOldest, inbox.SortPriority, inbox.SortLowest, inbox.SortUpdated:
	default:
		return q, fmt.Errorf("sort must be one of newest, oldest, priority, lowest, updated")
	}
	for _, p := range splitCSV(v.Get("priority")) {
		level := inbox.Level(p)
		if !inbox.ValidLevel(level) {
			return q, fmt.Errorf("unknown priority %q", p)
		}
		q.Priorities = append(q.Priorities, level)
	}
	for _, o := range splitCSV(v.Get("outcome")) {
		switch out := inbox.Outcome(o); out {
		case inbox.OutcomeDelivered, inbox.OutcomeHeld, inbox.OutcomeSuppressed, inbox.OutcomeMerged, inbox.OutcomeRecorded:
			q.Outcomes = append(q.Outcomes, out)
		default:
			return q, fmt.Errorf("unknown outcome %q", o)
		}
	}
	var err error
	if q.Read, err = optionalBool(v.Get("read"), "read"); err != nil {
		return q, err
	}
	archived, err := optionalBool(v.Get("archived"), "archived")
	if err != nil {
		return q, err
	}
	q.Archived = archived != nil && *archived
	if q.From, err = optionalTime(v.Get("from"), "from"); err != nil {
		return q, err
	}
	if q.To, err = optionalTime(v.Get("to"), "to"); err != nil {
		return q, err
	}
	if q.Page, err = optionalInt(v.Get("page"), "page", 1, 1_000_000); err != nil {
		return q, err
	}
	if q.PageSize, err = optionalInt(v.Get("pageSize"), "pageSize", 1, inbox.MaxPageSize); err != nil {
		return q, err
	}
	return q, nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func optionalBool(raw, name string) (*bool, error) {
	if raw == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be true or false", name)
	}
	return &b, nil
}

func optionalTime(raw, name string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC 3339 time", name)
	}
	return &t, nil
}

func optionalInt(raw, name string, lo, hi int) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s must be a whole number from %d to %d", name, lo, hi)
	}
	return n, nil
}
