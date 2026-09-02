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
	byChannel map[string]map[string]*Subscriber
	byUser    map[string]*Subscriber
}

func NewHub() *Hub {
	return &Hub{
		byChannel: make(map[string]map[string]*Subscriber),
		byUser:    make(map[string]*Subscriber),
	}
}

func (h *Hub) Connect(c *Subscriber, channelIDs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if old, ok := h.byUser[c.userID]; ok {
		h.drop(old)
	}
	h.byUser[c.userID] = c

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
	if c, ok := h.byUser[userID]; ok {
		h.attach(c, chID)
	}
}

func (h *Hub) Publish(chID string, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.byChannel[chID] {
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
		h.byChannel[chID] = make(map[string]*Subscriber)
	}
	h.byChannel[chID][c.userID] = c
	c.channels[chID] = struct{}{}
}

func (h *Hub) drop(c *Subscriber) {
	if c.dropped {
		return
	}
	c.dropped = true

	for chID := range c.channels {
		delete(h.byChannel[chID], c.userID)
		if len(h.byChannel[chID]) == 0 {
			delete(h.byChannel, chID)
		}
	}
	c.channels = nil

	if cur, ok := h.byUser[c.userID]; ok && cur == c {
		delete(h.byUser, c.userID)
	}
	close(c.send)
}
