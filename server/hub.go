package main

import (
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Subscriber struct {
	userID string
	// The session the socket was opened with. The socket outlives the request
	// that authenticated it, so revoking the session has to close it too.
	sessionID string
	// Identifies this socket in Redis, where presence counts the user's open
	// sockets across all nodes.
	socketID string
	send     chan []byte
	// Why the hub dropped the socket, as a WebSocket close code; zero is a plain
	// drop, which the client answers by reconnecting. Written under the hub's
	// mutex before send is closed, and read by the write pump only after it saw
	// send closed — a channel close orders the two, so no lock is needed.
	closeCode int

	channels map[string]struct{}
	dropped  bool

	// When this connection last sent a typing frame, and last announced typing
	// per channel. Touched only by the connection's read goroutine, so they
	// need no lock.
	lastTyping time.Time
	typedAt    map[string]time.Time
}

// userTopic addresses one person instead of a channel. The hub indexes sockets
// by user already, but that index reaches this process only, while the bus
// carries channel topics — so an event for somebody sitting on another node
// needs an address of the same kind. The prefix keeps the key out of the space
// of channel ids, and a pseudo-channel costs the bus no second kind of topic
// and leaves broadcast with the one seam it has.
func userTopic(userID string) string { return "u:" + userID }

func newSubscriber(userID string) *Subscriber {
	return &Subscriber{
		userID:   userID,
		socketID: bson.NewObjectID().Hex(),
		send:     make(chan []byte, sendBuffer),
		channels: make(map[string]struct{}),
		typedAt:  make(map[string]time.Time),
	}
}

type Hub struct {
	mu        sync.Mutex
	byChannel map[string]map[*Subscriber]struct{}
	byUser    map[string]map[*Subscriber]struct{}

	// Called when this instance gains its first local subscriber of a channel
	// and loses its last one, so that it listens on the bus only for channels
	// somebody here is actually watching. They run under the mutex and must
	// not block; the bus only leaves itself a note. Without a bus they do
	// nothing, which is what tests and a single instance need.
	watch   func(chID string)
	unwatch func(chID string)
}

func NewHub() *Hub {
	return &Hub{
		byChannel: make(map[string]map[*Subscriber]struct{}),
		byUser:    make(map[string]map[*Subscriber]struct{}),
		watch:     func(string) {},
		unwatch:   func(string) {},
	}
}

func (h *Hub) Connect(c *Subscriber, channelIDs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.byUser[c.userID] == nil {
		h.byUser[c.userID] = make(map[*Subscriber]struct{})
	}
	h.byUser[c.userID][c] = struct{}{}

	// Every socket reads its owner's topic, so a node subscribes to it for as
	// long as it holds one of their sockets, and stops when it holds none.
	h.attach(c, userTopic(c.userID))
	for _, chID := range channelIDs {
		h.attach(c, chID)
	}
}

// Subscribers copies every socket of this node, which presence needs when it
// has to register them in Redis again.
func (h *Hub) Subscribers() []*Subscriber {
	h.mu.Lock()
	defer h.mu.Unlock()

	var all []*Subscriber
	for _, subs := range h.byUser {
		for c := range subs {
			all = append(all, c)
		}
	}
	return all
}

// ChannelsOf copies the socket's channels, which is who has to hear that its
// user came or went. The set changes under the mutex as membership does.
func (h *Hub) ChannelsOf(c *Subscriber) []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	ids := make([]string, 0, len(c.channels))
	for chID := range c.channels {
		ids = append(ids, chID)
	}
	return ids
}

func (h *Hub) Disconnect(c *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.drop(c)
}

func (h *Hub) Subscribe(userID, chID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.byUser[userID] {
		h.attach(c, chID)
	}
}

func (h *Hub) Unsubscribe(userID, chID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.byUser[userID] {
		delete(h.byChannel[chID], c)
		delete(c.channels, chID)
	}
	// The check is on presence, not on length: a channel nobody here watched
	// reads as empty too, and unwatching it would be a message to Redis about
	// a subscription we never had.
	if subs, ok := h.byChannel[chID]; ok && len(subs) == 0 {
		delete(h.byChannel, chID)
		h.unwatch(chID)
	}
}

// Reads reports whether this connection is routed to a channel. It is the
// in-memory view of membership: a socket gets a channel on connect and on join,
// and loses it on leave, so asking here costs a map lookup instead of a query.
// It trails MongoDB by the time a subscription change takes over the bus, which
// is fine for "typing" and would not be for anything stored.
func (h *Hub) Reads(c *Subscriber, chID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	// A dropped socket keeps its channels for the announcement, but it is out
	// of every index and reads nothing any more.
	if c.dropped {
		return false
	}
	_, ok := c.channels[chID]
	return ok
}

// CloseSession drops every socket opened with a session, on this instance. A
// session can hold several — each tab of a browser shares its cookie. Revocation
// only knows the session id, so this scans the connected users rather than keep
// a second index in step on every connect; revocations are rare. Dropping closes
// send, so the write pump says goodbye and the socket's read pump ends.
func (h *Hub) CloseSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, subs := range h.byUser {
		for c := range subs {
			if c.sessionID == sessionID && !c.dropped {
				c.closeCode = closeSessionEnded
				h.drop(c)
			}
		}
	}
}

func (h *Hub) Publish(chID string, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.byChannel[chID] {
		select {
		case c.send <- msg:
		default:
			h.drop(c)
		}
	}
}

func (h *Hub) attach(c *Subscriber, chID string) {
	if c.dropped {
		return
	}
	if h.byChannel[chID] == nil {
		h.byChannel[chID] = make(map[*Subscriber]struct{})
		h.watch(chID)
	}
	h.byChannel[chID][c] = struct{}{}
	c.channels[chID] = struct{}{}
}

func (h *Hub) drop(c *Subscriber) {
	if c.dropped {
		return
	}
	c.dropped = true

	for chID := range c.channels {
		delete(h.byChannel[chID], c)
		if len(h.byChannel[chID]) == 0 {
			delete(h.byChannel, chID)
			h.unwatch(chID)
		}
	}
	// The set itself stays: the socket is out of every index already, and the
	// channels it read are who presence has to tell that its user went.

	delete(h.byUser[c.userID], c)
	if len(h.byUser[c.userID]) == 0 {
		delete(h.byUser, c.userID)
	}
	close(c.send)
}
