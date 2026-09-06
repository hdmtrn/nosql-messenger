package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	displayNameMinLen = 1
	displayNameMaxLen = 64
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

type displayNameRequest struct {
	DisplayName string `json:"display_name"`
}

func (s *server) handleSetDisplayName(w http.ResponseWriter, r *http.Request, sess Session) {
	var req displayNameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	name := strings.TrimSpace(req.DisplayName)
	if n := utf8.RuneCountInString(name); n < displayNameMinLen || n > displayNameMaxLen {
		writeError(w, http.StatusBadRequest, "display name must be between 1 and 64 characters")
		return
	}

	if err := s.users.SetDisplayName(r.Context(), sess.UserID, name); err != nil {
		log.Printf("setting display name: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"display_name": name})
}
