package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestForwardOfKeepsTheOriginalSource(t *testing.T) {
	alice := Message{
		ID:        bson.NewObjectID(),
		ChannelID: bson.NewObjectID(),
		Author:    MessageAuthor{ID: bson.NewObjectID(), Username: "alice"},
		Text:      "hi",
		CreatedAt: time.Now(),
	}
	first := alice.forwardOf()
	if first.MessageID != alice.ID || first.Author.Username != "alice" {
		t.Fatalf("forward of an original points at %s by %s, want %s by alice",
			first.MessageID.Hex(), first.Author.Username, alice.ID.Hex())
	}

	bob := Message{
		ID:        bson.NewObjectID(),
		ChannelID: bson.NewObjectID(),
		Author:    MessageAuthor{ID: bson.NewObjectID(), Username: "bob"},
		Text:      alice.Text,
		Forwarded: first,
	}
	second := bob.forwardOf()
	if second.MessageID != alice.ID || second.Author.Username != "alice" {
		t.Fatalf("forward of a forward points at %s by %s, want %s by alice",
			second.MessageID.Hex(), second.Author.Username, alice.ID.Hex())
	}
}

func TestForwardReachesOnlyMessagesYouCanSee(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)

	alice, mallory := person("alice"), person("mallory")
	aliceRoom := createChannel(t, channels, "alice-room", alice)
	aliceNotes := createChannel(t, channels, "alice-notes", alice)
	malloryRoom := createChannel(t, channels, "mallory-room", mallory)

	orig, err := messages.Insert(ctx, Message{ChannelID: aliceRoom.ID, Author: authorOf(alice), Text: "the plan"})
	if err != nil {
		t.Fatalf("inserting message: %v", err)
	}

	h := NewHub()
	s := &server{channels: channels, messages: messages, hub: h, bus: h}

	// The allowed case first: without it a handler that always answers 404
	// would pass everything below.
	code, body := callMessageHandler(t, s.handleForwardMessage, orig.ID.Hex(),
		map[string]string{"channel_id": aliceNotes.ID.Hex()}, alice)
	if code != http.StatusCreated {
		t.Fatalf("forwarding own message: got %d %s, want 201", code, body)
	}
	var copied Message
	if err := json.Unmarshal(body, &copied); err != nil {
		t.Fatalf("decoding forwarded message: %v", err)
	}
	if copied.Text != orig.Text || copied.Forwarded == nil || copied.Forwarded.MessageID != orig.ID {
		t.Fatalf("forwarded copy is %+v, want text %q pointing at %s", copied, orig.Text, orig.ID.Hex())
	}

	// A message in someone else's channel must look exactly like one that does not exist.
	foreign, foreignBody := callMessageHandler(t, s.handleForwardMessage, orig.ID.Hex(),
		map[string]string{"channel_id": malloryRoom.ID.Hex()}, mallory)
	invented, inventedBody := callMessageHandler(t, s.handleForwardMessage, bson.NewObjectID().Hex(),
		map[string]string{"channel_id": malloryRoom.ID.Hex()}, mallory)
	if foreign != http.StatusNotFound || invented != http.StatusNotFound {
		t.Fatalf("foreign message gave %d, invented gave %d, want 404 for both", foreign, invented)
	}
	if !bytes.Equal(foreignBody, inventedBody) {
		t.Fatalf("foreign message answers %s, missing one %s: they must not differ", foreignBody, inventedBody)
	}

	leaked, err := messages.List(ctx, malloryRoom.ID, bson.ObjectID{}, 0)
	if err != nil {
		t.Fatalf("listing messages: %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("mallory's room holds %d messages after a refused forward, want 0", len(leaked))
	}
}

func TestRepeatedClientMsgIDIsNotBroadcastAgain(t *testing.T) {
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)

	owner := person("owner")
	ch := createChannel(t, channels, "room", owner)

	h := NewHub()
	s := &server{channels: channels, messages: messages, hub: h, bus: h}
	// No writePump reads this subscriber, so every broadcast stays in its buffer
	// and len(send) counts them.
	watcher := newSubscriber("watcher")
	s.hub.Connect(watcher, []string{ch.ID.Hex()})

	send := func() (int, Message) {
		code, body := callMessageHandler(t, s.handleSendMessage, "", map[string]string{
			"channel_id":    ch.ID.Hex(),
			"text":          "hello",
			"client_msg_id": "c1",
		}, owner)
		var msg Message
		if err := json.Unmarshal(body, &msg); err != nil {
			t.Fatalf("decoding reply %s: %v", body, err)
		}
		return code, msg
	}

	firstCode, first := send()
	secondCode, second := send()
	if firstCode != http.StatusCreated || secondCode != http.StatusOK {
		t.Fatalf("first send gave %d, repeat gave %d, want 201 then 200", firstCode, secondCode)
	}
	if first.ID != second.ID {
		t.Fatalf("repeat returned message %s, want the stored %s", second.ID.Hex(), first.ID.Hex())
	}
	if n := len(watcher.send); n != 1 {
		t.Fatalf("channel saw %d broadcasts, want 1", n)
	}
}

func TestReplyStaysInItsChannel(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)

	alice, mallory := person("alice"), person("mallory")
	aliceRoom := createChannel(t, channels, "alice-room", alice)
	aliceNotes := createChannel(t, channels, "alice-notes", alice)
	malloryRoom := createChannel(t, channels, "mallory-room", mallory)

	orig, err := messages.Insert(ctx, Message{ChannelID: aliceRoom.ID, Author: authorOf(alice), Text: "the plan"})
	if err != nil {
		t.Fatalf("inserting message: %v", err)
	}

	h := NewHub()
	s := &server{channels: channels, messages: messages, hub: h, bus: h}
	reply := func(sess Session, channelID bson.ObjectID, replyTo string) (int, []byte) {
		return callMessageHandler(t, s.handleSendMessage, "", map[string]string{
			"channel_id": channelID.Hex(),
			"text":       "agreed",
			"reply_to":   replyTo,
		}, sess)
	}

	// The allowed case first: without it a handler that always answers 404
	// would pass everything below.
	code, body := reply(alice, aliceRoom.ID, orig.ID.Hex())
	if code != http.StatusCreated {
		t.Fatalf("replying in the same channel: got %d %s, want 201", code, body)
	}
	var sent Message
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decoding reply: %v", err)
	}
	stored, err := messages.ByID(ctx, sent.ID)
	if err != nil {
		t.Fatalf("loading stored reply: %v", err)
	}
	if stored.ReplyTo == nil || *stored.ReplyTo != orig.ID {
		t.Fatalf("stored reply points at %v, want %s", stored.ReplyTo, orig.ID.Hex())
	}

	// Alice can read the original, but a reply in another channel would show its
	// text there. That, a foreign message and a missing one must all look the same.
	otherChannel, otherBody := reply(alice, aliceNotes.ID, orig.ID.Hex())
	foreign, foreignBody := reply(mallory, malloryRoom.ID, orig.ID.Hex())
	invented, inventedBody := reply(mallory, malloryRoom.ID, bson.NewObjectID().Hex())
	if otherChannel != http.StatusNotFound || foreign != http.StatusNotFound || invented != http.StatusNotFound {
		t.Fatalf("other channel gave %d, foreign %d, invented %d, want 404 for all",
			otherChannel, foreign, invented)
	}
	if !bytes.Equal(otherBody, inventedBody) || !bytes.Equal(foreignBody, inventedBody) {
		t.Fatalf("answers differ: other channel %s, foreign %s, missing %s", otherBody, foreignBody, inventedBody)
	}

	for _, ch := range []Channel{aliceNotes, malloryRoom} {
		leaked, err := messages.List(ctx, ch.ID, bson.ObjectID{}, 0)
		if err != nil {
			t.Fatalf("listing messages: %v", err)
		}
		if len(leaked) != 0 {
			t.Fatalf("%s holds %d messages after refused replies, want 0", ch.Name, len(leaked))
		}
	}
}

// A plain message must not grow an empty reply_to: old documents lack the field,
// and the client tells a reply apart by the field being there at all.
func TestPlainMessageHasNoReplyTo(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)

	owner := person("owner")
	ch := createChannel(t, channels, "room", owner)
	h := NewHub()
	s := &server{channels: channels, messages: messages, hub: h, bus: h}

	code, body := callMessageHandler(t, s.handleSendMessage, "", map[string]string{
		"channel_id": ch.ID.Hex(),
		"text":       "hello",
	}, owner)
	if code != http.StatusCreated {
		t.Fatalf("sending: got %d %s, want 201", code, body)
	}
	if bytes.Contains(body, []byte(`"reply_to"`)) {
		t.Fatalf("answer %s carries reply_to", body)
	}

	var sent Message
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decoding answer: %v", err)
	}
	raw, err := db.Collection("messages").FindOne(ctx, bson.M{"_id": sent.ID}).Raw()
	if err != nil {
		t.Fatalf("loading raw document: %v", err)
	}
	if _, err := raw.LookupErr("reply_to"); err == nil {
		t.Fatalf("stored document %s has a reply_to field", raw)
	}
}

func TestMessagesByIDStayInTheAskedChannel(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)

	alice, mallory := person("alice"), person("mallory")
	aliceRoom := createChannel(t, channels, "alice-room", alice)
	aliceNotes := createChannel(t, channels, "alice-notes", alice)
	malloryRoom := createChannel(t, channels, "mallory-room", mallory)

	insert := func(ch Channel, sess Session, text string) Message {
		m, err := messages.Insert(ctx, Message{ChannelID: ch.ID, Author: authorOf(sess), Text: text})
		if err != nil {
			t.Fatalf("inserting message: %v", err)
		}
		return m
	}
	inRoom := insert(aliceRoom, alice, "in the room")
	insert(aliceRoom, alice, "not asked for")
	inNotes := insert(aliceNotes, alice, "in the notes")

	h := NewHub()
	s := &server{channels: channels, messages: messages, hub: h, bus: h}
	lookup := func(sess Session, query string) (int, []Message) {
		r := httptest.NewRequest(http.MethodGet, "/messages?"+query, nil)
		w := httptest.NewRecorder()
		s.handleListMessages(w, r, sess)
		var got []Message
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding %s: %v", w.Body.Bytes(), err)
			}
		}
		return w.Code, got
	}
	ids := func(ms ...string) string { return strings.Join(ms, ",") }

	// Only the listed message of the asked channel comes back. Alice can read her
	// notes too, but a notes id asked for under the room is not the room's.
	code, got := lookup(alice, "channel_id="+aliceRoom.ID.Hex()+"&ids="+
		ids(inRoom.ID.Hex(), inNotes.ID.Hex(), bson.NewObjectID().Hex()))
	if code != http.StatusOK || len(got) != 1 || got[0].ID != inRoom.ID {
		t.Fatalf("lookup in alice-room gave %d %+v, want only %s", code, got, inRoom.ID.Hex())
	}

	// Naming her own channel does not let mallory read alice's message.
	code, got = lookup(mallory, "channel_id="+malloryRoom.ID.Hex()+"&ids="+inRoom.ID.Hex())
	if code != http.StatusOK || len(got) != 0 {
		t.Fatalf("mallory's lookup gave %d %+v, want 200 and nothing", code, got)
	}
	if code, _ := lookup(mallory, "channel_id="+aliceRoom.ID.Hex()+"&ids="+inRoom.ID.Hex()); code != http.StatusNotFound {
		t.Fatalf("lookup in a channel mallory is not in gave %d, want 404", code)
	}

	if code, _ := lookup(alice, "channel_id="+aliceRoom.ID.Hex()+"&ids="+inRoom.ID.Hex()+"&limit=5"); code != http.StatusBadRequest {
		t.Fatalf("ids with limit gave %d, want 400", code)
	}

	many := make([]string, messagesMaxLimit+1)
	for i := range many {
		many[i] = inRoom.ID.Hex()
	}
	if code, _ := lookup(alice, "channel_id="+aliceRoom.ID.Hex()+"&ids="+ids(many...)); code != http.StatusBadRequest {
		t.Fatalf("%d ids gave %d, want 400", len(many), code)
	}
}

// The other tests call handlers directly, past the router. This one goes through
// it, so a typo in the pattern fails here instead of at the first real request.
func TestForwardRouteIsRegistered(t *testing.T) {
	// An empty token is refused before the session store touches the database.
	s := &server{sessions: &sessionStore{cache: make(map[string]cachedSession)}}
	id := bson.NewObjectID().Hex()

	r := httptest.NewRequest(http.MethodPost, "/messages/"+id+"/forward", nil)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)

	if r.Pattern != "POST /messages/{id}/forward" {
		t.Fatalf("request matched pattern %q, want the forward route", r.Pattern)
	}
	if r.PathValue("id") != id {
		t.Fatalf("path value id is %q, want %q", r.PathValue("id"), id)
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous forward got %d, want 401 from requireAuth", w.Code)
	}
}

func newMessageTestStores(t *testing.T, db *mongo.Database) (*channelStore, *messageStore) {
	t.Helper()

	channels, messages := newChannelStore(db), newMessageStore(db)
	if err := channels.ensureIndexes(context.Background()); err != nil {
		t.Fatalf("channel indexes: %v", err)
	}
	// The unique client_msg_id index is what turns a repeat into a duplicate.
	if err := messages.ensureIndexes(context.Background()); err != nil {
		t.Fatalf("message indexes: %v", err)
	}
	return channels, messages
}

func createChannel(t *testing.T, channels *channelStore, name string, owner Session) Channel {
	t.Helper()

	ch, err := channels.Create(context.Background(), name, owner)
	if err != nil {
		t.Fatalf("creating channel %s: %v", name, err)
	}
	return ch
}

// callMessageHandler drives a message handler the way callInvite drives the
// invite ones: no router, no login, the path value set by hand.
func callMessageHandler(
	t *testing.T,
	h func(http.ResponseWriter, *http.Request, Session),
	pathID string,
	body map[string]string,
	sess Session,
) (int, []byte) {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(raw))
	if pathID != "" {
		r.SetPathValue("id", pathID)
	}
	w := httptest.NewRecorder()

	h(w, r, sess)
	return w.Code, w.Body.Bytes()
}

// client_msg_id is an id the client makes up so a retry does not store the
// message twice. It is not a key anyone may read by: the unique index used to
// be global, so sending with an id somebody else had already used answered with
// their message, out of a channel the sender is not in.
func TestClientMsgIDIsScopedToItsChannel(t *testing.T) {
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)

	alice, bob := person("alice"), person("bob")
	hers := createChannel(t, channels, "hers", alice)
	his := createChannel(t, channels, "his", bob)

	h := NewHub()
	s := &server{channels: channels, messages: messages, hub: h, bus: h}

	send := func(sess Session, ch Channel, text string) (int, Message) {
		code, body := callMessageHandler(t, s.handleSendMessage, "", map[string]string{
			"channel_id":    ch.ID.Hex(),
			"text":          text,
			"client_msg_id": "the-same-on-both",
		}, sess)
		var msg Message
		if err := json.Unmarshal(body, &msg); err != nil {
			t.Fatalf("decoding reply %s: %v", body, err)
		}
		return code, msg
	}

	if code, _ := send(alice, hers, "something private"); code != http.StatusCreated {
		t.Fatalf("alice's send gave %d, want 201", code)
	}

	code, got := send(bob, his, "bob's own text")
	if code != http.StatusCreated {
		t.Fatalf("bob's send gave %d, want 201", code)
	}
	if got.ChannelID != his.ID {
		t.Fatalf("bob was answered with a message from channel %s, want his own %s",
			got.ChannelID.Hex(), his.ID.Hex())
	}
	if got.Text != "bob's own text" {
		t.Fatalf("bob was answered with the text %q, want his own", got.Text)
	}
}

// Messages sent without a client_msg_id must not collide with one another. The
// index is partial rather than sparse for exactly this: a sparse compound index
// takes any document holding one of its keys, and every message has a
// channel_id, so all of them would index as {channel, null} and the second one
// in a channel would be refused as a duplicate.
func TestMessagesWithoutAClientMsgIDDoNotCollide(t *testing.T) {
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)

	owner := person("owner")
	ch := createChannel(t, channels, "room", owner)

	h := NewHub()
	s := &server{channels: channels, messages: messages, hub: h, bus: h}

	for i, text := range []string{"first", "second"} {
		code, body := callMessageHandler(t, s.handleSendMessage, "", map[string]string{
			"channel_id": ch.ID.Hex(),
			"text":       text,
		}, owner)
		if code != http.StatusCreated {
			t.Fatalf("message %d gave %d (%s), want 201", i+1, code, body)
		}
	}
}
