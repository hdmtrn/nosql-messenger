package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type createChannelRequest struct {
	Name string `json:"name"`
}

func validateChannelName(s string) error {
	n := utf8.RuneCountInString(s)
	if n < channelNameMinLen || n > channelNameMaxLen {
		return errors.New("channel name must be between 1 and 64 characters")
	}
	return nil
}

func (s *server) handleCreateChannel(w http.ResponseWriter, r *http.Request, sess Session) {
	var req createChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}
	if err := validateChannelName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ch, err := s.channels.Create(r.Context(), req.Name, sess)
	if err != nil {
		log.Printf("creating channel: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// A channel nobody can be invited to is not much of a channel, so it opens
	// with one invite already made.
	if _, err := s.invites.Create(r.Context(), ch.ID, sess.UserID); err != nil {
		log.Printf("creating first invite: %v", err)
	}

	s.hub.Subscribe(sess.UserID.Hex(), ch.ID.Hex())
	writeJSON(w, http.StatusCreated, ch)
}

func (s *server) handleListChannels(w http.ResponseWriter, r *http.Request, sess Session) {
	q := r.URL.Query()

	var after bson.ObjectID
	if raw := q.Get("after"); raw != "" {
		id, err := bson.ObjectIDFromHex(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed after cursor")
			return
		}
		after = id
	}

	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = n
	}

	channels, err := s.channels.ForUser(r.Context(), sess.UserID, after, limit)
	if err != nil {
		log.Printf("listing channels: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (s *server) handleGetChannel(w http.ResponseWriter, r *http.Request, sess Session) {
	id, ok := s.channelForMember(w, r, r.PathValue("id"), sess)
	if !ok {
		return
	}

	ch, err := s.channels.ByID(r.Context(), id)
	if err != nil {
		log.Printf("loading channel: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (s *server) handleChannelsInCommon(w http.ResponseWriter, r *http.Request, sess Session) {
	other, err := s.users.GetByUsername(r.Context(), r.PathValue("username"))
	if errors.Is(err, errUserNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		log.Printf("looking up user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	common, err := s.channels.InCommon(r.Context(), sess.UserID, other.ID)
	if err != nil {
		log.Printf("listing channels in common: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, common)
}

type directChannelRequest struct {
	Username string `json:"username"`
}

func (s *server) handleOpenDirect(w http.ResponseWriter, r *http.Request, sess Session) {
	var req directChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	other, err := s.users.GetByUsername(r.Context(), req.Username)
	if errors.Is(err, errUserNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		log.Printf("looking up user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if other.ID == sess.UserID {
		writeError(w, http.StatusBadRequest, "cannot open a conversation with yourself")
		return
	}

	friends, err := s.friends.AreFriends(r.Context(), sess.UserID, other.ID)
	if err != nil {
		log.Printf("checking friendship: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !friends {
		writeError(w, http.StatusForbidden, "you can only message friends")
		return
	}

	ch, err := s.channels.Direct(r.Context(), sess, other)
	if err != nil {
		log.Printf("opening direct channel: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Both sides may already hold a socket, and neither reconnects for this.
	s.hub.Subscribe(sess.UserID.Hex(), ch.ID.Hex())
	s.hub.Subscribe(other.ID.Hex(), ch.ID.Hex())

	writeJSON(w, http.StatusOK, ch)
}

func (s *server) handleLeaveChannel(w http.ResponseWriter, r *http.Request, sess Session) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed channel id")
		return
	}

	err = s.channels.Leave(r.Context(), s.messages, s.invites, s.media, id, sess.UserID)
	if errors.Is(err, errNotMember) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		log.Printf("leaving channel: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.hub.Unsubscribe(sess.UserID.Hex(), id.Hex())
	writeJSON(w, http.StatusOK, map[string]string{"status": "left"})
}

func writeOwnerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotMember):
		writeError(w, http.StatusNotFound, "channel not found")
	case errors.Is(err, errNotOwner):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		log.Printf("changing channel avatar: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *server) handleSetChannelAvatar(w http.ResponseWriter, r *http.Request, sess Session) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed channel id")
		return
	}

	// Refused before the upload rather than after it, so a non-owner does not
	// push a megabyte into storage only to have it deleted. The update below
	// checks ownership again, and that check is the one that counts.
	ch, err := s.channels.ByID(r.Context(), id)
	if errors.Is(err, errChannelNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		writeOwnerError(w, err)
		return
	}
	switch memberRole(ch, sess.UserID) {
	case roleOwner:
	case "":
		writeOwnerError(w, errNotMember)
		return
	default:
		writeOwnerError(w, errNotOwner)
		return
	}

	m, ok := s.saveUpload(w, r, sess.UserID, mediaKindAvatar, &id, avatarMaxBytes)
	if !ok {
		return
	}

	prev, err := s.channels.SetAvatar(r.Context(), id, sess.UserID, &m.ID)
	if err != nil {
		s.dropAvatar(r.Context(), &m.ID)
		writeOwnerError(w, err)
		return
	}
	s.dropAvatar(r.Context(), prev)

	writeJSON(w, http.StatusOK, map[string]string{"avatar_id": m.ID.Hex()})
}

func (s *server) handleDeleteChannelAvatar(w http.ResponseWriter, r *http.Request, sess Session) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed channel id")
		return
	}

	prev, err := s.channels.SetAvatar(r.Context(), id, sess.UserID, nil)
	if err != nil {
		writeOwnerError(w, err)
		return
	}
	s.dropAvatar(r.Context(), prev)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// memberRole is empty for someone who is not in the channel.
func memberRole(ch Channel, userID bson.ObjectID) string {
	for _, m := range ch.Members {
		if m.UserID == userID {
			return m.Role
		}
	}
	return ""
}
