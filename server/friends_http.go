package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type friendRequestBody struct {
	Username string `json:"username"`
}

// A friendship changed for these two: a request arrived, or one was answered.
// The event carries nothing — both lists are one small request away, and a
// payload would mean deriving "friend / incoming / sent" a second time in the
// client. Constant, so it is written out once instead of encoded per event.
var friendsPayload = []byte(`{"type":"friends"}`)

// announceFriends tells both parties, on every node and in every tab. Telling
// the one who acted is not redundant: their other tabs got no HTTP answer.
func (s *server) announceFriends(ids ...bson.ObjectID) {
	if s.bus == nil {
		return
	}
	for _, id := range ids {
		s.bus.Publish(userTopic(id.Hex()), friendsPayload)
	}
}

func (s *server) handleSendFriendRequest(w http.ResponseWriter, r *http.Request, sess Session) {
	var body friendRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	target, err := s.users.GetByUsername(r.Context(), body.Username)
	if errors.Is(err, errUserNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		log.Printf("looking up user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	req, err := s.friends.Send(r.Context(), sess, target)
	switch {
	case err == nil:
	case errors.Is(err, errFriendSelf):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, errFriendAlready), errors.Is(err, errFriendPending):
		writeError(w, http.StatusConflict, err.Error())
		return
	default:
		log.Printf("sending friend request: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.announceFriends(req.From.ID, req.To.ID)

	code := http.StatusCreated
	if req.Status == friendAccepted {
		code = http.StatusOK
	}
	writeJSON(w, code, req)
}

func (s *server) handleListFriendRequests(w http.ResponseWriter, r *http.Request, sess Session) {
	incoming, err := s.friends.Incoming(r.Context(), sess.UserID)
	if err == nil {
		var outgoing []FriendRequest
		outgoing, err = s.friends.Outgoing(r.Context(), sess.UserID)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"incoming": incoming,
				"outgoing": outgoing,
			})
			return
		}
	}
	log.Printf("listing friend requests: %v", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func (s *server) respond(w http.ResponseWriter, r *http.Request, sess Session, status string) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed request id")
		return
	}

	req, err := s.friends.Respond(r.Context(), id, sess, status)
	if errors.Is(err, errRequestNotFound) {
		writeError(w, http.StatusNotFound, "friend request not found")
		return
	}
	if err != nil {
		log.Printf("responding to friend request: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.announceFriends(req.From.ID, req.To.ID)
	writeJSON(w, http.StatusOK, req)
}

func (s *server) handleAcceptFriendRequest(w http.ResponseWriter, r *http.Request, sess Session) {
	s.respond(w, r, sess, friendAccepted)
}

func (s *server) handleDeclineFriendRequest(w http.ResponseWriter, r *http.Request, sess Session) {
	s.respond(w, r, sess, friendDeclined)
}

func (s *server) handleListFriends(w http.ResponseWriter, r *http.Request, sess Session) {
	friends, err := s.friends.Friends(r.Context(), sess.UserID)
	if err != nil {
		log.Printf("listing friends: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, friends)
}
