package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// testDB hands out a database of its own per test and drops it afterwards, so
// tests cannot see each other's documents and none of them touch "messenger".
// Without MongoDB running the test skips rather than fails: these are
// integration tests, and a missing database is a missing prerequisite, not a
// broken invariant. With -short it skips before even trying to connect, which
// is how the unit job in CI runs everything else without a database.
func testDB(t *testing.T) *mongo.Database {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test: needs MongoDB, skipped under -short")
	}

	ctx := context.Background()
	client, err := connectMongo(ctx)
	if err != nil {
		// In CI a missing database is a broken build, not a missing
		// prerequisite: a skipped test looks like a passed one, and the green
		// check mark starts to mean "nothing was verified".
		if os.Getenv("CI") != "" {
			t.Fatalf("no MongoDB at %s: %v", mongoURI(), err)
		}
		t.Skipf("no MongoDB at %s: %v", mongoURI(), err)
	}

	db := client.Database("messenger_test_" + strings.ToLower(rand.Text()[:8]))
	t.Cleanup(func() {
		if err := db.Drop(ctx); err != nil {
			t.Errorf("dropping test database: %v", err)
		}
		client.Disconnect(ctx)
	})
	return db
}

func person(name string) Session {
	return Session{UserID: bson.NewObjectID(), Username: name}
}

func TestAddMemberRefusesDirectChannel(t *testing.T) {
	ctx := context.Background()
	channels := newChannelStore(testDB(t))
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	alice, bob := person("alice"), person("bob")
	dm, err := channels.Direct(ctx, alice, &User{ID: bob.UserID, Username: bob.Username})
	if err != nil {
		t.Fatalf("opening direct: %v", err)
	}

	carol := person("carol")
	if err := channels.AddMember(ctx, dm.ID, carol); !errors.Is(err, errNotJoinable) {
		t.Fatalf("adding a third person to a direct conversation: got %v, want errNotJoinable", err)
	}

	// The refusal has to be a refusal to write, not just a returned error.
	after, err := channels.ByID(ctx, dm.ID)
	if err != nil {
		t.Fatalf("re-reading the conversation: %v", err)
	}
	if len(after.Members) != 2 {
		t.Fatalf("direct conversation has %d members, want 2", len(after.Members))
	}
}

func TestAddMemberJoinsNamedChannel(t *testing.T) {
	ctx := context.Background()
	channels := newChannelStore(testDB(t))
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	owner := person("owner")
	ch, err := channels.Create(ctx, "general", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}

	joiner := person("joiner")
	if err := channels.AddMember(ctx, ch.ID, joiner); err != nil {
		t.Fatalf("joining a named channel: %v", err)
	}
	// The kind clause must not swallow the reason a second attempt fails.
	if err := channels.AddMember(ctx, ch.ID, joiner); !errors.Is(err, errAlreadyMember) {
		t.Fatalf("joining twice: got %v, want errAlreadyMember", err)
	}
	if err := channels.AddMember(ctx, bson.NewObjectID(), joiner); !errors.Is(err, errChannelNotFound) {
		t.Fatalf("joining nothing: got %v, want errChannelNotFound", err)
	}
}

func TestADirectConversationHasNoInviteLink(t *testing.T) {
	ctx := context.Background()
	channels := newChannelStore(testDB(t))
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	alice, bob := person("alice"), person("bob")
	dm, err := channels.Direct(ctx, alice, &User{ID: bob.UserID, Username: bob.Username})
	if err != nil {
		t.Fatalf("opening direct: %v", err)
	}

	s := &server{channels: channels}
	if code, body := callInvite(t, s.handleGetInvite, dm.ID, alice); code != http.StatusBadRequest {
		t.Fatalf("asking a direct conversation for its link: got %d %s, want 400", code, body.Error)
	}
	// Nobody owns a conversation, so nobody can reset a link into it either.
	if code, body := callInvite(t, s.handleResetInvite, dm.ID, alice); code != http.StatusForbidden {
		t.Fatalf("resetting a direct conversation's link: got %d %s, want 403", code, body.Error)
	}

	if n, _ := channels.col.CountDocuments(ctx, bson.M{"_id": dm.ID, "invite_code": bson.M{"$exists": true}}); n != 0 {
		t.Fatalf("a direct conversation ended up with an invite code")
	}
}

func TestInviteEndpointsHideChannelsYouAreNotIn(t *testing.T) {
	ctx := context.Background()
	channels := newChannelStore(testDB(t))
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	ch, err := channels.Create(ctx, "private", person("owner"))
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}

	s := &server{channels: channels}
	// An outsider must not be able to tell "not yours" from "no such thing".
	if code, body := callInvite(t, s.handleGetInvite, ch.ID, person("stranger")); code != http.StatusNotFound {
		t.Fatalf("outsider reading the link: got %d %s, want 404", code, body.Error)
	}
	if code, body := callInvite(t, s.handleResetInvite, ch.ID, person("stranger")); code != http.StatusNotFound {
		t.Fatalf("outsider resetting the link: got %d %s, want 404", code, body.Error)
	}
}

func TestEveryMemberGetsTheSameLink(t *testing.T) {
	ctx := context.Background()
	channels := newChannelStore(testDB(t))
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	owner, member := person("owner"), person("member")
	ch, err := channels.Create(ctx, "general", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	if ch.InviteCode == "" {
		t.Fatalf("a new channel came without a link")
	}
	if err := channels.AddMember(ctx, ch.ID, member); err != nil {
		t.Fatalf("joining: %v", err)
	}

	s := &server{channels: channels}
	for _, who := range []Session{owner, member, member} {
		code, body := callInvite(t, s.handleGetInvite, ch.ID, who)
		if code != http.StatusOK || body.Code != ch.InviteCode {
			t.Fatalf("%s asked for the link: got %d %+v, want 200 %q", who.Username, code, body, ch.InviteCode)
		}
	}
}

func TestOnlyTheOwnerResetsTheLink(t *testing.T) {
	ctx := context.Background()
	channels := newChannelStore(testDB(t))
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	owner, member := person("owner"), person("member")
	ch, err := channels.Create(ctx, "general", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	if err := channels.AddMember(ctx, ch.ID, member); err != nil {
		t.Fatalf("joining: %v", err)
	}

	s := &server{channels: channels}
	if code, body := callInvite(t, s.handleResetInvite, ch.ID, member); code != http.StatusForbidden {
		t.Fatalf("a member resetting the link: got %d %s, want 403", code, body.Error)
	}
	// The refusal has to be a refusal to write, not just a returned error.
	if _, body := callInvite(t, s.handleGetInvite, ch.ID, member); body.Code != ch.InviteCode {
		t.Fatalf("a refused reset changed the link from %q to %q", ch.InviteCode, body.Code)
	}
}

func TestAChannelFromBeforeTheLinkGetsOneOnFirstAsk(t *testing.T) {
	ctx := context.Background()
	channels := newChannelStore(testDB(t))
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	owner := person("owner")
	ch, err := channels.Create(ctx, "old", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	if _, err := channels.col.UpdateOne(ctx, bson.M{"_id": ch.ID}, bson.M{"$unset": bson.M{"invite_code": ""}}); err != nil {
		t.Fatalf("taking the code away: %v", err)
	}

	// Several first asks at once: every one must come back with the code that
	// stayed, not with one a later ask then overwrote.
	const asks = 8
	got := make([]string, asks)
	var wg sync.WaitGroup
	for i := range asks {
		wg.Go(func() {
			code, err := channels.InviteCode(ctx, ch.ID, owner.UserID)
			if err != nil {
				t.Errorf("ask %d: %v", i, err)
			}
			got[i] = code
		})
	}
	wg.Wait()

	stored, err := channels.ByID(ctx, ch.ID)
	if err != nil {
		t.Fatalf("re-reading channel: %v", err)
	}
	if stored.InviteCode == "" {
		t.Fatalf("no code was stored")
	}
	for i, code := range got {
		if code != stored.InviteCode {
			t.Fatalf("ask %d got %q, but the channel kept %q", i, code, stored.InviteCode)
		}
	}
}

func TestLastLeaveMarksTheChannelAndClosesItsLink(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newChannelStore(db), newMessageStore(db)

	owner := person("owner")
	ch, err := channels.Create(ctx, "doomed", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	if _, err := messages.Insert(ctx, Message{ChannelID: ch.ID, Author: authorOf(owner), Text: "hello", ClientMsgID: "c1"}); err != nil {
		t.Fatalf("inserting message: %v", err)
	}

	if err := channels.Leave(ctx, ch.ID, owner.UserID); err != nil {
		t.Fatalf("leaving: %v", err)
	}

	got, err := channels.ByID(ctx, ch.ID)
	if err != nil {
		t.Fatalf("the channel went at once instead of waiting for the purge: %v", err)
	}
	if got.DeletedAt == nil {
		t.Fatalf("the last member left and the channel is not marked")
	}
	// A link outliving the channel it points at would be an invite to nothing.
	h := NewHub()
	s := &server{channels: channels, hub: h, bus: h}
	if code, body := followInvite(t, s, ch.InviteCode, person("late")); code != http.StatusNotFound {
		t.Fatalf("following the link of a marked channel: got %d %q, want 404", code, body.Error)
	}
	// Deleting the history is the purge job's.
	if n, _ := messages.col.CountDocuments(ctx, bson.M{"channel_id": ch.ID}); n != 1 {
		t.Fatalf("the history went at once: %d of 1 messages left", n)
	}
}

func TestAMarkedChannelTakesNoNewMembers(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels := newChannelStore(db)

	owner, newcomer := person("owner"), person("newcomer")
	ch, err := channels.Create(ctx, "doomed", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	if err := channels.Leave(ctx, ch.ID, owner.UserID); err != nil {
		t.Fatalf("leaving: %v", err)
	}

	// The mark is what closes the race with the last member leaving: a join
	// that comes after it finds the channel gone.
	if err := channels.AddMember(ctx, ch.ID, newcomer); !errors.Is(err, errChannelNotFound) {
		t.Fatalf("joining a marked channel: got %v, want errChannelNotFound", err)
	}
}

func TestALeaveThatLeavesSomeoneKeepsTheChannel(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels := newChannelStore(db)

	owner, member := person("owner"), person("member")
	ch, err := channels.Create(ctx, "team", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	if err := channels.AddMember(ctx, ch.ID, member); err != nil {
		t.Fatalf("joining: %v", err)
	}
	if err := channels.Leave(ctx, ch.ID, owner.UserID); err != nil {
		t.Fatalf("leaving: %v", err)
	}

	got, err := channels.ByID(ctx, ch.ID)
	if err != nil {
		t.Fatalf("reading channel: %v", err)
	}
	if got.DeletedAt != nil {
		t.Fatalf("a channel with a member left in it was marked")
	}
	if role := memberRole(got, member.UserID); role != roleOwner {
		t.Fatalf("the remaining member is %q, want %q", role, roleOwner)
	}
	// The same write handles direct_key for conversations; a named channel
	// must not come out of it with a null key, which the unique index would
	// then hold against the next named channel.
	if n, _ := channels.col.CountDocuments(ctx, bson.M{"_id": ch.ID, "direct_key": bson.M{"$exists": true}}); n != 0 {
		t.Fatalf("leaving gave a named channel a direct_key")
	}
}

func TestBothLeavingAConversationFreesThePair(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels := newChannelStore(db)
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	alice, bob := person("alice"), person("bob")
	first, err := channels.Direct(ctx, alice, &User{ID: bob.UserID, Username: bob.Username})
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	for _, u := range []Session{alice, bob} {
		if err := channels.Leave(ctx, first.ID, u.UserID); err != nil {
			t.Fatalf("%s leaving: %v", u.Username, err)
		}
	}

	// The marked conversation waits for the purge, but the two of them can
	// talk again in the meantime.
	again, err := channels.Direct(ctx, alice, &User{ID: bob.UserID, Username: bob.Username})
	if err != nil {
		t.Fatalf("opening again: %v", err)
	}
	if again.ID == first.ID || again.DeletedAt != nil || len(again.Members) != 2 {
		t.Fatalf("opening again returned the marked conversation: %+v", again)
	}
}

func TestEnsureOwnerPicksTheEarliestRemaining(t *testing.T) {
	ctx := context.Background()
	channels := newChannelStore(testDB(t))

	owner, first, second := person("owner"), person("first"), person("second")
	ch, err := channels.Create(ctx, "team", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	for _, u := range []Session{first, second} {
		if err := channels.AddMember(ctx, ch.ID, u); err != nil {
			t.Fatalf("joining: %v", err)
		}
	}

	// The owner and the member next in line leave at once: the owner's Leave
	// still has the first member in its list when it gets to the promotion.
	for _, u := range []Session{owner, first} {
		if _, err := channels.col.UpdateOne(ctx, bson.M{"_id": ch.ID},
			bson.M{"$pull": bson.M{"members": bson.M{"user_id": u.UserID}}}); err != nil {
			t.Fatalf("pulling %s: %v", u.Username, err)
		}
	}
	if err := channels.ensureOwner(ctx, ch.ID); err != nil {
		t.Fatalf("ensuring an owner: %v", err)
	}
	// A second call finds the owner in place and changes nothing.
	if err := channels.ensureOwner(ctx, ch.ID); err != nil {
		t.Fatalf("ensuring an owner again: %v", err)
	}

	got, err := channels.ByID(ctx, ch.ID)
	if err != nil {
		t.Fatalf("reading channel: %v", err)
	}
	if role := memberRole(got, second.UserID); role != roleOwner {
		t.Fatalf("the remaining member is %q, want %q", role, roleOwner)
	}
}

func TestFollowingAnInviteTwiceIsNotAnError(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels := newChannelStore(db)

	owner := person("owner")
	ch, err := channels.Create(ctx, "general", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}

	h := NewHub()
	s := &server{channels: channels, hub: h, bus: h}
	joiner := person("joiner")

	// The second call is the case that matters: a member clicking the link they
	// pasted into the channel themselves. It must still say which channel.
	for i, want := range []string{"joined", "member"} {
		code, body := followInvite(t, s, ch.InviteCode, joiner)
		if code != http.StatusOK {
			t.Fatalf("attempt %d: got %d %q, want 200", i+1, code, body.Error)
		}
		if body.Status != want {
			t.Fatalf("attempt %d: status %q, want %q", i+1, body.Status, want)
		}
		if body.ChannelID != ch.ID.Hex() {
			t.Fatalf("attempt %d: channel %q, want %q", i+1, body.ChannelID, ch.ID.Hex())
		}
	}

	after, err := channels.ByID(ctx, ch.ID)
	if err != nil {
		t.Fatalf("re-reading channel: %v", err)
	}
	if len(after.Members) != 2 {
		t.Fatalf("following twice left %d members, want 2", len(after.Members))
	}
}

func TestAResetLinkLeadsNowhere(t *testing.T) {
	ctx := context.Background()
	channels := newChannelStore(testDB(t))
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	owner := person("owner")
	ch, err := channels.Create(ctx, "general", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}

	h := NewHub()
	s := &server{channels: channels, hub: h, bus: h}
	code, reset := callInvite(t, s.handleResetInvite, ch.ID, owner)
	fresh := reset.Code
	if code != http.StatusOK || fresh == "" || fresh == ch.InviteCode {
		t.Fatalf("resetting: got %d %+v, want 200 and a code other than %q", code, reset, ch.InviteCode)
	}

	// A reset code and a code that never existed must be the same answer.
	old, _ := followInvite(t, s, ch.InviteCode, person("joiner"))
	invented, _ := followInvite(t, s, "NOSUCHCODE", person("joiner"))
	if old != http.StatusNotFound || invented != http.StatusNotFound {
		t.Fatalf("the old code gave %d, an invented one gave %d, want 404 for both", old, invented)
	}
	if code, body := followInvite(t, s, fresh, person("joiner")); code != http.StatusOK || body.ChannelID != ch.ID.Hex() {
		t.Fatalf("following the new code: got %d %+v, want 200 into %s", code, body, ch.ID.Hex())
	}
}

// followInvite returns the status and, on success, the channel the caller was
// told to open — which is the whole point of the endpoint.
func followInvite(t *testing.T, s *server, code string, sess Session) (int, inviteReply) {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/invites/"+code, nil)
	r.SetPathValue("code", code)
	w := httptest.NewRecorder()

	s.handleFollowInvite(w, r, sess)

	var body inviteReply
	json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

type inviteReply struct {
	Status    string `json:"status"`
	ChannelID string `json:"channel_id"`
	Error     string `json:"error"`
}

// callInvite drives one link handler without a router or a login: PathValue
// is set directly, and the session is a value the middleware would have looked
// up anyway.
func callInvite(
	t *testing.T,
	h func(http.ResponseWriter, *http.Request, Session),
	channelID bson.ObjectID,
	sess Session,
) (int, linkReply) {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/channels/"+channelID.Hex()+"/invite", nil)
	r.SetPathValue("id", channelID.Hex())
	w := httptest.NewRecorder()

	h(w, r, sess)

	var body linkReply
	json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

type linkReply struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}
