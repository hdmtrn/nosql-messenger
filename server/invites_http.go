package main

import (
	"errors"
	"log"
	"net/http"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// invitableChannel is channelForMember plus the rule that only a named channel
// has invites. Without it a participant of a direct conversation could mint a
// link into it and hand a third person the whole private history — membership
// alone was never the right question to ask here.
func (s *server) invitableChannel(w http.ResponseWriter, r *http.Request, sess Session) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed channel id")
		return bson.ObjectID{}, false
	}

	kind, err := s.channels.KindForMember(r.Context(), id, sess.UserID)
	if errors.Is(err, errNotMember) {
		writeError(w, http.StatusNotFound, "channel not found")
		return bson.ObjectID{}, false
	}
	if err != nil {
		log.Printf("reading channel kind: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return bson.ObjectID{}, false
	}
	if kind != channelKindNamed {
		writeError(w, http.StatusBadRequest, "a direct conversation has no invites")
		return bson.ObjectID{}, false
	}
	return id, true
}

func (s *server) handleCreateInvite(w http.ResponseWriter, r *http.Request, sess Session) {
	id, ok := s.invitableChannel(w, r, sess)
	if !ok {
		return
	}

	inv, err := s.invites.Create(r.Context(), id, sess.UserID)
	if errors.Is(err, errTooManyInvites) {
		writeError(w, http.StatusConflict, "revoke an invite before making another")
		return
	}
	if err != nil {
		log.Printf("creating invite: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, inv)
}

func (s *server) handleListInvites(w http.ResponseWriter, r *http.Request, sess Session) {
	id, ok := s.invitableChannel(w, r, sess)
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
	id, ok := s.invitableChannel(w, r, sess)
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

	// Following a link is a request for a state, not for a change: "put me in
	// that channel". Someone who is already in has that state, so answering with
	// an error would be wrong — and it would leave the client with no channel to
	// open, dropping the person into whichever conversation happened to be first.
	status := "joined"
	switch err := s.channels.AddMember(r.Context(), inv.ChannelID, sess); {
	case err == nil:
	case errors.Is(err, errAlreadyMember):
		status = "member"
	case errors.Is(err, errChannelNotFound), errors.Is(err, errNotJoinable):
		// errNotJoinable can only come from an invite made before direct
		// conversations were excluded. The code leads nowhere now, and that is
		// exactly what "not found" says.
		writeError(w, http.StatusNotFound, "invite not found")
		return
	default:
		log.Printf("joining by invite: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.bus.Subscribe(sess.UserID.Hex(), inv.ChannelID.Hex())
	writeJSON(w, http.StatusOK, map[string]string{
		"status":     status,
		"channel_id": inv.ChannelID.Hex(),
	})
}
