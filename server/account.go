package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	// How long one node holds an erasure before another may take it over.
	eraseLease = 5 * time.Minute
	// The erasure outlives the request that starts it: a closed tab must not
	// stop it halfway.
	eraseTimeout = 2 * time.Minute
)

var errErasureLost = errors.New("another node took the erasure over")

type deleteAccountRequest struct {
	Password string `json:"password"`
}

// handleDeleteAccount deletes the caller's account once they confirm it with
// their password, as Rocket.Chat asks: a session alone is not enough to throw an
// account away. Messages in shared chats stay, under the tomb name, because
// replies quote them by id and removing them would break other people's
// conversations.
func (s *server) handleDeleteAccount(w http.ResponseWriter, r *http.Request, sess Session) {
	var req deleteAccountRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if s.auth.checkPassword(w, r, sess.Username, req.Password) == nil {
		return
	}

	owner := s.presence.nodeID
	marked, err := s.users.markDeleted(r.Context(), sess.UserID, owner, time.Now())
	if err != nil {
		log.Printf("deleting account %s: %v", sess.UserID.Hex(), err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Not marked: a request from another tab deleted it a moment ago and holds
	// the erasure. The account is gone either way.
	if marked {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), eraseTimeout)
		defer cancel()
		if err := s.eraseAccount(ctx, sess.UserID, owner); err != nil {
			// The mark stands, so the account is deleted all the same; the
			// purge loop finishes the rest once the claim runs out.
			log.Printf("erasing account %s: %v", sess.UserID.Hex(), err)
		}
	}

	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// eraseAccount does everything after the mark. Every step can run again, so a
// run cut short, here or in the purge loop, is finished by the next one.
func (s *server) eraseAccount(ctx context.Context, id bson.ObjectID, owner string) error {
	u, err := s.users.erasing(ctx, id)
	if err != nil {
		return err
	}

	// Every device signs out: Revoke closes the sockets here and, through the
	// bus, on the other nodes.
	sessions, err := s.sessions.ForUser(ctx, id)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if err := s.sessions.Revoke(ctx, id, sess.ID); err != nil && !errors.Is(err, errSessionNotFound) {
			return err
		}
	}

	channels, err := s.channels.memberOf(ctx, id)
	if err != nil {
		return err
	}
	// Before leaving: while the account is still a member, the event reaches
	// everyone who has its messages on screen.
	s.announceDeleted(id, u.Username, channels)
	// Leave does what it does for anyone: the next member becomes the owner,
	// an emptied channel goes to the purge, and a direct conversation stays
	// with the other person.
	for _, ch := range channels {
		if err := s.channels.Leave(ctx, ch, id); err != nil && !errors.Is(err, errNotMember) {
			return err
		}
	}

	if err := s.friends.deleteAllOf(ctx, id); err != nil {
		return err
	}
	if err := s.reads.deleteFor(ctx, bson.M{"user_id": id}); err != nil {
		return err
	}
	if u.AvatarID != nil {
		if err := s.media.Delete(ctx, *u.AvatarID); err != nil && !errors.Is(err, errMediaNotFound) {
			return err
		}
	}

	tomb := tombName(id)
	if err := s.messages.renameAuthor(ctx, id, tomb); err != nil {
		return err
	}
	if s.beforeFinishErasure != nil {
		s.beforeFinishErasure()
	}
	held, err := s.users.finishErasure(ctx, id, owner)
	if err != nil {
		return err
	}
	if !held {
		return errErasureLost
	}
	// Once more: a send that got past its checks before the sessions went may
	// have landed under the old name after the first pass.
	return s.messages.renameAuthor(ctx, id, tomb)
}

// announceDeleted is announceProfile for an account that is gone: whoever has
// its messages open shows "Deleted account" at once rather than after a reload.
func (s *server) announceDeleted(id bson.ObjectID, username string, channels []bson.ObjectID) {
	if s.bus == nil {
		return
	}
	payload, err := json.Marshal(profileEvent{
		Type: "profile",
		User: profileView{ID: id.Hex(), Username: username, Deleted: true},
	})
	if err != nil {
		log.Printf("erasure: encoding the event for %s: %v", username, err)
		return
	}
	for _, ch := range channels {
		s.bus.Publish(ch.Hex(), payload)
	}
}

// finishErasures takes over the erasures a node left unfinished, one claim at a
// time. One that fails stays claimed until the claim runs out, so the loop
// moves on, and a later round retries it.
func (s *server) finishErasures(ctx context.Context, owner string) error {
	for {
		id, ok, err := s.users.claimErasure(ctx, owner, time.Now())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := s.eraseAccount(ctx, id, owner); err != nil {
			log.Printf("erasure: account %s: %v", id.Hex(), err)
		}
	}
}
