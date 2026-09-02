package main

import (
	"net/http"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

type server struct {
	mongo    *mongo.Client
	hub      *Hub
	auth     *auth
	sessions *sessionStore
	channels *channelStore
	messages *messageStore
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth)

	mux.HandleFunc("POST /auth/register", s.auth.handleRegister)
	mux.HandleFunc("POST /auth/login", s.auth.handleLogin)
	mux.HandleFunc("POST /auth/logout", s.auth.handleLogout)
	mux.HandleFunc("GET /auth/me", s.auth.handleMe)

	mux.HandleFunc("GET /ws", s.requireAuth(s.handleWS))

	mux.Handle("GET /", http.FileServer(http.Dir("static")))

	return withLogging(mux)
}
