package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	displayNameMinLen = 1
	displayNameMaxLen = 64
	bioMaxLen         = 200
)

func (s *server) handleSearchUsers(w http.ResponseWriter, r *http.Request, _ Session) {
	users, err := s.users.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		log.Printf("searching users: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *server) handleGetUser(w http.ResponseWriter, r *http.Request, _ Session) {
	u, err := s.users.GetByUsername(r.Context(), r.PathValue("username"))
	if errors.Is(err, errUserNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		log.Printf("loading user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type profileRequest struct {
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
}

func (s *server) handleUpdateProfile(w http.ResponseWriter, r *http.Request, sess Session) {
	var req profileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	name := strings.TrimSpace(req.DisplayName)
	if n := utf8.RuneCountInString(name); n < displayNameMinLen || n > displayNameMaxLen {
		writeError(w, http.StatusBadRequest, "display name must be between 1 and 64 characters")
		return
	}

	bio := strings.TrimSpace(req.Bio)
	if utf8.RuneCountInString(bio) > bioMaxLen {
		writeError(w, http.StatusBadRequest, "bio must be at most 200 characters")
		return
	}

	if err := s.users.UpdateProfile(r.Context(), sess.UserID, name, bio); err != nil {
		log.Printf("updating profile: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"display_name": name, "bio": bio})
}

type sessionView struct {
	ID             string    `json:"id"`
	UserAgent      string    `json:"user_agent"`
	CreatedAt      time.Time `json:"created_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
	Current        bool      `json:"current"`
}

func (s *server) handleListSessions(w http.ResponseWriter, r *http.Request, sess Session) {
	live, err := s.sessions.ForUser(r.Context(), sess.UserID)
	if err != nil {
		log.Printf("listing sessions: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]sessionView, 0, len(live))
	for _, l := range live {
		out = append(out, sessionView{
			ID:             l.ID.Hex(),
			UserAgent:      l.UserAgent,
			CreatedAt:      l.CreatedAt,
			LastActivityAt: l.LastActivityAt,
			Current:        l.ID == sess.ID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleRevokeSession(w http.ResponseWriter, r *http.Request, sess Session) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed session id")
		return
	}

	err = s.sessions.Revoke(r.Context(), sess.UserID, id)
	if errors.Is(err, errSessionNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		log.Printf("revoking session: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *server) dropAvatar(ctx context.Context, id *bson.ObjectID) {
	if id == nil {
		return
	}
	if err := s.media.Delete(ctx, *id); err != nil {
		log.Printf("deleting avatar %s: %v", id.Hex(), err)
	}
}

func (s *server) handleSetAvatar(w http.ResponseWriter, r *http.Request, sess Session) {
	m, ok := s.saveUpload(w, r, sess.UserID, mediaKindAvatar, avatarMaxBytes)
	if !ok {
		return
	}

	prev, err := s.users.SetAvatar(r.Context(), sess.UserID, &m.ID)
	if err != nil {
		log.Printf("setting avatar: %v", err)
		s.dropAvatar(r.Context(), &m.ID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.dropAvatar(r.Context(), prev)

	writeJSON(w, http.StatusOK, map[string]string{"avatar_id": m.ID.Hex()})
}

func (s *server) handleDeleteAvatar(w http.ResponseWriter, r *http.Request, sess Session) {
	prev, err := s.users.SetAvatar(r.Context(), sess.UserID, nil)
	if err != nil {
		log.Printf("removing avatar: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.dropAvatar(r.Context(), prev)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
