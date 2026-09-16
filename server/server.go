package main

import (
	"net/http"
	"os"
	"path/filepath"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

const webRoot = "web/dist"

// serveWeb hands back the built page for any path that is not a file, because
// an invite link is a client-side route: /invite/CODE exists in the app, not
// on disk, and a file server would answer it with 404.
func serveWeb(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(webRoot, filepath.Clean(r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, filepath.Join(webRoot, "index.html"))
}

type server struct {
	mongo    *mongo.Client
	hub      *Hub
	auth     *auth
	sessions *sessionStore
	users    *userStore
	channels *channelStore
	messages *messageStore
	friends  *friendStore
	invites  *inviteStore
	media    *mediaStore
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth)

	mux.HandleFunc("POST /auth/register", s.auth.handleRegister)
	mux.HandleFunc("POST /auth/login", s.auth.handleLogin)
	mux.HandleFunc("POST /auth/logout", s.auth.handleLogout)
	mux.HandleFunc("GET /auth/me", s.auth.handleMe)
	mux.HandleFunc("POST /auth/me/profile", s.requireAuth(s.handleUpdateProfile))
	mux.HandleFunc("POST /auth/me/avatar", s.requireAuth(s.handleSetAvatar))
	mux.HandleFunc("DELETE /auth/me/avatar", s.requireAuth(s.handleDeleteAvatar))
	mux.HandleFunc("GET /auth/sessions", s.requireAuth(s.handleListSessions))
	mux.HandleFunc("DELETE /auth/sessions/{id}", s.requireAuth(s.handleRevokeSession))

	mux.HandleFunc("GET /users", s.requireAuth(s.handleSearchUsers))
	mux.HandleFunc("GET /users/{username}", s.requireAuth(s.handleGetUser))
	mux.HandleFunc("GET /users/{username}/channels", s.requireAuth(s.handleChannelsInCommon))

	mux.HandleFunc("POST /channels", s.requireAuth(s.handleCreateChannel))
	mux.HandleFunc("GET /channels", s.requireAuth(s.handleListChannels))
	mux.HandleFunc("POST /channels/{id}/invites", s.requireAuth(s.handleCreateInvite))
	mux.HandleFunc("GET /channels/{id}/invites", s.requireAuth(s.handleListInvites))
	mux.HandleFunc("DELETE /channels/{id}/invites/{code}", s.requireAuth(s.handleRevokeInvite))
	mux.HandleFunc("POST /invites/{code}", s.requireAuth(s.handleFollowInvite))
	mux.HandleFunc("POST /channels/direct", s.requireAuth(s.handleOpenDirect))
	mux.HandleFunc("GET /channels/{id}", s.requireAuth(s.handleGetChannel))
	mux.HandleFunc("POST /channels/{id}/leave", s.requireAuth(s.handleLeaveChannel))

	mux.HandleFunc("POST /messages", s.requireAuth(s.handleSendMessage))
	mux.HandleFunc("GET /messages", s.requireAuth(s.handleListMessages))
	mux.HandleFunc("POST /messages/{id}/forward", s.requireAuth(s.handleForwardMessage))

	mux.HandleFunc("POST /friends/requests", s.requireAuth(s.handleSendFriendRequest))
	mux.HandleFunc("GET /friends/requests", s.requireAuth(s.handleListFriendRequests))
	mux.HandleFunc("POST /friends/requests/{id}/accept", s.requireAuth(s.handleAcceptFriendRequest))
	mux.HandleFunc("POST /friends/requests/{id}/decline", s.requireAuth(s.handleDeclineFriendRequest))
	mux.HandleFunc("GET /friends", s.requireAuth(s.handleListFriends))

	mux.HandleFunc("POST /media", s.requireAuth(s.handleUploadMedia))
	mux.HandleFunc("GET /media/{id}", s.requireAuth(s.handleGetMedia))

	mux.HandleFunc("GET /ws", s.requireAuth(s.handleWS))

	mux.HandleFunc("GET /", serveWeb)

	return withLogging(mux)
}
