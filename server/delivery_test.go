package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCatchingUpListsWhatCameAfterOldestFirst(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)
	owner := person("owner")
	ch := createChannel(t, channels, "room", owner)
	other := createChannel(t, channels, "other", owner)

	var ids []bson.ObjectID
	for i := range 5 {
		m, err := messages.Insert(ctx, Message{ChannelID: ch.ID, Author: authorOf(owner), Text: string(rune('a' + i))})
		if err != nil {
			t.Fatalf("inserting: %v", err)
		}
		ids = append(ids, m.ID)
	}
	if _, err := messages.Insert(ctx, Message{ChannelID: other.ID, Author: authorOf(owner), Text: "elsewhere"}); err != nil {
		t.Fatalf("inserting: %v", err)
	}

	s := &server{channels: channels, messages: messages}
	list := func(query string) (int, []Message) {
		r := httptest.NewRequest(http.MethodGet, "/messages?channel_id="+ch.ID.Hex()+query, nil)
		w := httptest.NewRecorder()
		s.handleListMessages(w, r, owner)
		var got []Message
		json.Unmarshal(w.Body.Bytes(), &got)
		return w.Code, got
	}
	texts := func(ms []Message) string {
		var b strings.Builder
		for _, m := range ms {
			b.WriteString(m.Text)
		}
		return b.String()
	}

	if code, got := list("&after=" + ids[1].Hex()); code != http.StatusOK || texts(got) != "cde" {
		t.Fatalf("after the second: %d %q, want 200 \"cde\" oldest first and without the cursor", code, texts(got))
	}
	// Pages chain on the last id of the one before.
	code, page := list("&after=" + ids[1].Hex() + "&limit=2")
	if code != http.StatusOK || texts(page) != "cd" {
		t.Fatalf("first page: %d %q, want \"cd\"", code, texts(page))
	}
	if _, rest := list("&after=" + page[1].ID.Hex() + "&limit=2"); texts(rest) != "e" {
		t.Fatalf("second page: %q, want \"e\"", texts(rest))
	}
	if _, none := list("&after=" + ids[4].Hex()); len(none) != 0 {
		t.Fatalf("after the newest: %q, want nothing", texts(none))
	}

	for _, bad := range []string{
		"&after=" + ids[1].Hex() + "&before=" + ids[3].Hex(),
		"&after=" + ids[1].Hex() + "&ids=" + ids[2].Hex(),
		"&after=nonsense",
	} {
		if code, _ := list(bad); code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", bad, code)
		}
	}
}

// Settled may let a caller go only once every subscription written to Redis is
// confirmed: a publish in the gap would pass the node by.
func TestSettledWaitsForEveryConfirmation(t *testing.T) {
	b := &bus{hub: NewHub(), unconfirmed: 2}
	done := make(chan struct{})
	b.held = []chan struct{}{done}

	closed := func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}

	b.confirmed(&redis.Subscription{Kind: "unsubscribe", Channel: channelTopic + "x"})
	b.confirmed(&redis.Subscription{Kind: "subscribe", Channel: channelTopic + "a"})
	if closed() {
		t.Fatalf("released with one confirmation still outstanding")
	}
	b.confirmed(&redis.Subscription{Kind: "subscribe", Channel: channelTopic + "b"})
	if !closed() {
		t.Fatalf("not released once every confirmation was in")
	}
}

// The same, end to end with Redis but without Run, so that nothing races the
// test: a watch written to Redis holds a waiter until its confirmation is read.
func TestAWatchIsCountedUntilRedisConfirmsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Redis, skipped under -short")
	}
	ctx := context.Background()
	b, err := newBusWith(ctx, NewHub(), redisOptions())
	if err != nil {
		t.Skipf("no Redis: %v", err)
	}
	t.Cleanup(func() { b.sub.Close(); b.rdb.Close() })

	done := make(chan struct{})
	b.waiters = []chan struct{}{done}
	b.pending = map[string]bool{"counted": true}
	b.applyWatches(ctx)

	select {
	case <-done:
		t.Fatal("a waiter was let go before Redis confirmed the watch")
	default:
	}
	msg, err := b.sub.Receive(ctx)
	if err != nil {
		t.Fatalf("reading the confirmation: %v", err)
	}
	sub, ok := msg.(*redis.Subscription)
	if !ok {
		t.Fatalf("got %T, want a subscription confirmation", msg)
	}
	b.confirmed(sub)
	select {
	case <-done:
	default:
		t.Fatal("the confirmation did not let the waiter go")
	}
}

// busWithName is testInstance with connections Redis can tell apart, so the
// test can cut exactly this bus's subscription connection.
func busWithName(t *testing.T) (*Hub, *bus, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Redis, skipped under -short")
	}
	ctx, cancel := context.WithCancel(context.Background())
	hub := NewHub()
	name := "bus-test-" + strings.ToLower(rand.Text()[:8])
	opts := redisOptions()
	opts.ClientName = name
	b, err := newBusWith(ctx, hub, opts)
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
	return hub, b, name
}

func settle(t *testing.T, p publisher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Settled(ctx); err != nil {
		t.Fatalf("settling: %v", err)
	}
}

func TestASettledChannelHearsTheNextPublish(t *testing.T) {
	hub, b, _ := busWithName(t)
	reader := newSubscriber("u1")
	hub.Connect(reader, []string{"fresh"})
	settle(t, b)

	// No wait for subscribers: Settled is the wait.
	if err := b.rdb.Publish(context.Background(), channelTopic+"fresh", "right after").Err(); err != nil {
		t.Fatalf("publishing: %v", err)
	}
	select {
	case got := <-reader.send:
		if string(got) != "right after" {
			t.Fatalf("received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a publish right after Settled was lost")
	}
}

// When go-redis loses the subscription connection it reconnects and subscribes
// again on its own; what was published in between is gone, so the sockets are
// told to catch up. An ordinary subscription is not a reconnection.
func TestAReconnectedBusTellsItsSocketsToCatchUp(t *testing.T) {
	hub, b, name := busWithName(t)
	ctx := context.Background()

	reader := newSubscriber("u1")
	hub.Connect(reader, []string{"c1"})
	settle(t, b)
	hub.Connect(newSubscriber("u2"), []string{"c2"})
	settle(t, b)
	if n := len(reader.send); n != 0 {
		t.Fatalf("ordinary subscriptions sent %d frames", n)
	}

	list, err := b.rdb.ClientList(ctx).Result()
	if err != nil {
		t.Fatalf("listing clients: %v", err)
	}
	var killed int
	for _, line := range strings.Split(list, "\n") {
		if !strings.Contains(line, "name="+name+" ") || strings.Contains(line, " sub=0 ") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if id, ok := strings.CutPrefix(field, "id="); ok {
				if err := b.rdb.Do(ctx, "CLIENT", "KILL", "ID", id).Err(); err != nil {
					t.Fatalf("killing %s: %v", id, err)
				}
				killed++
			}
		}
	}
	if killed != 1 {
		t.Fatalf("killed %d subscription connections, want 1", killed)
	}

	select {
	case got := <-reader.send:
		if string(got) != string(resyncFrame) {
			t.Fatalf("received %q, want the resync frame", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no resync after the subscription connection was cut")
	}

	// And the channel is heard again afterwards.
	settle(t, b)
	if err := b.rdb.Publish(ctx, channelTopic+"c1", "after the cut").Err(); err != nil {
		t.Fatalf("publishing: %v", err)
	}
	select {
	case got := <-reader.send:
		if string(got) != "after the cut" {
			t.Fatalf("received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the channel stayed deaf after the reconnection")
	}
}

func TestASocketIsToldWhenItCanMissNothing(t *testing.T) {
	db := testDB(t)
	h := NewHub()
	s := &server{sessions: newSessionStore(db), channels: newChannelStore(db), hub: h, bus: h}
	_, url, header, _ := serveOneSocket(t, s)

	conn := dialWith(t, url, header)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	kind, frame, err := conn.ReadMessage()
	if err != nil || kind != websocket.TextMessage || string(frame) != string(readyFrame) {
		t.Fatalf("first frame: %v %q, want the ready frame", err, frame)
	}
}

func TestSendingToAGoneSocketIsHarmless(t *testing.T) {
	h := NewHub()
	c := newSubscriber("u1")
	h.Connect(c, nil)
	h.Disconnect(c)
	h.Send(c, readyFrame)
	h.SendAll(resyncFrame)
}
