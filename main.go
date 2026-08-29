package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

const dbName = "messenger"

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	mongoClient, err := connectMongo(ctx)
	if err != nil {
		return err
	}
	defer mongoClient.Disconnect(ctx)
	log.Println("connected to MongoDB")

	db := mongoClient.Database(dbName)

	users := newUserStore(db)

	if err := users.ensureIndexes(ctx); err != nil {
		return fmt.Errorf("creating users indexes: %w", err)
	}

	sessions := newSessionStore(db)
	if err := sessions.ensureIndexes(ctx); err != nil {
		return fmt.Errorf("creating sessions indexes: %w", err)
	}
	go sessions.sweepCache(ctx)

	channels := newChannelStore(db)
	if err := channels.ensureIndexes(ctx); err != nil {
		return fmt.Errorf("creating channels indexes: %w", err)
	}

	messages := newMessageStore(db)
	if err := messages.ensureIndexes(ctx); err != nil {
		return fmt.Errorf("creating messages indexes: %w", err)
	}

	authSvc, err := newAuth(users, sessions)
	if err != nil {
		return fmt.Errorf("initializing auth: %w", err)
	}

	hub := NewHub()

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           routes(mongoClient, hub, authSvc, sessions),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("cookies: Secure=%v (set COOKIE_SECURE=false for plain http)", secureCookies)
	log.Println("listening on", srv.Addr)
	return srv.ListenAndServe()
}

func routes(mongoClient *mongo.Client, hub *Hub, a *auth, sessions *sessionStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(mongoClient))
	mux.HandleFunc("POST /auth/register", a.handleRegister)
	mux.HandleFunc("POST /auth/login", a.handleLogin)
	mux.HandleFunc("POST /auth/logout", a.handleLogout)
	mux.HandleFunc("GET /auth/me", a.handleMe)
	mux.HandleFunc("GET /ws", handleWS(hub, sessions))
	mux.Handle("GET /", http.FileServer(http.Dir("static")))
	return withLogging(mux)
}
