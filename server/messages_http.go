package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type sendMessageRequest struct {
	ChannelID   string   `json:"channel_id"`
	Text        string   `json:"text"`
	ClientMsgID string   `json:"client_msg_id,omitempty"`
	ReplyTo     string   `json:"reply_to,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
}

// validateMessageText lets the text be empty only under a picture.
func validateMessageText(s string, attachments int) error {
	n := utf8.RuneCountInString(s)
	if n == 0 && attachments == 0 {
		return errors.New("message text must not be empty")
	}
	if n > messageMaxLen {
		return errors.New("message text is too long")
	}
	return nil
}

func parseMediaIDs(raw []string) ([]bson.ObjectID, error) {
	if len(raw) > messageMaxAttachments {
		return nil, fmt.Errorf("at most %d attachments per message", messageMaxAttachments)
	}
	ids := make([]bson.ObjectID, 0, len(raw))
	seen := make(map[bson.ObjectID]bool, len(raw))
	for _, r := range raw {
		id, err := bson.ObjectIDFromHex(r)
		if err != nil {
			return nil, errors.New("malformed attachment id")
		}
		if seen[id] {
			return nil, errors.New("an attachment is listed twice")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
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

// messageForMember is channelForMember for a message: the channel to check comes
// from the stored document, never from the request, so pairing a foreign message
// id with a channel of one's own does not get through.
func (s *server) messageForMember(w http.ResponseWriter, r *http.Request, raw string, sess Session) (Message, bool) {
	id, err := bson.ObjectIDFromHex(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed message id")
		return Message{}, false
	}

	msg, err := s.messages.ByID(r.Context(), id)
	if errors.Is(err, errMessageNotFound) {
		writeError(w, http.StatusNotFound, "message not found")
		return Message{}, false
	}
	if err != nil {
		log.Printf("loading message: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return Message{}, false
	}

	// Not being in the message's channel answers like a missing message, so ids
	// of other people's messages cannot be probed.
	member, err := s.channels.IsMember(r.Context(), msg.ChannelID, sess.UserID)
	if err != nil {
		log.Printf("checking membership: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return Message{}, false
	}
	if !member {
		writeError(w, http.StatusNotFound, "message not found")
		return Message{}, false
	}
	return msg, true
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
	mediaIDs, err := parseMediaIDs(req.Attachments)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateMessageText(req.Text, len(mediaIDs)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var replyTo *bson.ObjectID
	if req.ReplyTo != "" {
		orig, ok := s.messageForMember(w, r, req.ReplyTo, sess)
		if !ok {
			return
		}
		// A reply stays in its channel. Pointing at a message from another channel
		// would have clients show its text to people who cannot read that channel;
		// it answers like a missing message, as a foreign one does.
		if orig.ChannelID != channelID {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		replyTo = &orig.ID
	}

	// Attaching goes last, once nothing else can refuse the message: a file bound
	// to a channel without a message is harmless, a message pointing at files no
	// one may open is not.
	var attachments []Attachment
	if len(mediaIDs) > 0 {
		attachments, err = s.media.Attach(r.Context(), mediaIDs, sess.UserID, channelID)
		if errors.Is(err, errMediaNotFound) {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		if err != nil {
			log.Printf("attaching media: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	s.deliverMessage(w, r, Message{
		ChannelID:   channelID,
		Author:      authorOf(sess),
		Text:        req.Text,
		ClientMsgID: req.ClientMsgID,
		ReplyTo:     replyTo,
		Attachments: attachments,
	})
}

type forwardMessageRequest struct {
	ChannelID   string `json:"channel_id"`
	ClientMsgID string `json:"client_msg_id,omitempty"`
}

// handleForwardMessage copies a message the caller can read into a channel the
// caller is in. The text comes from the stored message, not from the request.
func (s *server) handleForwardMessage(w http.ResponseWriter, r *http.Request, sess Session) {
	var req forwardMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	orig, ok := s.messageForMember(w, r, r.PathValue("id"), sess)
	if !ok {
		return
	}
	channelID, ok := s.channelForMember(w, r, req.ChannelID, sess)
	if !ok {
		return
	}

	// A repeat is answered before copying, so a retried forward leaves no second
	// set of media records behind.
	if req.ClientMsgID != "" {
		existing, err := s.messages.ByClientMsgID(r.Context(), req.ClientMsgID)
		if err == nil {
			writeJSON(w, http.StatusOK, existing)
			return
		}
		if !errors.Is(err, errMessageNotFound) {
			log.Printf("looking up message by client id: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	var attachments []Attachment
	if len(orig.Attachments) > 0 {
		copied, err := s.media.CopyTo(r.Context(), orig.Attachments, sess.UserID, channelID)
		if err != nil {
			log.Printf("copying media: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		attachments = copied
	}

	s.deliverMessage(w, r, Message{
		ChannelID:   channelID,
		Author:      authorOf(sess),
		Text:        orig.Text,
		ClientMsgID: req.ClientMsgID,
		Forwarded:   orig.forwardOf(),
		Attachments: attachments,
	})
}

// deliverMessage is the one way a checked message gets into a channel: persisted
// first, broadcast only after the write succeeded, then answered to the sender.
// A repeated client_msg_id gets the stored message back and is not broadcast again.
func (s *server) deliverMessage(w http.ResponseWriter, r *http.Request, msg Message) {
	stored, err := s.messages.Insert(r.Context(), msg)
	if errors.Is(err, errDuplicateMessage) {
		existing, ferr := s.messages.ByClientMsgID(r.Context(), msg.ClientMsgID)
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

	payload, err := json.Marshal(stored)
	if err != nil {
		log.Printf("encoding message for broadcast: %v", err)
	} else {
		s.hub.Publish(stored.ChannelID.Hex(), payload)
	}

	writeJSON(w, http.StatusCreated, stored)
}

func (s *server) handleListMessages(w http.ResponseWriter, r *http.Request, sess Session) {
	q := r.URL.Query()

	channelID, ok := s.channelForMember(w, r, q.Get("channel_id"), sess)
	if !ok {
		return
	}

	// ids asks for particular messages (the originals replies point at) rather
	// than a page, so the page parameters make no sense next to it.
	if raw := q.Get("ids"); raw != "" {
		if q.Has("before") || q.Has("limit") {
			writeError(w, http.StatusBadRequest, "ids cannot be combined with before or limit")
			return
		}
		s.listMessagesByID(w, r, channelID, raw)
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

// listMessagesByID answers with the listed messages of the channel. An id that is
// not there is simply left out; the client shows its quote as unavailable.
func (s *server) listMessagesByID(w http.ResponseWriter, r *http.Request, channelID bson.ObjectID, raw string) {
	// SplitN stops one piece past the limit, so a query with a million commas is
	// refused without first allocating a million strings.
	parts := strings.SplitN(raw, ",", messagesMaxLimit+1)
	if len(parts) > messagesMaxLimit {
		writeError(w, http.StatusBadRequest, "too many ids")
		return
	}
	ids := make([]bson.ObjectID, 0, len(parts))
	for _, p := range parts {
		id, err := bson.ObjectIDFromHex(p)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed message id")
			return
		}
		ids = append(ids, id)
	}

	messages, err := s.messages.ByIDs(r.Context(), channelID, ids)
	if err != nil {
		log.Printf("looking up messages: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, messages)
}
