package main

import (
	"net/http"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

const webRoot = "web/dist"

type server struct {
	mongo    *mongo.Client
	hub      *Hub
	auth     *auth
	sessions *sessionStore
	users    *userStore
	channels *channelStore
	messages *messageStore
	friends  *friendStore
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth)

	mux.HandleFunc("POST /auth/register", s.auth.handleRegister)
	mux.HandleFunc("POST /auth/login", s.auth.handleLogin)
	mux.HandleFunc("POST /auth/logout", s.auth.handleLogout)
	mux.HandleFunc("GET /auth/me", s.auth.handleMe)
	mux.HandleFunc("POST /auth/me/profile", s.requireAuth(s.handleUpdateProfile))
	mux.HandleFunc("GET /auth/sessions", s.requireAuth(s.handleListSessions))
	mux.HandleFunc("DELETE /auth/sessions/{id}", s.requireAuth(s.handleRevokeSession))

	mux.HandleFunc("GET /users", s.requireAuth(s.handleSearchUsers))
	mux.HandleFunc("GET /users/{username}", s.requireAuth(s.handleGetUser))

	mux.HandleFunc("POST /channels", s.requireAuth(s.handleCreateChannel))
	mux.HandleFunc("GET /channels", s.requireAuth(s.handleListChannels))
	mux.HandleFunc("POST /channels/join", s.requireAuth(s.handleJoinChannel))
	mux.HandleFunc("POST /channels/direct", s.requireAuth(s.handleOpenDirect))
	mux.HandleFunc("POST /channels/{id}/leave", s.requireAuth(s.handleLeaveChannel))

	mux.HandleFunc("POST /messages", s.requireAuth(s.handleSendMessage))
	mux.HandleFunc("GET /messages", s.requireAuth(s.handleListMessages))

	mux.HandleFunc("POST /friends/requests", s.requireAuth(s.handleSendFriendRequest))
	mux.HandleFunc("GET /friends/requests", s.requireAuth(s.handleListFriendRequests))
	mux.HandleFunc("POST /friends/requests/{id}/accept", s.requireAuth(s.handleAcceptFriendRequest))
	mux.HandleFunc("POST /friends/requests/{id}/decline", s.requireAuth(s.handleDeclineFriendRequest))
	mux.HandleFunc("GET /friends", s.requireAuth(s.handleListFriends))

	mux.HandleFunc("GET /ws", s.requireAuth(s.handleWS))

	mux.Handle("GET /", http.FileServer(http.Dir(webRoot)))

	return withLogging(mux)
}
