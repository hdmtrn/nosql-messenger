package main

import (
	"errors"
	"log"
	"net/http"
)

func (s *server) handleCreateInvite(w http.ResponseWriter, r *http.Request, sess Session) {
	id, ok := s.channelForMember(w, r, r.PathValue("id"), sess)
	if !ok {
		return
	}

	inv, err := s.invites.Create(r.Context(), id, sess.UserID)
	if err != nil {
		log.Printf("creating invite: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, inv)
}

func (s *server) handleListInvites(w http.ResponseWriter, r *http.Request, sess Session) {
	id, ok := s.channelForMember(w, r, r.PathValue("id"), sess)
	if !ok {
		return
	}

	list, err := s.invites.ForChannel(r.Context(), id)
	if err != nil {
		log.Printf("listing invites: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *server) handleRevokeInvite(w http.ResponseWriter, r *http.Request, sess Session) {
	id, ok := s.channelForMember(w, r, r.PathValue("id"), sess)
	if !ok {
		return
	}

	err := s.invites.Revoke(r.Context(), id, r.PathValue("code"))
	if errors.Is(err, errInviteNotFound) {
		writeError(w, http.StatusNotFound, "invite not found")
		return
	}
	if err != nil {
		log.Printf("revoking invite: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// handleFollowInvite joins whatever the code points at. An unknown code and a
// revoked one are the same answer, because both mean the code leads nowhere.
func (s *server) handleFollowInvite(w http.ResponseWriter, r *http.Request, sess Session) {
	inv, err := s.invites.ByCode(r.Context(), r.PathValue("code"))
	if errors.Is(err, errInviteNotFound) {
		writeError(w, http.StatusNotFound, "invite not found")
		return
	}
	if err != nil {
		log.Printf("looking up invite: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	switch err := s.channels.AddMember(r.Context(), inv.ChannelID, sess); {
	case err == nil:
	case errors.Is(err, errAlreadyMember):
		writeError(w, http.StatusConflict, "already a member of this channel")
		return
	case errors.Is(err, errChannelNotFound):
		writeError(w, http.StatusNotFound, "invite not found")
		return
	default:
		log.Printf("joining by invite: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.hub.Subscribe(sess.UserID.Hex(), inv.ChannelID.Hex())
	writeJSON(w, http.StatusOK, map[string]string{
		"status":     "joined",
		"channel_id": inv.ChannelID.Hex(),
	})
}
