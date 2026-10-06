package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// The bus carries what has to reach the other instances but does not have to
// survive a restart: a new message now, a revoked session and "typing" later.
// Everything durable stays in MongoDB, so a lost event costs latency, not data —
// the message is stored before it is announced, and the client catches up when
// its socket is sent ready or resync.
const (
	channelTopic = "ch:"
	// One topic for all instances: revocations are rare, and every node has to
	// hear about every one of them. What travels is the session id, never the
	// token — the token is the credential itself, and there is no reason to put
	// it on the wire. Rocket.Chat keeps a hash of the token for the same reason.
	sessionTopic = "session-revoked"
	// Also one topic for all: a subscription change concerns one user, and
	// nobody knows which nodes hold that user's sockets.
	subscriptionTopic = "subscription"
	// Says that something published may have been lost: sent by a node whose
	// publishes failed for a while, and by a node that has just started.
	resyncTopic = "resync"
	// A publish outlives the request that caused it, so it cannot borrow the
	// request context — but it must not hang either.
	publishTimeout = 2 * time.Second
	// How long rebuilding after a gap may read MongoDB.
	gapTimeout = 30 * time.Second
)

// redisOptions connects with REDIS_PASSWORD when it is set; empty sends no
// AUTH, as the local stack runs.
func redisOptions() *redis.Options {
	return &redis.Options{Addr: redisAddr(), Password: os.Getenv("REDIS_PASSWORD")}
}

func redisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

// A publisher reaches every instance. *bus is the real one; *Hub satisfies the
// same interface and reaches this process only, which is what tests use — they
// then need neither Redis nor a second instance to check that a handler
// broadcasts what it should.
//
// Subscribe and Unsubscribe route a user's open sockets to a channel. This is
// not membership — that lives in MongoDB — but the in-memory view of it that
// the hub delivers by, and every node holding the user's sockets has to update
// its own.
//
// Settled returns once every channel this node listens to is confirmed by
// Redis, so that nothing published from then on can pass it by.
type publisher interface {
	Publish(chID string, msg []byte)
	Subscribe(userID, chID string)
	Unsubscribe(userID, chID string)
	Settled(ctx context.Context) error
}

// resyncFrame tells this node's sockets that the bus was down for a while and
// they may have missed messages; the client then fetches what it lacks.
var resyncFrame = []byte(`{"type":"resync"}`)

type subscriptionChange struct {
	UserID     string `json:"user_id"`
	ChID       string `json:"channel_id"`
	Subscribed bool   `json:"subscribed"`
}

type bus struct {
	rdb *redis.Client
	sub *redis.PubSub
	hub *Hub

	// Channels this node still has to subscribe to or unsubscribe from. A set,
	// not a queue: the hub announces a change under its own mutex, so this must
	// never block, and a cold start after a restart announces every channel of
	// the node at once. Repeats collapse, so nothing has to be dropped.
	mu      sync.Mutex
	pending map[string]bool
	wake    chan struct{}
	// Callers of Settled not yet seen by Run, under mu.
	waiters []chan struct{}

	// Run's own: subscriptions written to Redis and not yet confirmed, and the
	// callers waiting for that count to reach zero.
	unconfirmed int
	held        []chan struct{}

	// Set by main once the session store exists. Without it a revocation event
	// is simply ignored, which is what a test that only checks messages wants.
	onSessionRevoked func(sessionID string)

	// Runs after this node may have missed events: it rebuilds what it learned
	// from the bus and tells its sockets to catch up. Without it the sockets are
	// only told.
	onGap func()

	// A publish failed and no node has been told yet.
	lost atomic.Bool
}

func newBus(ctx context.Context, hub *Hub) (*bus, error) {
	return newBusWith(ctx, hub, redisOptions())
}

// newBusWith takes the options, so that a test can name its connections and
// find them in Redis.
func newBusWith(ctx context.Context, hub *Hub, opts *redis.Options) (*bus, error) {
	rdb := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("ping Redis: %w", err)
	}

	// The permanent topics are subscribed here rather than in Run, so that once
	// this function returns the instance is certainly listening. In Run it
	// would happen in another goroutine, and an event published right after
	// startup could slip past.
	permanent := []string{sessionTopic, subscriptionTopic, resyncTopic}
	sub := rdb.Subscribe(ctx)
	err := sub.Subscribe(ctx, permanent...)
	// Subscribe only writes the command; Redis may not have run it yet. Each
	// topic is confirmed by a reply of its own, and only after reading them
	// all is the instance really listening — a test caught an event published
	// in the gap between the two.
	for i := 0; err == nil && i < len(permanent); i++ {
		_, err = sub.Receive(pingCtx)
	}
	if err != nil {
		sub.Close()
		rdb.Close()
		return nil, fmt.Errorf("subscribing to the permanent topics: %w", err)
	}

	return &bus{
		rdb:     rdb,
		sub:     sub,
		hub:     hub,
		pending: make(map[string]bool),
		wake:    make(chan struct{}, 1),
	}, nil
}

// Publish sends a message to every instance, this one included: the sender sees
// it on the way back from Redis like everybody else. One path means one order
// of messages on all nodes and no need to filter out an echo of our own event.
func (b *bus) Publish(chID string, msg []byte) {
	b.publish(channelTopic+chID, msg)
}

// publish sends to every node. A failure is remembered: what failed here reached
// no node, and none of them can tell. Once Redis takes a publish again, every
// node is told to resync.
func (b *bus) publish(topic string, payload any) {
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	if err := b.rdb.Publish(ctx, topic, payload).Err(); err != nil {
		b.lost.Store(true)
		log.Printf("bus: publishing to %s: %v", topic, err)
		return
	}
	b.announceIfLost(ctx)
}

func (b *bus) announceIfLost(ctx context.Context) {
	if !b.lost.CompareAndSwap(true, false) {
		return
	}
	log.Printf("bus: publishes were lost, telling every node to resync")
	if err := b.rdb.Publish(ctx, resyncTopic, "lost").Err(); err != nil {
		b.lost.Store(true)
		log.Printf("bus: publishing a resync: %v", err)
	}
}

// ResyncAll tells every node that something may have been missed. A node that
// starts calls it: if it went down holding lost publishes, nobody else knows.
func (b *bus) ResyncAll() {
	b.publish(resyncTopic, "started")
}

// PublishRevoked tells the other instances to forget a session they may have
// cached. The revoking node has already dropped its own copy: if Redis is down,
// revocation must still work where it was asked for.
func (b *bus) PublishRevoked(sessionID string) {
	b.publish(sessionTopic, sessionID)
}

// Subscribe and Unsubscribe apply the change here first and then announce it,
// as a revocation does: with Redis down, a join still works for the sockets on
// this node. Our own event comes back from Redis and is applied a second time,
// which is harmless — both hub methods are idempotent, so there is no need to
// tell our echo apart from somebody else's event.
func (b *bus) Subscribe(userID, chID string) {
	b.hub.Subscribe(userID, chID)
	b.publishSubscription(subscriptionChange{UserID: userID, ChID: chID, Subscribed: true})
}

func (b *bus) Unsubscribe(userID, chID string) {
	b.hub.Unsubscribe(userID, chID)
	b.publishSubscription(subscriptionChange{UserID: userID, ChID: chID, Subscribed: false})
}

func (b *bus) publishSubscription(c subscriptionChange) {
	payload, err := json.Marshal(c)
	if err != nil {
		log.Printf("bus: encoding a subscription change: %v", err)
		return
	}
	b.publish(subscriptionTopic, payload)
}

// Watch and Unwatch are called from the hub while it holds its mutex, so they
// only note what has to change; talking to Redis is the bus goroutine's job.
func (b *bus) Watch(chID string)   { b.change(chID, true) }
func (b *bus) Unwatch(chID string) { b.change(chID, false) }

func (b *bus) change(chID string, on bool) {
	b.mu.Lock()
	b.pending[chID] = on
	b.mu.Unlock()

	// One pending signal is enough: it says there is work, the set says what.
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// applyWatches takes the whole set at once and tells Redis in one command per
// direction, which is what makes a burst of a few hundred channels a couple of
// round trips instead of a few hundred. A failed command is only logged:
// go-redis records the channels whatever the command returned, and resubscribes
// them itself when it reconnects.
func (b *bus) applyWatches(ctx context.Context) {
	b.mu.Lock()
	pending := b.pending
	b.pending = make(map[string]bool)
	waiters := b.waiters
	b.waiters = nil
	b.mu.Unlock()

	var add, drop []string
	for chID, on := range pending {
		if on {
			add = append(add, channelTopic+chID)
		} else {
			drop = append(drop, channelTopic+chID)
		}
	}

	if len(add) > 0 {
		// Redis confirms each channel with a reply of its own; until they are all
		// in, a publish can still pass the node by. A failed write is not
		// counted: go-redis keeps the channels and subscribes them again when it
		// reconnects, and the reconnection triggers a resync.
		if err := b.sub.Subscribe(ctx, add...); err != nil {
			log.Printf("bus: subscribing to %d channels: %v", len(add), err)
		} else {
			b.unconfirmed += len(add)
		}
	}
	if len(drop) > 0 {
		if err := b.sub.Unsubscribe(ctx, drop...); err != nil {
			log.Printf("bus: unsubscribing from %d channels: %v", len(drop), err)
		}
	}

	b.held = append(b.held, waiters...)
	b.release()
}

// Close releases the connections to Redis once nothing publishes any more. The
// subscription is not among them: Run closes it when its context ends.
func (b *bus) Close() error {
	return b.rdb.Close()
}

// Run owns the subscription for the lifetime of the process: one connection to
// Redis for the whole instance, not one per client as Revolt does.
func (b *bus) Run(ctx context.Context) {
	// Channel topics come and go with the local readers; the permanent ones were
	// subscribed in newBus, because a revocation or a subscription change
	// concerns every node whatever it happens to be watching.
	sub := b.sub
	defer sub.Close()

	// With the confirmations, not only the messages: they tell when a watch has
	// taken effect, and when go-redis has reconnected.
	incoming := sub.ChannelWithSubscriptions()

	for {
		select {
		case <-ctx.Done():
			return

		case <-b.wake:
			b.applyWatches(ctx)

		case m, ok := <-incoming:
			if !ok {
				return
			}
			switch m := m.(type) {
			case *redis.Subscription:
				b.confirmed(m)
			case *redis.Message:
				b.dispatch(m)
			}
		}
	}
}

func (b *bus) dispatch(m *redis.Message) {
	switch {
	case strings.HasPrefix(m.Channel, channelTopic):
		b.hub.Publish(strings.TrimPrefix(m.Channel, channelTopic), []byte(m.Payload))
	case m.Channel == sessionTopic:
		if b.onSessionRevoked != nil {
			b.onSessionRevoked(m.Payload)
		}
	case m.Channel == resyncTopic:
		b.gap()
	case m.Channel == subscriptionTopic:
		var c subscriptionChange
		if err := json.Unmarshal([]byte(m.Payload), &c); err != nil {
			log.Printf("bus: unreadable subscription change %q: %v", m.Payload, err)
			return
		}
		if c.Subscribed {
			b.hub.Subscribe(c.UserID, c.ChID)
		} else {
			b.hub.Unsubscribe(c.UserID, c.ChID)
		}
	}
}

// confirmed counts a subscription off. The permanent topics are subscribed once,
// in newBus, so a confirmation of one here means go-redis lost the connection
// and subscribed everything again, and whatever was published in between never
// reached this node: its sockets are told to catch up.
func (b *bus) confirmed(m *redis.Subscription) {
	if m.Kind != "subscribe" {
		return
	}
	if m.Channel == sessionTopic {
		log.Printf("bus: resubscribed after a lost connection")
		b.gap()
		// Whatever this node failed to publish meanwhile, the others never got.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
			defer cancel()
			b.announceIfLost(ctx)
		}()
	}
	if b.unconfirmed > 0 {
		b.unconfirmed--
	}
	b.release()
}

// gap hands a possible loss to onGap, off Run's goroutine: rebuilding reads
// MongoDB, and Run must keep reading Redis meanwhile.
func (b *bus) gap() {
	if b.onGap == nil {
		b.hub.SendAll(resyncFrame)
		return
	}
	go b.onGap()
}

// release lets the waiting callers of Settled go once nothing is outstanding.
func (b *bus) release() {
	if b.unconfirmed > 0 {
		return
	}
	for _, done := range b.held {
		close(done)
	}
	b.held = nil
}

// Settled waits until Redis has confirmed every subscription this node asked for
// so far. A socket's channels are watched in Connect, before this is called, so
// once it returns the node hears everything published to them.
func (b *bus) Settled(ctx context.Context) error {
	done := make(chan struct{})
	b.mu.Lock()
	b.waiters = append(b.waiters, done)
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// recoverFromGap rebuilds what this node learns from the bus after it may have
// missed some of it: which channels each local socket reads, and whether its
// session still exists. Only then are the sockets told to catch up, so that what
// they fetch and what reaches them afterwards agree.
func (s *server) recoverFromGap() {
	ctx, cancel := context.WithTimeout(context.Background(), gapTimeout)
	defer cancel()

	byUser := map[string][]*Subscriber{}
	for _, c := range s.hub.Subscribers() {
		byUser[c.userID] = append(byUser[c.userID], c)
	}
	for userID, socks := range byUser {
		id, err := bson.ObjectIDFromHex(userID)
		if err != nil {
			continue
		}
		channels, err := s.channels.memberOf(ctx, id)
		if err != nil {
			log.Printf("recovering after a gap: %v", err)
			continue
		}
		ids := make([]string, len(channels))
		for i, ch := range channels {
			ids[i] = ch.Hex()
		}
		s.hub.SetChannels(userID, ids)

		for _, c := range socks {
			sid, err := bson.ObjectIDFromHex(c.sessionID)
			if err != nil {
				continue
			}
			alive, err := s.sessions.exists(ctx, sid)
			if err != nil {
				log.Printf("recovering after a gap: %v", err)
				continue
			}
			if !alive {
				s.sessions.invalidateByID(sid)
				s.hub.CloseSession(c.sessionID)
			}
		}
	}
	s.hub.SendAll(resyncFrame)
}
