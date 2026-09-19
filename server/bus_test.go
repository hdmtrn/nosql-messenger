package main

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
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
func waitForSubscribers(t *testing.T, b *bus, topic string, want int64) {
	t.Helper()

	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for {
		counts, err := b.rdb.PubSubNumSub(ctx, topic).Result()
		if err != nil {
			t.Fatalf("asking Redis who is subscribed: %v", err)
		}
		if counts[topic] == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("topic %s has %d subscribers, want %d",
				topic, counts[topic], want)
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
	waitForSubscribers(t, busB, channelTopic+"c1", 1)

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
	waitForSubscribers(t, busA, channelTopic+"c1", 2)

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
	waitForSubscribers(t, busB, channelTopic+"c1", 1)

	// The last reader leaves: the instance must stop listening, otherwise every
	// node would carry the traffic of every channel.
	hubB.Disconnect(reader)
	waitForSubscribers(t, busB, channelTopic+"c1", 0)

	busA.Publish("c1", []byte("nobody is here"))

	// A hub with no subscriber cannot show the message was dropped, so the
	// check is on the other side: Redis has nobody to deliver it to.
	silent := newSubscriber("u2")
	hubA.Connect(silent, []string{"c2"})
	waitForSubscribers(t, busA, channelTopic+"c2", 1)
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

// Revoking a session is the reason server-side sessions were chosen over JWT.
// Each instance caches sessions in its own memory for cacheTTL, so without an
// announcement the revoked session would keep working on the other node for up
// to ten minutes — the very property we paid a database lookup for would be
// gone as soon as a second instance started.
func TestRevokingASessionClosesItOnTheOtherInstance(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	_, busA := testInstance(t)
	_, busB := testInstance(t)

	// Two stores over one database are two instances: separate caches, shared
	// collection, as two processes would have.
	storeA, storeB := newSessionStore(db), newSessionStore(db)
	storeA.onRevoked = func(id bson.ObjectID) { busA.PublishRevoked(id.Hex()) }
	busB.onSessionRevoked = func(id string) {
		oid, err := bson.ObjectIDFromHex(id)
		if err != nil {
			t.Errorf("unreadable session id %q", id)
			return
		}
		storeB.evictByID(oid)
	}

	user := &User{ID: bson.NewObjectID(), Username: "alice"}
	sess, err := storeA.Create(ctx, user, "a browser")
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}

	// B must have it cached, otherwise the test would pass even with no event:
	// a lookup in MongoDB alone already refuses a deleted session.
	if _, err := storeB.ByToken(ctx, sess.Token); err != nil {
		t.Fatalf("the second instance did not accept the session: %v", err)
	}

	if err := storeA.Revoke(ctx, user.ID, sess.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := storeB.ByToken(ctx, sess.Token); err != nil {
			return // the second instance no longer accepts the token
		}
		if time.Now().After(deadline) {
			t.Fatal("the revoked session still works on the second instance")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A join is handled by whichever node got the request, while the joining user's
// socket may sit on another. Without the announcement only the handling node
// would route the socket to the channel, and the user would see nothing from it
// until a reload.
func TestSubscribingReachesASocketOnTheOtherInstance(t *testing.T) {
	_, busA := testInstance(t)
	hubB, busB := testInstance(t)

	bob := newSubscriber("bob")
	hubB.Connect(bob, nil)

	// The request lands on A, which holds none of Bob's sockets. B listening on
	// the channel's topic is proof that the change reached it; counting on Redis
	// is enough, since A has no reader of its own to subscribe for.
	busA.Subscribe("bob", "c-join")
	waitForSubscribers(t, busB, channelTopic+"c-join", 1)

	busA.Publish("c-join", []byte("welcome"))
	select {
	case got := <-bob.send:
		if string(got) != "welcome" {
			t.Fatalf("received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the socket on the other instance never received the channel")
	}

	busA.Unsubscribe("bob", "c-join")
	waitForSubscribers(t, busB, channelTopic+"c-join", 0)
}
