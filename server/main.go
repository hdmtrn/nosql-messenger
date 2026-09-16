package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
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

	friends := newFriendStore(db)
	if err := friends.ensureIndexes(ctx); err != nil {
		return fmt.Errorf("creating friend request indexes: %w", err)
	}

	invites := newInviteStore(db)
	if err := invites.ensureIndexes(ctx); err != nil {
		return fmt.Errorf("creating invite indexes: %w", err)
	}

	media := newMediaStore(db)
	if err := media.ensureIndexes(ctx); err != nil {
		return fmt.Errorf("creating media indexes: %w", err)
	}
	go media.sweepExpired(ctx)

	authSvc, err := newAuth(users, sessions)
	if err != nil {
		return fmt.Errorf("initializing auth: %w", err)
	}

	srv := &server{
		mongo:    mongoClient,
		hub:      NewHub(),
		auth:     authSvc,
		sessions: sessions,
		users:    users,
		channels: channels,
		messages: messages,
		friends:  friends,
		invites:  invites,
		media:    media,
	}

	httpSrv := &http.Server{
		Addr:              ":8080",
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("cookies: Secure=%v (set COOKIE_SECURE=false for plain http)", secureCookies)
	log.Println("listening on", httpSrv.Addr)
	return httpSrv.ListenAndServe()
}
