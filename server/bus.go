package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// The bus carries what has to reach the other instances but does not have to
// survive a restart: a new message now, a revoked session and "typing" later.
// Everything durable stays in MongoDB, so a lost event costs latency, not data —
// the message is stored before it is announced and the client backfills on
// reconnect.
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
	// A publish outlives the request that caused it, so it cannot borrow the
	// request context — but it must not hang either.
	publishTimeout = 2 * time.Second
)

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
type publisher interface {
	Publish(chID string, msg []byte)
	Subscribe(userID, chID string)
	Unsubscribe(userID, chID string)
}

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

	// Set by main once the session store exists. Without it a revocation event
	// is simply ignored, which is what a test that only checks messages wants.
	onSessionRevoked func(sessionID string)
}

func newBus(ctx context.Context, hub *Hub) (*bus, error) {
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr()})

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("ping Redis: %w", err)
	}

	// The permanent topics are subscribed here rather than in Run, so that once
	// this function returns the instance is certainly listening. In Run it
	// would happen in another goroutine, and an event published right after
	// startup could slip past.
	permanent := []string{sessionTopic, subscriptionTopic}
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
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()

	if err := b.rdb.Publish(ctx, channelTopic+chID, msg).Err(); err != nil {
		log.Printf("bus: publishing to %s: %v", chID, err)
	}
}

// PublishRevoked tells the other instances to forget a session they may have
// cached. The revoking node has already dropped its own copy: if Redis is down,
// revocation must still work where it was asked for.
func (b *bus) PublishRevoked(sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()

	if err := b.rdb.Publish(ctx, sessionTopic, sessionID).Err(); err != nil {
		log.Printf("bus: publishing the revocation of %s: %v", sessionID, err)
	}
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

	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()

	if err := b.rdb.Publish(ctx, subscriptionTopic, payload).Err(); err != nil {
		log.Printf("bus: publishing a subscription change for %s: %v", c.ChID, err)
	}
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
		if err := b.sub.Subscribe(ctx, add...); err != nil {
			log.Printf("bus: subscribing to %d channels: %v", len(add), err)
		}
	}
	if len(drop) > 0 {
		if err := b.sub.Unsubscribe(ctx, drop...); err != nil {
			log.Printf("bus: unsubscribing from %d channels: %v", len(drop), err)
		}
	}
}

// Run owns the subscription for the lifetime of the process: one connection to
// Redis for the whole instance, not one per client as Revolt does.
func (b *bus) Run(ctx context.Context) {
	// Channel topics come and go with the local readers; the permanent ones were
	// subscribed in newBus, because a revocation or a subscription change
	// concerns every node whatever it happens to be watching.
	sub := b.sub
	defer sub.Close()

	incoming := sub.Channel()

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
			switch {
			case strings.HasPrefix(m.Channel, channelTopic):
				b.hub.Publish(strings.TrimPrefix(m.Channel, channelTopic), []byte(m.Payload))
			case m.Channel == sessionTopic:
				if b.onSessionRevoked != nil {
					b.onSessionRevoked(m.Payload)
				}
			case m.Channel == subscriptionTopic:
				var c subscriptionChange
				if err := json.Unmarshal([]byte(m.Payload), &c); err != nil {
					log.Printf("bus: unreadable subscription change %q: %v", m.Payload, err)
					continue
				}
				if c.Subscribed {
					b.hub.Subscribe(c.UserID, c.ChID)
				} else {
					b.hub.Unsubscribe(c.UserID, c.ChID)
				}
			}
		}
	}
}
