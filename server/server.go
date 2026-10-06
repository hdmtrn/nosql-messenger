package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/redis/go-redis/v9"
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
	// What readyz touches: the one document it rewrites, and Redis to ping.
	health *mongo.Collection
	redis  *redis.Client

	// hub delivers to the sockets of this process, bus to every instance.
	// Handlers broadcast and route sockets through bus, never through hub.
	hub      *Hub
	bus      publisher
	presence *presence
	auth     *auth
	sessions *sessionStore
	users    *userStore
	channels *channelStore
	messages *messageStore
	friends  *friendStore
	media    *mediaStore
	reads    *readStore
	limits   *limiter
	operator operator

	// The socket handlers still running. Shutdown waits for them before it
	// closes Redis and MongoDB, which they use until they return.
	sockets sync.WaitGroup

	// Runs in an erasure between the first rename and the name going, for
	// tests that need a send to land exactly there. Nil outside tests.
	beforeFinishErasure func()
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /privacy", s.handleLegal("privacy"))
	mux.HandleFunc("GET /terms", s.handleLegal("terms"))

	mux.HandleFunc("POST /auth/register", s.auth.handleRegister)
	mux.HandleFunc("POST /auth/login", s.auth.handleLogin)
	mux.HandleFunc("POST /auth/logout", s.auth.handleLogout)
	mux.HandleFunc("GET /auth/me", s.auth.handleMe)
	mux.HandleFunc("DELETE /auth/me", s.requireAuth(s.handleDeleteAccount))
	mux.HandleFunc("POST /auth/me/profile", s.requireAuth(s.handleUpdateProfile))
	mux.HandleFunc("POST /auth/me/avatar", s.requireAuth(s.handleSetAvatar))
	mux.HandleFunc("DELETE /auth/me/avatar", s.requireAuth(s.handleDeleteAvatar))
	mux.HandleFunc("GET /auth/sessions", s.requireAuth(s.handleListSessions))
	mux.HandleFunc("DELETE /auth/sessions/{id}", s.requireAuth(s.handleRevokeSession))

	mux.HandleFunc("GET /users", s.requireAuth(s.handleSearchUsers))
	mux.HandleFunc("GET /users/{username}", s.requireAuth(s.handleGetUser))
	mux.HandleFunc("GET /users/{username}/channels", s.requireAuth(s.handleChannelsInCommon))
	mux.HandleFunc("GET /presence", s.requireAuth(s.handlePresence))

	mux.HandleFunc("POST /channels", s.requireAuth(s.handleCreateChannel))
	mux.HandleFunc("GET /channels", s.requireAuth(s.handleListChannels))
	mux.HandleFunc("GET /channels/{id}/invite", s.requireAuth(s.handleGetInvite))
	mux.HandleFunc("POST /channels/{id}/invite/reset", s.requireAuth(s.handleResetInvite))
	mux.HandleFunc("POST /invites/{code}", s.requireAuth(s.handleFollowInvite))
	mux.HandleFunc("POST /channels/direct", s.requireAuth(s.handleOpenDirect))
	mux.HandleFunc("GET /channels/{id}", s.requireAuth(s.handleGetChannel))
	mux.HandleFunc("POST /channels/{id}/leave", s.requireAuth(s.handleLeaveChannel))
	mux.HandleFunc("POST /channels/{id}/read", s.requireAuth(s.handleMarkRead))
	mux.HandleFunc("POST /channels/{id}/avatar", s.requireAuth(s.handleSetChannelAvatar))
	mux.HandleFunc("DELETE /channels/{id}/avatar", s.requireAuth(s.handleDeleteChannelAvatar))

	mux.HandleFunc("POST /messages", s.requireAuth(s.handleSendMessage))
	mux.HandleFunc("GET /messages", s.requireAuth(s.handleListMessages))
	mux.HandleFunc("POST /messages/{id}/forward", s.requireAuth(s.handleForwardMessage))

	mux.HandleFunc("POST /friends/requests", s.requireAuth(s.handleSendFriendRequest))
	mux.HandleFunc("GET /friends/requests", s.requireAuth(s.handleListFriendRequests))
	mux.HandleFunc("POST /friends/requests/{id}/accept", s.requireAuth(s.handleAcceptFriendRequest))
	mux.HandleFunc("POST /friends/requests/{id}/decline", s.requireAuth(s.handleDeclineFriendRequest))
	mux.HandleFunc("GET /friends", s.requireAuth(s.handleListFriends))
	mux.HandleFunc("DELETE /friends/{id}", s.requireAuth(s.handleRemoveFriend))

	mux.HandleFunc("POST /media", s.requireAuth(s.handleUploadMedia))
	mux.HandleFunc("GET /media/{id}", s.requireAuth(s.handleGetMedia))

	// Not behind requireAuth: the socket checks its session after the upgrade,
	// so that a refusal reaches the client as a close code (see handleWS).
	mux.HandleFunc("GET /ws", s.handleWS)

	mux.HandleFunc("GET /", serveWeb)

	return withLogging(mux)
}
