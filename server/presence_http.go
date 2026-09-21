package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Events tell a client what changes while it is connected; this tells it where
// things stand when it arrives. Asked for explicit ids, as Mattermost's
// /users/status/ids is: the client knows who it is about to display.
const presenceMaxIDs = 100

type presenceView struct {
	Online   bool       `json:"online"`
	Version  int64      `json:"version"`
	Epoch    int64      `json:"epoch"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

func (s *server) handlePresence(w http.ResponseWriter, r *http.Request, sess Session) {
	asked := make([]bson.ObjectID, 0, presenceMaxIDs)
	offline := make([]bson.ObjectID, 0, presenceMaxIDs)

	for _, raw := range strings.Split(r.URL.Query().Get("ids"), ",") {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		oid, err := bson.ObjectIDFromHex(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed user id")
			return
		}
		asked = append(asked, oid)
		if len(asked) > presenceMaxIDs {
			writeError(w, http.StatusBadRequest, "too many ids")
			return
		}
	}

	visible, err := s.visibleTo(r.Context(), sess, asked)
	if err != nil {
		log.Printf("presence: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	ids := make([]string, 0, len(asked))
	for _, oid := range asked {
		if visible[oid] {
			ids = append(ids, oid.Hex())
		}
	}

	states, err := s.presence.lookup(r.Context(), ids)
	if err != nil {
		log.Printf("presence: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make(map[string]presenceView, len(states))
	for id, state := range states {
		out[id] = presenceView{Online: state.Online, Version: state.Version, Epoch: state.Epoch}
		if !state.Online {
			oid, _ := bson.ObjectIDFromHex(id)
			offline = append(offline, oid)
		}
	}

	if len(offline) > 0 {
		seen, err := s.users.LastSeenByIDs(r.Context(), offline)
		if err != nil {
			log.Printf("presence: reading last seen: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		for id, at := range seen {
			view := out[id]
			view.LastSeen = &at
			out[id] = view
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// visibleTo narrows the people asked about to those the caller may see at all.
// The boundary is the one the project already draws for writing to a person: a
// channel in common, or friendship. Somebody outside it is left out of the answer
// rather than reported offline, which would still confirm the account exists.
func (s *server) visibleTo(ctx context.Context, sess Session, ids []bson.ObjectID) (map[bson.ObjectID]bool, error) {
	visible, err := s.channels.SharingAChannelWith(ctx, sess.UserID, ids)
	if err != nil {
		return nil, err
	}
	friends, err := s.friends.FriendsAmong(ctx, sess.UserID, ids)
	if err != nil {
		return nil, err
	}
	for id := range friends {
		visible[id] = true
	}
	return visible, nil
}
