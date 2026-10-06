package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Sends racing each other in one channel still get one number each, and the
// channel's last number is how many there were.
func TestRacingSendsGetDistinctNumbers(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)
	owner := person("owner")
	ch := createChannel(t, channels, "room", owner)
	h := NewHub()
	s := &server{channels: channels, messages: messages, hub: h, bus: h}

	const n = 20
	seqs := make([]int64, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, body := callMessageHandler(t, s.handleSendMessage, "", map[string]string{
				"channel_id": ch.ID.Hex(), "text": "x", "client_msg_id": bson.NewObjectID().Hex(),
			}, owner)
			var m Message
			json.Unmarshal(body, &m)
			seqs[i] = m.Seq
		})
	}
	wg.Wait()

	seen := map[int64]bool{}
	for _, seq := range seqs {
		if seq < 1 || seq > n || seen[seq] {
			t.Fatalf("numbers %v are not 1..%d each once", seqs, n)
		}
		seen[seq] = true
	}
	got, err := channels.ByID(ctx, ch.ID)
	if err != nil || got.LastSeq != n {
		t.Fatalf("last number %d (%v), want %d", got.LastSeq, err, n)
	}
}

func TestOldMessagesAreNumberedInTheOrderTheyWereWritten(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)
	owner := person("owner")
	a := createChannel(t, channels, "a", owner)
	b := createChannel(t, channels, "b", owner)
	for _, ch := range []Channel{a, b, a, a, b} {
		if _, err := messages.Insert(ctx, Message{ChannelID: ch.ID, Author: authorOf(owner), Text: "old"}); err != nil {
			t.Fatalf("inserting: %v", err)
		}
	}

	for range 2 { // the second run finds nothing to do
		if err := messages.numberOldMessages(ctx, channels); err != nil {
			t.Fatalf("numbering: %v", err)
		}
	}
	for _, c := range []struct {
		ch   Channel
		want []int64
	}{{a, []int64{1, 2, 3}}, {b, []int64{1, 2}}} {
		list, err := messages.ListAfter(ctx, c.ch.ID, 0, 0)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		var got []int64
		for i, m := range list {
			got = append(got, m.Seq)
			if i > 0 && m.ID.Hex() < list[i-1].ID.Hex() {
				t.Fatalf("channel %s numbered out of write order", c.ch.Name)
			}
		}
		if len(got) != len(c.want) || got[len(got)-1] != c.want[len(c.want)-1] {
			t.Fatalf("channel %s numbers %v, want %v", c.ch.Name, got, c.want)
		}
		stored, _ := channels.ByID(ctx, c.ch.ID)
		if stored.LastSeq != c.want[len(c.want)-1] {
			t.Fatalf("channel %s last number %d", c.ch.Name, stored.LastSeq)
		}
	}
}

func TestReadPositions(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)
	reads := newReadStore(db)
	if err := reads.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	owner, member := person("owner"), person("member")
	ch := createChannel(t, channels, "room", owner)
	if err := channels.AddMember(ctx, ch.ID, member); err != nil {
		t.Fatalf("joining: %v", err)
	}
	h := NewHub()
	s := &server{channels: channels, messages: messages, reads: reads, hub: h, bus: h}

	for i := range 3 {
		callMessageHandler(t, s.handleSendMessage, "", map[string]string{
			"channel_id": ch.ID.Hex(), "text": "hi", "client_msg_id": string(rune('a' + i)),
		}, owner)
	}
	readSeq := func(who Session) (int64, int64) {
		t.Helper()
		list, err := channels.ForUser(ctx, who.UserID, bson.ObjectID{}, 0)
		if err != nil || len(list) != 1 {
			t.Fatalf("listing: %v %+v", err, list)
		}
		return list[0].LastSeq, list[0].ReadSeq
	}
	mark := func(who Session, seq int64) int {
		t.Helper()
		body, _ := json.Marshal(markReadRequest{Seq: seq})
		r := httptest.NewRequest(http.MethodPost, "/channels/"+ch.ID.Hex()+"/read", strings.NewReader(string(body)))
		r.SetPathValue("id", ch.ID.Hex())
		w := httptest.NewRecorder()
		s.handleMarkRead(w, r, who)
		return w.Code
	}

	// What you wrote is read; what others wrote is not until you say so.
	if last, read := readSeq(owner); last != 3 || read != 3 {
		t.Fatalf("the author: last %d read %d, want 3 and 3", last, read)
	}
	if last, read := readSeq(member); last != 3 || read != 0 {
		t.Fatalf("the member: last %d read %d, want 3 and 0", last, read)
	}
	mark(member, 2)
	if _, read := readSeq(member); read != 2 {
		t.Fatalf("after marking 2: read %d", read)
	}
	mark(member, 1) // a late request from another tab
	if _, read := readSeq(member); read != 2 {
		t.Fatalf("a read position went back to %d", read)
	}
	mark(member, 99) // ahead of what exists
	if _, read := readSeq(member); read != 3 {
		t.Fatalf("a read position went to %d, past the last message", read)
	}
	if code := mark(person("stranger"), 1); code != http.StatusNotFound {
		t.Fatalf("an outsider marking got %d, want 404", code)
	}
}

// A publish that fails is remembered, and the next one that goes through tells
// every node to resync.
func TestALostPublishTellsEveryNodeToResync(t *testing.T) {
	broken := &bus{rdb: redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})}
	t.Cleanup(func() { broken.rdb.Close() })
	broken.Publish("c1", []byte("lost"))
	if !broken.lost.Load() {
		t.Fatalf("a failed publish was not remembered")
	}

	_, busA, _ := busWithName(t)
	hubB, busB, _ := busWithName(t)
	settle(t, busA)
	settle(t, busB)
	reader := newSubscriber("u1")
	hubB.Connect(reader, nil)
	settle(t, busB)

	busA.lost.Store(true)
	busA.Publish("anything", []byte("goes through"))
	if busA.lost.Load() {
		t.Fatalf("the loss is still marked after it was announced")
	}
	select {
	case got := <-reader.send:
		if string(got) != string(resyncFrame) {
			t.Fatalf("received %q, want the resync frame", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the other node was not told to resync")
	}
}

func TestAStartingNodeTellsEveryNodeToResync(t *testing.T) {
	_, busA, _ := busWithName(t)
	hubB, busB, _ := busWithName(t)
	reader := newSubscriber("u1")
	hubB.Connect(reader, nil)
	settle(t, busB)

	busA.ResyncAll()
	select {
	case got := <-reader.send:
		if string(got) != string(resyncFrame) {
			t.Fatalf("received %q, want the resync frame", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a starting node did not reach the others")
	}
}

// After a gap a node rebuilds what it learned from the bus: a join it never
// heard of, a leave it never heard of, a revocation it never heard of.
func TestRecoveringFromAGapRebuildsRoutingAndSessions(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels := newChannelStore(db)
	sessions := newSessionStore(db)
	if err := sessions.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	h := NewHub()
	s := &server{hub: h, bus: h, channels: channels, sessions: sessions}

	alice := &User{ID: bson.NewObjectID(), Username: "alice"}
	joined := createChannel(t, channels, "joined", Session{UserID: alice.ID, Username: "alice"})
	left := bson.NewObjectID().Hex()

	live, err := sessions.Create(ctx, alice, "live")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	gone, err := sessions.Create(ctx, alice, "gone")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// Revoked on another node: the document is gone, the event never came.
	if _, err := sessions.col.DeleteOne(ctx, bson.M{"_id": gone.ID}); err != nil {
		t.Fatalf("deleting: %v", err)
	}

	keep, drop := newSubscriber(alice.ID.Hex()), newSubscriber(alice.ID.Hex())
	keep.sessionID, drop.sessionID = live.ID.Hex(), gone.ID.Hex()
	h.Connect(keep, []string{left})
	h.Connect(drop, []string{left})

	s.recoverFromGap()

	if !h.Reads(keep, joined.ID.Hex()) || h.Reads(keep, left) {
		t.Fatalf("routing after recovery: reads joined %v, reads left %v", h.Reads(keep, joined.ID.Hex()), h.Reads(keep, left))
	}
	var frames []string
	for len(keep.send) > 0 {
		frames = append(frames, string(<-keep.send))
	}
	if len(frames) == 0 || frames[len(frames)-1] != string(resyncFrame) {
		t.Fatalf("the live socket got %v, want the resync frame", frames)
	}
	if !drop.dropped || drop.closeCode != closeSessionEnded {
		t.Fatalf("the socket of a revoked session was not closed with %d", closeSessionEnded)
	}
}

// Joining starts with nothing unread: the history before is not news.
func TestJoiningStartsWithNothingUnread(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)
	reads := newReadStore(db)
	if err := reads.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	owner, joiner := person("owner"), person("joiner")
	ch := createChannel(t, channels, "room", owner)
	h := NewHub()
	s := &server{channels: channels, messages: messages, reads: reads, hub: h, bus: h}
	for i := range 3 {
		callMessageHandler(t, s.handleSendMessage, "", map[string]string{
			"channel_id": ch.ID.Hex(), "text": "before", "client_msg_id": string(rune('a' + i)),
		}, owner)
	}

	if code, body := followInvite(t, s, ch.InviteCode, joiner); code != http.StatusOK {
		t.Fatalf("joining: %d %+v", code, body)
	}
	list, err := channels.ForUser(ctx, joiner.UserID, bson.ObjectID{}, 0)
	if err != nil || len(list) != 1 || list[0].LastSeq != 3 || list[0].ReadSeq != 3 {
		t.Fatalf("after joining: %v %+v, want last 3 and read 3", err, list)
	}
}
