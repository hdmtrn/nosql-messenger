package main

import (
	"errors"
	"log"
	"net/http"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type inviteLink struct {
	Code string `json:"code"`
}

// handleGetInvite gives any member of a named channel its link. Without the
// kind check a participant of a direct conversation could hand a third person
// the whole private history; membership alone was never the right question.
func (s *server) handleGetInvite(w http.ResponseWriter, r *http.Request, sess Session) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed channel id")
		return
	}

	code, err := s.channels.InviteCode(r.Context(), id, sess.UserID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, inviteLink{Code: code})
	case errors.Is(err, errNotMember), errors.Is(err, errChannelNotFound):
		writeError(w, http.StatusNotFound, "channel not found")
	case errors.Is(err, errNotJoinable):
		writeError(w, http.StatusBadRequest, "a direct conversation has no invite link")
	default:
		log.Printf("reading invite code: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// handleResetInvite is the only way to take a link back: the old code stops
// leading anywhere, and the new one is the answer.
func (s *server) handleResetInvite(w http.ResponseWriter, r *http.Request, sess Session) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed channel id")
		return
	}

	code, err := s.channels.ResetInviteCode(r.Context(), id, sess.UserID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, inviteLink{Code: code})
	case errors.Is(err, errNotMember):
		writeError(w, http.StatusNotFound, "channel not found")
	case errors.Is(err, errNotOwner):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		log.Printf("resetting invite code: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// handleFollowInvite joins whatever the code points at. An unknown code and a
// reset one are the same answer, because both mean the code leads nowhere.
func (s *server) handleFollowInvite(w http.ResponseWriter, r *http.Request, sess Session) {
	channelID, err := s.channels.ByInviteCode(r.Context(), r.PathValue("code"))
	if errors.Is(err, errChannelNotFound) {
		writeError(w, http.StatusNotFound, "invite not found")
		return
	}
	if err != nil {
		log.Printf("looking up invite: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Following a link is a request for a state, not for a change: "put me in
	// that channel". Someone who is already in has that state, so answering with
	// an error would be wrong — and it would leave the client with no channel to
	// open, dropping the person into whichever conversation happened to be first.
	status := "joined"
	switch err := s.channels.AddMember(r.Context(), channelID, sess); {
	case err == nil:
	case errors.Is(err, errAlreadyMember):
		status = "member"
	case errors.Is(err, errChannelNotFound):
		// The last member left and the channel waits for the purge.
		writeError(w, http.StatusNotFound, "invite not found")
		return
	default:
		log.Printf("joining by invite: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.bus.Subscribe(sess.UserID.Hex(), channelID.Hex())
	writeJSON(w, http.StatusOK, map[string]string{
		"status":     status,
		"channel_id": channelID.Hex(),
	})
}
