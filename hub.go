package main

import "sync"

type Subscriber struct {
	userID string
	send   chan []byte

	channels map[string]struct{}
	dropped  bool
}

func newSubscriber(userID string) *Subscriber {
	return &Subscriber{
		userID:   userID,
		send:     make(chan []byte, sendBuffer),
		channels: make(map[string]struct{}),
	}
}

type Hub struct {
	mu        sync.Mutex
	byChannel map[string]map[*Subscriber]struct{}
	byUser    map[string]map[*Subscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{
		byChannel: make(map[string]map[*Subscriber]struct{}),
		byUser:    make(map[string]map[*Subscriber]struct{}),
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
		}
	}
	c.channels = nil

	delete(h.byUser[c.userID], c)
	if len(h.byUser[c.userID]) == 0 {
		delete(h.byUser, c.userID)
	}
	close(c.send)
}
