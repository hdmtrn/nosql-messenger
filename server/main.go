package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const dbName = "messenger"

// shutdownTimeout is how long a stopping node waits for its requests and
// sockets. It has to fit inside the grace period of whatever stops the
// container (stop_grace_period in compose, stopTimeout in ECS), or the process
// is killed in the middle of the drain.
const shutdownTimeout = 20 * time.Second

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	// docker and ECS stop a container with SIGTERM. Without a handler the Go
	// runtime exits on the spot: requests in flight are cut off and sockets die
	// without a close frame. The background loops started below end with ctx,
	// at the signal; requests and sockets are drained at the end of run.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mongoClient, err := connectMongo(ctx)
	if err != nil {
		return err
	}
	// Not ctx, which has ended by then: Disconnect with an ended context closes
	// the connections still in use instead of waiting for them.
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mongoClient.Disconnect(dctx); err != nil {
			log.Printf("disconnecting from MongoDB: %v", err)
		}
	}()
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

	hub := NewHub()

	bus, err := newBus(ctx, hub)
	if err != nil {
		return err
	}
	defer bus.Close()
	// Method values: hub keeps the two functions, not the bus itself, so it
	// stays unaware of what is on the other end.
	hub.watch, hub.unwatch = bus.Watch, bus.Unwatch

	wireRevocation(sessions, hub, bus)

	go bus.Run(ctx)
	log.Println("connected to Redis")

	srv := &server{
		mongo:    mongoClient,
		hub:      hub,
		bus:      bus,
		presence: newPresence(bus.rdb),
		auth:     authSvc,
		sessions: sessions,
		users:    users,
		channels: channels,
		messages: messages,
		friends:  friends,
		invites:  invites,
		media:    media,
	}

	go srv.runPresence(ctx)
	go srv.runPurge(ctx)
	go srv.runFileSweep(ctx)

	// Closed rather than drained at shutdown: nobody waits for a profile.
	internal := internalServer()
	go func() {
		// Diagnostics, not service: a node that cannot bind it still serves users.
		if err := internal.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("internal endpoints: %v", err)
		}
	}()
	defer internal.Close()
	log.Println("internal endpoints on", internal.Addr)

	httpSrv := srv.httpServer(":8080")

	log.Printf("cookies: Secure=%v (set COOKIE_SECURE=false for plain http)", secureCookies)
	log.Println("listening on", httpSrv.Addr)

	served := make(chan error, 1)
	go func() { served <- httpSrv.ListenAndServe() }()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	// A second signal now kills the process the default way, for when the
	// drain itself hangs.
	stop()
	log.Println("shutting down")

	drainCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.drain(drainCtx, httpSrv); err != nil {
		log.Printf("shutdown: %v", err)
	}
	log.Println("drained, closing Redis and MongoDB")
	return nil
}

// httpServer builds the server run starts, in a function of its own so that the
// shutdown test stops this very server rather than a copy of its settings.
func (s *server) httpServer(addr string) *http.Server {
	hs := &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// Shutdown leaves hijacked connections alone, and every socket is one. It
	// calls this after closing the listener, so a client told to reconnect
	// cannot land on this node again.
	hs.RegisterOnShutdown(func() { s.hub.CloseAll(closeServiceRestart) })
	return hs
}

// drain stops hs the way a deploy needs: no new connections, sockets told to
// reconnect elsewhere, requests in flight allowed to finish, and the socket
// handlers waited for, since they use Redis and MongoDB until they return.
func (s *server) drain(ctx context.Context, hs *http.Server) error {
	if err := hs.Shutdown(ctx); err != nil {
		return fmt.Errorf("requests still running: %w", err)
	}

	// Waiting is safe only now. Every request has finished or been hijacked,
	// and handleWS counts itself before the hijack, so no handler can start
	// counting while Wait runs.
	done := make(chan struct{})
	go func() {
		s.sockets.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("sockets still open: %w", ctx.Err())
	}
}

// wireRevocation connects the two directions of a revocation: this node
// announces its own, and acts on the ones announced elsewhere. The announcing
// node has already dropped its copy and closed its sockets, so hearing its own
// event back changes nothing — and with Redis down, revocation still works
// where it was asked. Invalidating the cache stops new requests; closing the
// sockets stops the ones already open, which authenticated once and would
// otherwise live on. A function of its own so that the test runs this very
// wiring instead of a copy of it.
func wireRevocation(sessions *sessionStore, hub *Hub, b *bus) {
	sessions.onRevoked = func(id bson.ObjectID) {
		hub.CloseSession(id.Hex())
		b.PublishRevoked(id.Hex())
	}
	b.onSessionRevoked = func(id string) {
		oid, err := bson.ObjectIDFromHex(id)
		if err != nil {
			log.Printf("bus: revocation of an unreadable session id %q", id)
			return
		}
		sessions.invalidateByID(oid)
		hub.CloseSession(id)
	}
}
