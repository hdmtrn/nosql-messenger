package main

import (
	"sync"
	"time"
)

type Subscriber struct {
	userID string
	// The session the socket was opened with. The socket outlives the request
	// that authenticated it, so revoking the session has to close it too.
	sessionID string
	send      chan []byte
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

func newSubscriber(userID string) *Subscriber {
	return &Subscriber{
		userID:   userID,
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

	for _, chID := range channelIDs {
		h.attach(c, chID)
	}
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
	c.channels = nil

	delete(h.byUser[c.userID], c)
	if len(h.byUser[c.userID]) == 0 {
		delete(h.byUser, c.userID)
	}
	close(c.send)
}
