package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
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

	s.hub.Subscribe(sess.UserID.Hex(), ch.ID.Hex())
	writeJSON(w, http.StatusCreated, ch)
}

func (s *server) handleListChannels(w http.ResponseWriter, r *http.Request, sess Session) {
	channels, err := s.channels.ForUser(r.Context(), sess.UserID)
	if err != nil {
		log.Printf("listing channels: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (s *server) handleJoinChannel(w http.ResponseWriter, r *http.Request, sess Session) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed channel id")
		return
	}

	switch err := s.channels.AddMember(r.Context(), id, sess); {
	case err == nil:
	case errors.Is(err, errAlreadyMember):
		writeError(w, http.StatusConflict, "already a member of this channel")
		return
	case errors.Is(err, errChannelNotFound):
		writeError(w, http.StatusNotFound, "channel not found")
		return
	default:
		log.Printf("joining channel: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.hub.Subscribe(sess.UserID.Hex(), id.Hex())
	writeJSON(w, http.StatusOK, map[string]string{"status": "joined"})
}
