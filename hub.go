package main

import "sync"

type Subscriber struct {
	userID string

	send chan []byte
}

type Hub struct {
	mu       sync.Mutex
	channels map[string]map[string]*Subscriber
}

func NewHub() *Hub {
	return &Hub{channels: make(map[string]map[string]*Subscriber)}
}

func (h *Hub) register(chID string, c *Subscriber) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.channels[chID] == nil {
		h.channels[chID] = make(map[string]*Subscriber)
	}

	if old, ok := h.channels[chID][c.userID]; ok {
		close(old.send)
	}
	h.channels[chID][c.userID] = c
	return len(h.channels[chID])
}

func (h *Hub) unregister(chID string, c *Subscriber) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	if cur, ok := h.channels[chID][c.userID]; !ok || cur != c {
		return len(h.channels[chID])
	}
	delete(h.channels[chID], c.userID)
	close(c.send)
	return len(h.channels[chID])
}

func (h *Hub) Publish(chID string, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.channels[chID] {
		select {
		case c.send <- msg:

		default:

			delete(h.channels[chID], c.userID)
			close(c.send)
		}
	}
}
