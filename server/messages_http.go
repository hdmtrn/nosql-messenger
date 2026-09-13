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

type sendMessageRequest struct {
	ChannelID   string `json:"channel_id"`
	Text        string `json:"text"`
	ClientMsgID string `json:"client_msg_id,omitempty"`
}

func validateMessageText(s string) error {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return errors.New("message text must not be empty")
	}
	if n > messageMaxLen {
		return errors.New("message text is too long")
	}
	return nil
}

func (s *server) channelForMember(w http.ResponseWriter, r *http.Request, raw string, sess Session) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed channel id")
		return bson.ObjectID{}, false
	}

	member, err := s.channels.IsMember(r.Context(), id, sess.UserID)
	if err != nil {
		log.Printf("checking membership: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return bson.ObjectID{}, false
	}
	if !member {
		writeError(w, http.StatusNotFound, "channel not found")
		return bson.ObjectID{}, false
	}
	return id, true
}

func (s *server) handleSendMessage(w http.ResponseWriter, r *http.Request, sess Session) {
	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	channelID, ok := s.channelForMember(w, r, req.ChannelID, sess)
	if !ok {
		return
	}
	if err := validateMessageText(req.Text); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.deliverMessage(w, r, channelID, sess, req.Text, req.ClientMsgID, nil)
}

// deliverMessage is the one way a checked message gets into a channel: persisted
// first, broadcast only after the write succeeded, then answered to the sender.
// A repeated client_msg_id gets the stored message back and is not broadcast again.
func (s *server) deliverMessage(w http.ResponseWriter, r *http.Request, channelID bson.ObjectID,
	sess Session, text, clientMsgID string, fwd *ForwardedFrom) {
	msg, err := s.messages.Insert(r.Context(), channelID, sess, text, clientMsgID, fwd)
	if errors.Is(err, errDuplicateMessage) {
		existing, ferr := s.messages.ByClientMsgID(r.Context(), clientMsgID)
		if ferr != nil {
			log.Printf("resolving duplicate message: %v", ferr)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if err != nil {
		log.Printf("inserting message: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("encoding message for broadcast: %v", err)
	} else {
		s.hub.Publish(channelID.Hex(), payload)
	}

	writeJSON(w, http.StatusCreated, msg)
}

func (s *server) handleListMessages(w http.ResponseWriter, r *http.Request, sess Session) {
	q := r.URL.Query()

	channelID, ok := s.channelForMember(w, r, q.Get("channel_id"), sess)
	if !ok {
		return
	}

	var before bson.ObjectID
	if raw := q.Get("before"); raw != "" {
		id, err := bson.ObjectIDFromHex(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed before cursor")
			return
		}
		before = id
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

	messages, err := s.messages.List(r.Context(), channelID, before, limit)
	if err != nil {
		log.Printf("listing messages: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, messages)
}
