package httpapi

import (
	"net/http"
	"strconv"
)

// Webhook events that ran out of attempts (see webhook/outbox.go). They are kept for
// WEBHOOK_FAILED_RETENTION_HOURS so they can be sent again once the receiver is fixed.

const maxFailureList = 500

// webhookFailures lists an instance's failed events, newest first (?limit=, default 100).
func (s *Server) webhookFailures(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.manager.Get(id); err != nil {
		s.fail(w, err)
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxFailureList {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = n
	}
	list, err := s.store.Outbox().Failed(r.Context(), id, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"failures": list})
}

// webhookFailuresAct sends failed events again (retry = true) or deletes them. The body is
// optional: {"ids": [1, 2]} picks some; without it, every failed event of the instance.
func (s *Server) webhookFailuresAct(retry bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := s.manager.Get(id); err != nil {
			s.fail(w, err)
			return
		}
		var body struct {
			IDs []int64 `json:"ids"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return
		}
		if len(body.IDs) > maxFailureList {
			writeError(w, http.StatusBadRequest, "send at most 500 ids")
			return
		}
		var (
			n   int64
			err error
		)
		out := s.store.Outbox()
		if retry {
			n, err = out.RetryFailed(r.Context(), id, body.IDs)
		} else {
			n, err = out.DiscardFailed(r.Context(), id, body.IDs)
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		key := "discarded"
		if retry {
			key = "requeued"
		}
		writeJSON(w, http.StatusOK, map[string]any{key: n})
	}
}
