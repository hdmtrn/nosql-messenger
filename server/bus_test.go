package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// Same rule as testDB: without Redis the test is missing a prerequisite, but in
// CI that is a broken build rather than a reason to skip.
func testInstance(t *testing.T) (*Hub, *bus) {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test: needs Redis, skipped under -short")
	}

	ctx, cancel := context.WithCancel(context.Background())
	hub := NewHub()

	b, err := newBus(ctx, hub)
	if err != nil {
		cancel()
		if os.Getenv("CI") != "" {
			t.Fatalf("no Redis at %s: %v", redisAddr(), err)
		}
		t.Skipf("no Redis at %s: %v", redisAddr(), err)
	}
	hub.watch, hub.unwatch = b.Watch, b.Unwatch
	go b.Run(ctx)

	t.Cleanup(func() {
		cancel()
		b.rdb.Close()
	})
	return hub, b
}

// Subscribing happens in the bus goroutine, so a publish sent too early reaches
// nobody. Redis itself answers who is listening, which is a more honest wait
// than a sleep.
func waitForSubscribers(t *testing.T, b *bus, chID string, want int64) {
	t.Helper()

	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for {
		counts, err := b.rdb.PubSubNumSub(ctx, channelTopic+chID).Result()
		if err != nil {
			t.Fatalf("asking Redis who is subscribed: %v", err)
		}
		if counts[channelTopic+chID] == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("channel %s has %d subscribers, want %d",
				chID, counts[channelTopic+chID], want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMessageReachesTheOtherInstance(t *testing.T) {
	hubA, busA := testInstance(t)
	hubB, busB := testInstance(t)

	// Nobody is connected to A, so without the bus the message would go nowhere.
	reader := newSubscriber("u1")
	hubB.Connect(reader, []string{"c1"})
	waitForSubscribers(t, busB, "c1", 1)

	busA.Publish("c1", []byte("from the other instance"))

	select {
	case got := <-reader.send:
		if string(got) != "from the other instance" {
			t.Fatalf("received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the message never crossed to the second instance")
	}

	// The sender's own instance delivers the same way, through Redis, so both
	// sides see one order of messages.
	writer := newSubscriber("u2")
	hubA.Connect(writer, []string{"c1"})
	waitForSubscribers(t, busA, "c1", 2)

	busA.Publish("c1", []byte("second"))
	for _, s := range []*Subscriber{reader, writer} {
		select {
		case got := <-s.send:
			if string(got) != "second" {
				t.Fatalf("received %q", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a subscriber missed the second message")
		}
	}
}

func TestInstanceListensOnlyToChannelsItHasReadersFor(t *testing.T) {
	hubA, busA := testInstance(t)
	hubB, busB := testInstance(t)

	reader := newSubscriber("u1")
	hubB.Connect(reader, []string{"c1"})
	waitForSubscribers(t, busB, "c1", 1)

	// The last reader leaves: the instance must stop listening, otherwise every
	// node would carry the traffic of every channel.
	hubB.Disconnect(reader)
	waitForSubscribers(t, busB, "c1", 0)

	busA.Publish("c1", []byte("nobody is here"))

	// A hub with no subscriber cannot show the message was dropped, so the
	// check is on the other side: Redis has nobody to deliver it to.
	silent := newSubscriber("u2")
	hubA.Connect(silent, []string{"c2"})
	waitForSubscribers(t, busA, "c2", 1)
	busA.Publish("c2", []byte("still working"))

	select {
	case got := <-silent.send:
		if string(got) != "still working" {
			t.Fatalf("received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the bus stopped working after an unsubscribe")
	}
}
