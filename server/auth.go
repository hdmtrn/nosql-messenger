package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	usernameMinLen = 3
	usernameMaxLen = 32
	passwordMinLen = 8
	passwordMaxLen = 128

	hashConcurrency = 6
	hashWaitTimeout = 2 * time.Second

	sessionCookie = "session"
)

var secureCookies = os.Getenv("COOKIE_SECURE") != "false"

func tokenFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return h[7:]
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionLength.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

type auth struct {
	users    *userStore
	sessions *sessionStore
	params   argonParams

	sem chan struct{}

	dummyHash string
}

func newAuth(users *userStore, sessions *sessionStore) (*auth, error) {
	dummy, err := hashPassword("placeholder-for-timing-equalization", defaultArgonParams)
	if err != nil {
		return nil, err
	}
	return &auth{
		users:     users,
		sessions:  sessions,
		params:    defaultArgonParams,
		sem:       make(chan struct{}, hashConcurrency),
		dummyHash: dummy,
	}, nil
}

func (a *auth) acquire() (release func(), ok bool) {
	select {
	case a.sem <- struct{}{}:
		return func() { <-a.sem }, true
	case <-time.After(hashWaitTimeout):
		return nil, false
	}
}

type authRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string `json:"token"`
	User  struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
}

func validateUsername(s string) error {
	n := utf8.RuneCountInString(s)
	if n < usernameMinLen || n > usernameMaxLen {
		return errors.New("username must be between 3 and 32 characters")
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return errors.New("username may contain only latin letters, digits, underscore and hyphen")
		}
	}
	return nil
}

func validatePassword(s string) error {
	if utf8.RuneCountInString(s) < passwordMinLen {
		return errors.New("password must be at least 8 characters long")
	}

	if len(s) > passwordMaxLen {
		return errors.New("password is too long")
	}
	return nil
}

func (a *auth) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}
	if err := validateUsername(req.Username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	release, ok := a.acquire()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "server is overloaded, try again later")
		return
	}
	hash, err := hashPassword(req.Password, a.params)
	release()
	if err != nil {
		log.Printf("hashing password: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	u := &User{
		Username:     normaliseUsername(req.Username),
		DisplayName:  strings.TrimSpace(req.Username),
		PasswordHash: hash,
		CreatedAt:    time.Now(),
	}

	if err := a.users.Create(r.Context(), u); err != nil {
		if errors.Is(err, errUsernameTaken) {
			writeError(w, http.StatusConflict, "username already taken")
			return
		}
		log.Printf("creating user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	a.respondWithToken(w, r, http.StatusCreated, u)
}

func (a *auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	u, err := a.users.GetByUsername(r.Context(), req.Username)
	if err != nil && !errors.Is(err, errUserNotFound) {
		log.Printf("looking up user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	release, ok := a.acquire()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "server is overloaded, try again later")
		return
	}

	stored := a.dummyHash
	if u != nil {
		stored = u.PasswordHash
	}
	match, verr := verifyPassword(req.Password, stored)
	release()

	if verr != nil {
		log.Printf("verifying password: %v", verr)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if u == nil || !match {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	a.respondWithToken(w, r, http.StatusOK, u)
}

func (a *auth) respondWithToken(w http.ResponseWriter, r *http.Request, code int, u *User) {
	sess, err := a.sessions.Create(r.Context(), u, r.UserAgent())
	if err != nil {
		log.Printf("creating session: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	setSessionCookie(w, sess.Token)

	var resp authResponse
	resp.Token = sess.Token
	resp.User.ID = u.ID.Hex()
	resp.User.Username = u.Username
	writeJSON(w, code, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writing response: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (a *auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := tokenFromRequest(r); token != "" {
		if err := a.sessions.Delete(r.Context(), token); err != nil {
			log.Printf("deleting session: %v", err)
		}
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type meResponse struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Bio         string    `json:"bio"`
	AvatarID    string    `json:"avatar_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// handleMe reads the user rather than the session: the display name changes, and
// a copy in the session would keep showing the old one until the session expired.
func (a *auth) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, err := a.sessions.ByToken(r.Context(), tokenFromRequest(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	u, err := a.users.GetByUsername(r.Context(), sess.Username)
	if err != nil {
		log.Printf("loading user: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	me := meResponse{
		ID:          u.ID.Hex(),
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Bio:         u.Bio,
		CreatedAt:   u.CreatedAt,
	}
	if u.AvatarID != nil {
		me.AvatarID = u.AvatarID.Hex()
	}
	writeJSON(w, http.StatusOK, me)
}
