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

func TestSecondConnectionOfSameUserAlsoReceives(t *testing.T) {
	h := NewHub()
	first := newSubscriber("u1")
	h.Connect(first, []string{"a"})

	second := newSubscriber("u1")
	h.Connect(second, []string{"a"})

	h.Publish("a", []byte("hello"))

	if len(first.send) != 1 {
		t.Errorf("first connection did not receive: %d", len(first.send))
	}
	if len(second.send) != 1 {
		t.Errorf("second connection did not receive: %d", len(second.send))
	}

	h.Disconnect(first)
	h.Publish("a", []byte("again"))
	if len(second.send) != 2 {
		t.Errorf("surviving connection stopped receiving: %d", len(second.send))
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	leaver := newSubscriber("u1")
	stayer := newSubscriber("u2")
	h.Connect(leaver, []string{"a", "b"})
	h.Connect(stayer, []string{"a"})

	h.Unsubscribe("u1", "a")

	h.Publish("a", []byte("after leaving"))
	if len(leaver.send) != 0 {
		t.Errorf("a departed member still receives: %d", len(leaver.send))
	}
	if len(stayer.send) != 1 {
		t.Errorf("remaining member stopped receiving: %d", len(stayer.send))
	}

	h.Publish("b", []byte("other channel"))
	if len(leaver.send) != 1 {
		t.Errorf("leaving one channel cost the subscriber another: %d", len(leaver.send))
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

// Tabs of one browser share a session and so its revocation; another device of
// the same user has a session of its own and must stay connected.
func TestCloseSessionDropsOnlyThatSessionsSockets(t *testing.T) {
	h := NewHub()
	tab1, tab2, phone := newSubscriber("u1"), newSubscriber("u1"), newSubscriber("u1")
	tab1.sessionID, tab2.sessionID, phone.sessionID = "laptop", "laptop", "phone"
	for _, c := range []*Subscriber{tab1, tab2, phone} {
		h.Connect(c, []string{"ch1"})
	}

	h.CloseSession("laptop")

	// CloseSession is synchronous, so a closed send is already readable; an open
	// one would block, and the default turns that into a failure, not a hang.
	for name, c := range map[string]*Subscriber{"tab1": tab1, "tab2": tab2} {
		select {
		case _, open := <-c.send:
			if open {
				t.Fatalf("%s: got a message instead of being closed", name)
			}
		default:
			t.Fatalf("%s: send is still open after its session was closed", name)
		}
	}
	h.Publish("ch1", []byte("hi"))
	if len(phone.send) != 1 {
		t.Fatalf("the other session's socket got %d messages, want 1", len(phone.send))
	}
}
