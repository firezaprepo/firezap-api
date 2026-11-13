package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.mau.fi/whatsmeow"

	"github.com/firezaprepo/firezap-api/internal/instance"
)

// Group routes. {jid} accepts "120363…@g.us" or the bare id.

func (s *Server) groupCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 60*time.Second)
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.groupCtx(r)
	defer cancel()
	gs, err := s.manager.Groups(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": gs})
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string   `json:"name"`
		Participants []string `json:"participants"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx, cancel := s.groupCtx(r)
	defer cancel()
	g, err := s.manager.CreateGroup(ctx, r.PathValue("id"), body.Name, body.Participants)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.groupCtx(r)
	defer cancel()
	g, err := s.manager.GroupInfo(ctx, r.PathValue("id"), r.PathValue("jid"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Announce    *bool   `json:"announce"`
		Locked      *bool   `json:"locked"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx, cancel := s.groupCtx(r)
	defer cancel()
	err := s.manager.UpdateGroup(ctx, r.PathValue("id"), r.PathValue("jid"), instance.GroupUpdate{
		Name: body.Name, Description: body.Description, Announce: body.Announce, Locked: body.Locked,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (s *Server) groupParticipants(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action       string   `json:"action"`
		Participants []string `json:"participants"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx, cancel := s.groupCtx(r)
	defer cancel()
	res, err := s.manager.UpdateParticipants(ctx, r.PathValue("id"), r.PathValue("jid"), body.Action, body.Participants)
	if err != nil {
		s.fail(w, err)
		return
	}
	// WhatsApp answers per person: check "error" in each item (for example 403 when their
	// privacy settings do not allow being added — send them the invite link instead).
	writeJSON(w, http.StatusOK, map[string]any{"participants": res})
}

func (s *Server) groupInvite(reset bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := s.groupCtx(r)
		defer cancel()
		link, err := s.manager.InviteLink(ctx, r.PathValue("id"), r.PathValue("jid"), reset)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"link": link})
	}
}

func (s *Server) joinGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Link string `json:"link"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx, cancel := s.groupCtx(r)
	defer cancel()
	jid, err := s.manager.JoinGroup(ctx, r.PathValue("id"), body.Link)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"jid": jid})
}

func (s *Server) leaveGroup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.groupCtx(r)
	defer cancel()
	if err := s.manager.LeaveGroup(ctx, r.PathValue("id"), r.PathValue("jid")); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"left": true})
}

// whatsappStatus maps WhatsApp's answers to group requests to an HTTP status and message.
func whatsappStatus(err error) (int, string, bool) {
	switch {
	case errors.Is(err, whatsmeow.ErrGroupNotFound), errors.Is(err, whatsmeow.ErrNotInGroup):
		return http.StatusNotFound, "group not found, or this number is not in it", true
	case errors.Is(err, whatsmeow.ErrGroupInviteLinkUnauthorized):
		return http.StatusForbidden, "only group admins can get or reset the invite link", true
	case errors.Is(err, whatsmeow.ErrInviteLinkRevoked):
		return http.StatusGone, "this invite link was revoked", true
	case errors.Is(err, whatsmeow.ErrInviteLinkInvalid):
		return http.StatusBadRequest, "this invite link is not valid", true
	}
	var iq *whatsmeow.IQError
	if errors.As(err, &iq) {
		switch iq.Code {
		case 401, 403:
			return http.StatusForbidden, "WhatsApp refused: this number is not allowed to do this (admin required?)", true
		case 404:
			return http.StatusNotFound, "WhatsApp did not find it (group or participant)", true
		case 400, 406:
			return http.StatusBadRequest, "WhatsApp refused the request as not acceptable", true
		case 409:
			return http.StatusConflict, "WhatsApp refused: conflict with the current state", true
		case 429:
			return http.StatusTooManyRequests, "WhatsApp is rate limiting this number: slow down", true
		}
	}
	return 0, "", false
}
