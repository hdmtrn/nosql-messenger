package main

import (
	"sync"
	"testing"
)

func TestDropRemovesFromAllChannelsOnce(t *testing.T) {
	h := NewHub()
	c := newSubscriber("u1")
	h.Connect(c, []string{"a", "b"})

	for i := 0; i < sendBuffer; i++ {
		h.Publish("a", []byte("x"))
	}
	if len(h.byChannel["a"]) != 1 {
		t.Fatalf("subscriber dropped too early")
	}

	h.Publish("a", []byte("overflow"))

	if _, ok := h.byChannel["a"]; ok {
		t.Errorf("still present in channel a")
	}
	if _, ok := h.byChannel["b"]; ok {
		t.Errorf("still present in channel b")
	}
	if _, ok := h.byUser["u1"]; ok {
		t.Errorf("still present in byUser")
	}

	h.Publish("b", []byte("after drop"))
	h.Disconnect(c)
	h.Disconnect(c)
}

func TestReconnectEvictsPreviousConnection(t *testing.T) {
	h := NewHub()
	old := newSubscriber("u1")
	h.Connect(old, []string{"a"})

	fresh := newSubscriber("u1")
	h.Connect(fresh, []string{"a"})

	if _, open := <-old.send; open {
		t.Errorf("previous connection was not closed")
	}

	h.Publish("a", []byte("hello"))
	if len(fresh.send) != 1 {
		t.Errorf("new connection did not receive: %d", len(fresh.send))
	}
}

func TestHubConcurrentAccess(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c := newSubscriber(string(rune('a' + n%26)))
			h.Connect(c, []string{"room", "lobby"})
			for j := 0; j < 100; j++ {
				h.Publish("room", []byte("msg"))
				h.Subscribe(c.userID, "extra")
				<-c.send
			}
			h.Disconnect(c)
		}(i)
	}
	wg.Wait()
}
