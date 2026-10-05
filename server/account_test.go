package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// accountFixture is alice with everything an account gathers: a session, a
// friend and a request waiting, a channel she owns with bob in it, one she is
// alone in, a direct conversation, messages, a forwarded copy and a picture.
type accountFixture struct {
	s         *server
	watcher   *Subscriber
	alice     User
	bob       User
	aliceSess Session
	team      Channel
	solo      Channel
	direct    Channel
	avatar    bson.ObjectID
}

func newAccountFixture(t *testing.T) accountFixture {
	t.Helper()
	ctx := context.Background()
	db := testDB(t)

	users, sessions := newUserStore(db), newSessionStore(db)
	channels, messages := newMessageTestStores(t, db)
	friends, media := newFriendStore(db), newMediaStore(db)
	for _, ensure := range []func(context.Context) error{
		users.ensureIndexes, sessions.ensureIndexes, friends.ensureIndexes, media.ensureIndexes,
	} {
		if err := ensure(ctx); err != nil {
			t.Fatalf("indexes: %v", err)
		}
	}

	authSvc, err := newAuth(users, sessions, nil)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	h := NewHub()
	s := &server{
		hub: h, bus: h, presence: &presence{nodeID: "this-node"},
		auth: authSvc, sessions: sessions, users: users, channels: channels,
		messages: messages, friends: friends, media: media,
	}

	newUser := func(name string) User {
		hash, err := hashPassword(alicePassword, defaultArgonParams)
		if err != nil {
			t.Fatalf("hashing: %v", err)
		}
		u := User{Username: name, DisplayName: strings.ToUpper(name[:1]) + name[1:], Bio: "about " + name, PasswordHash: hash}
		if err := users.Create(ctx, &u); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
		return u
	}
	f := accountFixture{s: s, alice: newUser("alice"), bob: newUser("bob")}
	carol := newUser("carol")
	asSession := func(u User) Session { return Session{UserID: u.ID, Username: u.Username} }

	if f.aliceSess, err = sessions.Create(ctx, &f.alice, "test"); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := sessions.Create(ctx, &f.alice, "another device"); err != nil {
		t.Fatalf("session: %v", err)
	}

	req, err := friends.Send(ctx, asSession(f.alice), &f.bob)
	if err != nil {
		t.Fatalf("friend request: %v", err)
	}
	if _, err := friends.Respond(ctx, req.ID, asSession(f.bob), friendAccepted); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if _, err := friends.Send(ctx, asSession(carol), &f.alice); err != nil {
		t.Fatalf("friend request: %v", err)
	}

	f.team = createChannel(t, channels, "team", asSession(f.alice))
	if err := channels.AddMember(ctx, f.team.ID, asSession(f.bob)); err != nil {
		t.Fatalf("joining: %v", err)
	}
	f.solo = createChannel(t, channels, "solo", asSession(f.alice))
	if f.direct, err = channels.Direct(ctx, asSession(f.alice), &f.bob); err != nil {
		t.Fatalf("direct: %v", err)
	}

	insert := func(ch bson.ObjectID, author User, text string, fwd *ForwardedFrom) {
		msg := Message{ChannelID: ch, Author: MessageAuthor{ID: author.ID, Username: author.Username}, Text: text, Forwarded: fwd}
		if _, err := messages.Insert(ctx, msg); err != nil {
			t.Fatalf("inserting: %v", err)
		}
	}
	insert(f.team.ID, f.alice, "hello team", nil)
	insert(f.team.ID, f.bob, "hi alice", nil)
	insert(f.direct.ID, f.alice, "just us", nil)
	insert(f.solo.ID, f.bob, "forwarded", &ForwardedFrom{
		ChannelID: f.team.ID,
		Author:    MessageAuthor{ID: f.alice.ID, Username: "alice"},
	})

	pic, err := media.Save(ctx, f.alice.ID, mediaKindAvatar, nil, bytes.NewReader(testPNG(t, 2, 2)))
	if err != nil {
		t.Fatalf("saving avatar: %v", err)
	}
	if _, err := users.SetAvatar(ctx, f.alice.ID, &pic.ID); err != nil {
		t.Fatalf("setting avatar: %v", err)
	}
	f.avatar = pic.ID

	f.watcher = newSubscriber("watcher")
	h.Connect(f.watcher, []string{f.team.ID.Hex()})
	return f
}

func (f accountFixture) deleteAccount(password string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(deleteAccountRequest{Password: password})
	r := httptest.NewRequest(http.MethodDelete, "/auth/me", bytes.NewReader(body))
	r.RemoteAddr = "198.51.100.7:40000"
	w := httptest.NewRecorder()
	f.s.handleDeleteAccount(w, r, f.aliceSess)
	return w
}

func (f accountFixture) count(t *testing.T, col *mongo.Collection, filter bson.M) int64 {
	t.Helper()
	n, err := col.CountDocuments(context.Background(), filter)
	if err != nil {
		t.Fatalf("counting in %s: %v", col.Name(), err)
	}
	return n
}

// assertErased checks everything a finished erasure leaves behind.
func (f accountFixture) assertErased(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	s, id := f.s, f.alice.ID
	tomb := tombName(id)

	var doc bson.M
	if err := s.users.col.FindOne(ctx, bson.M{"_id": id}).Decode(&doc); err != nil {
		t.Fatalf("the account document went: %v", err)
	}
	if doc["username"] != tomb || doc["deleted_at"] == nil {
		t.Fatalf("account left as %v, want username %s and deleted_at", doc, tomb)
	}
	for _, field := range []string{"password_hash", "bio", "avatar_id", "last_seen_at", "erase_owner", "erase_until"} {
		if _, ok := doc[field]; ok {
			t.Fatalf("%s survived the erasure", field)
		}
	}
	if doc["display_name"] != "" {
		t.Fatalf("display name survived: %v", doc["display_name"])
	}

	if n := f.count(t, s.sessions.col, bson.M{"user_id": id}); n != 0 {
		t.Fatalf("%d sessions survived", n)
	}
	if n := f.count(t, s.friends.col, bson.M{"$or": bson.A{bson.M{"from.id": id}, bson.M{"to.id": id}}}); n != 0 {
		t.Fatalf("%d friend requests survived", n)
	}
	if n := f.count(t, s.media.col, bson.M{"_id": f.avatar}); n != 0 {
		t.Fatalf("the avatar survived")
	}

	// The messages stay, under the tomb name; bob's are his own.
	if n := f.count(t, s.messages.col, bson.M{"author.id": id, "author.username": tomb}); n != 2 {
		t.Fatalf("%d of alice's 2 messages carry the tomb name", n)
	}
	if n := f.count(t, s.messages.col, bson.M{"forwarded.author.id": id, "forwarded.author.username": tomb}); n != 1 {
		t.Fatalf("the forwarded copy still names alice")
	}
	if n := f.count(t, s.messages.col, bson.M{"author.id": f.bob.ID, "author.username": "bob"}); n != 2 {
		t.Fatalf("bob's messages were touched")
	}

	team, err := s.channels.ByID(ctx, f.team.ID)
	if err != nil {
		t.Fatalf("reading team: %v", err)
	}
	if memberRole(team, id) != "" || memberRole(team, f.bob.ID) != roleOwner {
		t.Fatalf("team members after the erasure: %+v, want bob alone as owner", team.Members)
	}
	solo, err := s.channels.ByID(ctx, f.solo.ID)
	if err != nil || solo.DeletedAt == nil {
		t.Fatalf("the channel alice was alone in is not marked for the purge: %v %+v", err, solo)
	}
	direct, err := s.channels.ByID(ctx, f.direct.ID)
	if err != nil || len(direct.Members) != 1 || memberRole(direct, f.bob.ID) == "" {
		t.Fatalf("the direct conversation should stay with bob: %v %+v", err, direct.Members)
	}
}

func TestDeletingAnAccountErasesWhatIsPersonal(t *testing.T) {
	f := newAccountFixture(t)

	w := f.deleteAccount(alicePassword)
	if w.Code != http.StatusOK {
		t.Fatalf("delete got %d %s, want 200", w.Code, w.Body)
	}
	if c := w.Result().Cookies(); len(c) == 0 || c[0].MaxAge >= 0 {
		t.Fatalf("the session cookie was not cleared: %v", c)
	}
	f.assertErased(t)

	// The chats with her messages on screen are told, while she is still a member.
	var told bool
	for len(f.watcher.send) > 0 {
		var ev profileEvent
		json.Unmarshal(<-f.watcher.send, &ev)
		told = told || (ev.Type == "profile" && ev.User.Deleted && ev.User.ID == f.alice.ID.Hex())
	}
	if !told {
		t.Fatalf("the team channel got no event saying alice is gone")
	}

	// The name is free again for whoever registers next.
	again := User{Username: "alice", PasswordHash: "x"}
	if err := f.s.users.Create(context.Background(), &again); err != nil {
		t.Fatalf("registering alice again: %v", err)
	}
}

func TestDeletingNeedsThePassword(t *testing.T) {
	f := newAccountFixture(t)

	if w := f.deleteAccount("not the password"); w.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong password got %d, want 401", w.Code)
	}
	if _, err := f.s.users.GetByUsername(context.Background(), "alice"); err != nil {
		t.Fatalf("alice is gone after a refused deletion: %v", err)
	}
	if n := f.count(t, f.s.sessions.col, bson.M{"user_id": f.alice.ID}); n != 2 {
		t.Fatalf("%d sessions left after a refused deletion, want 2", n)
	}
}

// A node that stops right after the mark leaves the rest to whichever node's
// purge loop finds the claim run out.
func TestAnErasureCutShortIsFinishedByTheLoop(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()

	if _, err := f.s.users.markDeleted(ctx, f.alice.ID, "dead-node", time.Now().Add(-2*eraseLease)); err != nil {
		t.Fatalf("marking: %v", err)
	}
	if err := f.s.finishErasures(ctx, "this-node"); err != nil {
		t.Fatalf("finishing: %v", err)
	}
	f.assertErased(t)
}

// While the node that marked it is still at work, nobody else takes it.
func TestALiveErasureIsLeftToItsNode(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()

	if _, err := f.s.users.markDeleted(ctx, f.alice.ID, "busy-node", time.Now()); err != nil {
		t.Fatalf("marking: %v", err)
	}
	if err := f.s.finishErasures(ctx, "this-node"); err != nil {
		t.Fatalf("finishing: %v", err)
	}
	if n := f.count(t, f.s.sessions.col, bson.M{"user_id": f.alice.ID}); n != 2 {
		t.Fatalf("another node erased an account whose claim was still held")
	}
	if err := f.s.eraseAccount(ctx, f.alice.ID, "this-node"); !errors.Is(err, errErasureLost) {
		t.Fatalf("finishing someone else's erasure: got %v, want errErasureLost", err)
	}
}

// The messages take the tomb name before the old name is let go: if the name
// went first, a newcomer under it would be shown as their author. A node that
// does the work without the claim stops right at that line.
func TestTheMessagesAreRenamedBeforeTheNameGoes(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()

	if _, err := f.s.users.markDeleted(ctx, f.alice.ID, "busy-node", time.Now()); err != nil {
		t.Fatalf("marking: %v", err)
	}
	if err := f.s.eraseAccount(ctx, f.alice.ID, "this-node"); !errors.Is(err, errErasureLost) {
		t.Fatalf("got %v, want errErasureLost", err)
	}
	if n := f.count(t, f.s.messages.col, bson.M{"author.id": f.alice.ID, "author.username": "alice"}); n != 0 {
		t.Fatalf("%d messages still carry the old name at the point the name would go", n)
	}
	if n := f.count(t, f.s.users.col, bson.M{"_id": f.alice.ID, "username": "alice"}); n != 1 {
		t.Fatalf("the name went without the claim")
	}
}

// A send that got past its checks before the sessions went can land after the
// first rename; the second one catches it.
func TestASendCaughtMidErasureGetsTheTombName(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	f.s.beforeFinishErasure = func() {
		late := Message{ChannelID: f.team.ID, Author: MessageAuthor{ID: f.alice.ID, Username: "alice"}, Text: "late"}
		if _, err := f.s.messages.Insert(ctx, late); err != nil {
			t.Errorf("inserting the late message: %v", err)
		}
	}

	if w := f.deleteAccount(alicePassword); w.Code != http.StatusOK {
		t.Fatalf("delete got %d", w.Code)
	}
	if n := f.count(t, f.s.messages.col, bson.M{"author.id": f.alice.ID, "author.username": "alice"}); n != 0 {
		t.Fatalf("the late message kept the old name")
	}
}

// Between the mark and the end the old name is still taken, so a newcomer
// cannot appear as the author of messages that do not carry the tomb name yet.
func TestTheNameIsFreedOnlyAtTheEnd(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()

	if _, err := f.s.users.markDeleted(ctx, f.alice.ID, "this-node", time.Now()); err != nil {
		t.Fatalf("marking: %v", err)
	}
	newcomer := User{Username: "alice", PasswordHash: "x"}
	if err := f.s.users.Create(ctx, &newcomer); !errors.Is(err, errUsernameTaken) {
		t.Fatalf("registering the name mid-erasure: got %v, want errUsernameTaken", err)
	}
	if _, err := f.s.users.GetByUsername(ctx, "alice"); !errors.Is(err, errUserNotFound) {
		t.Fatalf("a marked account is still found: %v", err)
	}
	// Login goes the way of a name that does not exist, not to a 500 over the
	// missing hash.
	if code := authCall(f.s.auth.handleLogin, "198.51.100.7", "alice", alicePassword); code != http.StatusUnauthorized {
		t.Fatalf("logging in to a deleted account got %d, want 401", code)
	}
	if err := f.s.eraseAccount(ctx, f.alice.ID, "this-node"); err != nil {
		t.Fatalf("erasing: %v", err)
	}
	if err := f.s.users.Create(ctx, &newcomer); err != nil {
		t.Fatalf("registering the name after the erasure: %v", err)
	}
}

// Writes from requests already under way, or from the sockets the deletion
// closes, must not put back what it erased.
func TestADeletedAccountIsNotWrittenToOrFound(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	if _, err := f.s.users.markDeleted(ctx, f.alice.ID, "this-node", time.Now()); err != nil {
		t.Fatalf("marking: %v", err)
	}

	f.s.users.SetLastSeen(ctx, f.alice.ID, time.Now())
	f.s.users.UpdateProfile(ctx, f.alice.ID, "Back", "again")
	if _, err := f.s.users.SetAvatar(ctx, f.alice.ID, nil); !errors.Is(err, errUserNotFound) {
		t.Fatalf("changing a deleted account's avatar: got %v, want errUserNotFound", err)
	}
	var doc bson.M
	f.s.users.col.FindOne(ctx, bson.M{"_id": f.alice.ID}).Decode(&doc)
	if _, ok := doc["last_seen_at"]; ok || doc["display_name"] != "" || doc["bio"] != nil {
		t.Fatalf("a write reached the deleted account: %v", doc)
	}
	if found, _ := f.s.users.Search(ctx, "ali"); len(found) != 0 {
		t.Fatalf("search found a deleted account: %+v", found)
	}
	if marked, _ := f.s.users.markDeleted(ctx, f.alice.ID, "other-tab", time.Now()); marked {
		t.Fatalf("an account was marked deleted twice")
	}
}

func TestADeletedAuthorStillHasAProfile(t *testing.T) {
	f := newAccountFixture(t)
	if w := f.deleteAccount(alicePassword); w.Code != http.StatusOK {
		t.Fatalf("delete got %d", w.Code)
	}

	tomb := tombName(f.alice.ID)
	r := httptest.NewRequest(http.MethodGet, "/users/"+tomb, nil)
	r.SetPathValue("username", tomb)
	w := httptest.NewRecorder()
	f.s.handleGetUser(w, r, Session{UserID: f.bob.ID, Username: "bob"})

	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || got["deleted"] != true || got["username"] != tomb {
		t.Fatalf("profile of a deleted author: %d %v", w.Code, got)
	}
	if got["display_name"] != "" || got["avatar_id"] != nil || got["bio"] != nil {
		t.Fatalf("the profile of a deleted author says more than that it is gone: %v", got)
	}
}

func TestTheTombPrefixCannotBeRegistered(t *testing.T) {
	for _, name := range []string{"deleted-x", "Deleted-6ac40089c321518b93250684", "DELETED-abc"} {
		if err := validateUsername(name); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := validateUsername("deletedx"); err != nil {
		t.Errorf("deletedx was refused: %v", err)
	}
}
