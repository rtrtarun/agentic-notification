package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/notification-service/pkg/delivery"
	"github.com/example/notification-service/pkg/util"
)

const (
	deliveriesPrefix     = "/api/deliveries"
	defaultDeliveryLimit = 50
	maxDeliveryLimit     = 200
)

// deliveriesHandler serves the delivery log and queue.
//
//	GET  /api/deliveries?limit=50      newest first, with counts by status
//	POST /api/deliveries/{id}/retry    send a dead delivery again
//
// A job never carries its target or message text: addresses and webhook URLs
// stay in configuration, and an error is redacted before it is stored.
func (s *Server) deliveriesHandler(w http.ResponseWriter, r *http.Request) {
	if s.deliveryStore == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "deliveries are not available"})
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, deliveriesPrefix), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		s.listDeliveries(w, r)
	case rest == "":
		util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
	default:
		id, action, _ := strings.Cut(rest, "/")
		if action != "retry" {
			util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: "not found"})
			return
		}
		if r.Method != http.MethodPost {
			util.WriteJSON(w, http.StatusMethodNotAllowed, util.ErrorResponse{Error: errMethod})
			return
		}
		s.retryDelivery(w, r, id)
	}
}

func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) {
	limit := defaultDeliveryLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxDeliveryLimit {
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "limit must be between 1 and " + strconv.Itoa(maxDeliveryLimit)})
			return
		}
		limit = n
	}
	jobs, err := s.deliveryStore.List(r.Context(), limit)
	if err == nil {
		var counts map[delivery.Status]int
		if counts, err = s.deliveryStore.Counts(r.Context()); err == nil {
			if jobs == nil {
				jobs = []delivery.Job{}
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"deliveries": jobs, "counts": counts})
			return
		}
	}
	s.logFor(r.Context()).Error("list deliveries failed", "error", delivery.RedactError(err))
	util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
}

func (s *Server) retryDelivery(w http.ResponseWriter, r *http.Request, id string) {
	err := s.deliveryStore.Requeue(r.Context(), id, time.Now())
	switch {
	case errors.Is(err, delivery.ErrNotFound):
		util.WriteJSON(w, http.StatusNotFound, util.ErrorResponse{Error: err.Error()})
	case errors.Is(err, delivery.ErrNotRetryable):
		util.WriteJSON(w, http.StatusConflict, util.ErrorResponse{Error: err.Error()})
	case err != nil:
		s.logFor(r.Context()).Error("retry delivery failed", "error", delivery.RedactError(err))
		util.WriteJSON(w, http.StatusInternalServerError, util.ErrorResponse{Error: errInternal})
	default:
		util.WriteJSON(w, http.StatusOK, map[string]string{"status": string(delivery.StatusPending)})
	}
}
