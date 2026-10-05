package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// directFixture is two friends with a direct conversation between them, on a
// server whose bus is a local hub.
type directFixture struct {
	s      *server
	hub    *Hub
	alice  User
	bob    User
	direct Channel
}

func newDirectFixture(t *testing.T) directFixture {
	t.Helper()
	ctx := context.Background()
	db := testDB(t)
	users, friends := newUserStore(db), newFriendStore(db)
	channels, messages := newMessageTestStores(t, db)
	if err := friends.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	h := NewHub()
	s := &server{hub: h, bus: h, users: users, friends: friends, channels: channels, messages: messages}

	f := directFixture{s: s, hub: h, alice: User{Username: "alice"}, bob: User{Username: "bob"}}
	for _, u := range []*User{&f.alice, &f.bob} {
		if err := users.Create(ctx, u); err != nil {
			t.Fatalf("creating %s: %v", u.Username, err)
		}
	}
	req, err := friends.Send(ctx, f.session(f.alice), &f.bob)
	if err != nil {
		t.Fatalf("friend request: %v", err)
	}
	if _, err := friends.Respond(ctx, req.ID, f.session(f.bob), friendAccepted); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if f.direct, err = channels.Direct(ctx, f.session(f.alice), &f.bob); err != nil {
		t.Fatalf("direct: %v", err)
	}
	return f
}

func (f directFixture) session(u User) Session {
	return Session{UserID: u.ID, Username: u.Username}
}

func (f directFixture) leave(who Session, channelID string) int {
	r := httptest.NewRequest(http.MethodPost, "/channels/"+channelID+"/leave", nil)
	r.SetPathValue("id", channelID)
	w := httptest.NewRecorder()
	f.s.handleLeaveChannel(w, r, who)
	return w.Code
}

func TestADirectConversationCannotBeLeft(t *testing.T) {
	f := newDirectFixture(t)
	ctx := context.Background()

	if code := f.leave(f.session(f.alice), f.direct.ID.Hex()); code != http.StatusBadRequest {
		t.Fatalf("leaving a direct conversation got %d, want 400", code)
	}
	got, err := f.s.channels.ByID(ctx, f.direct.ID)
	if err != nil || memberRole(got, f.alice.ID) == "" {
		t.Fatalf("alice is out of the conversation after a refused leave: %v %+v", err, got.Members)
	}
	// An outsider still gets the answer for a channel that is not there, not
	// one that says what kind of channel it is.
	if code := f.leave(person("stranger"), f.direct.ID.Hex()); code != http.StatusNotFound {
		t.Fatalf("an outsider leaving got %d, want 404", code)
	}
	// Named channels are left as before.
	team := createChannel(t, f.s.channels, "team", f.session(f.alice))
	if code := f.leave(f.session(f.alice), team.ID.Hex()); code != http.StatusOK {
		t.Fatalf("leaving a named channel got %d, want 200", code)
	}
}

// Someone who left a direct conversation before leaving was refused finds it
// again without themselves in it. Opening it must not route its messages to
// them, while the one who stayed is routed as before.
func TestReopeningALeftConversationRoutesOnlyMembers(t *testing.T) {
	f := newDirectFixture(t)
	ctx := context.Background()
	if err := f.s.channels.Leave(ctx, f.direct.ID, f.alice.ID); err != nil {
		t.Fatalf("leaving the old way: %v", err)
	}

	aliceSock, bobSock := newSubscriber(f.alice.ID.Hex()), newSubscriber(f.bob.ID.Hex())
	f.hub.Connect(aliceSock, nil)
	f.hub.Connect(bobSock, nil)

	body, _ := json.Marshal(directChannelRequest{Username: "bob"})
	r := httptest.NewRequest(http.MethodPost, "/channels/direct", bytes.NewReader(body))
	w := httptest.NewRecorder()
	f.s.handleOpenDirect(w, r, f.session(f.alice))
	if w.Code != http.StatusOK {
		t.Fatalf("opening got %d %s", w.Code, w.Body)
	}

	f.hub.Publish(f.direct.ID.Hex(), []byte(`{"text":"for alice?"}`))
	if n := len(aliceSock.send); n != 0 {
		t.Fatalf("the one who left got %d messages of the conversation", n)
	}
	if n := len(bobSock.send); n != 1 {
		t.Fatalf("the one who stayed got %d messages, want 1", n)
	}
}

// Leave hands ownership on when the owner goes; a direct conversation has none,
// and one would be allowed to give it an invite code.
func TestADirectConversationGetsNoOwner(t *testing.T) {
	f := newDirectFixture(t)
	ctx := context.Background()

	// What an account deletion does to the other side.
	if err := f.s.channels.Leave(ctx, f.direct.ID, f.bob.ID); err != nil {
		t.Fatalf("leaving: %v", err)
	}
	got, err := f.s.channels.ByID(ctx, f.direct.ID)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if role := memberRole(got, f.alice.ID); role != roleMember {
		t.Fatalf("the one left in a direct conversation is %q, want %q", role, roleMember)
	}
	if _, err := f.s.channels.ResetInviteCode(ctx, f.direct.ID, f.alice.ID); err == nil {
		t.Fatalf("a direct conversation got an invite code")
	}
}
