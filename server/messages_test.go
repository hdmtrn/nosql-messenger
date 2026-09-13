package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	orig, err := messages.Insert(ctx, aliceRoom.ID, alice, "the plan", "", nil)
	if err != nil {
		t.Fatalf("inserting message: %v", err)
	}

	s := &server{channels: channels, messages: messages, hub: NewHub()}

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

	s := &server{channels: channels, messages: messages, hub: NewHub()}
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
