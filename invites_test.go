package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// testDB hands out a database of its own per test and drops it afterwards, so
// tests cannot see each other's documents and none of them touch "messenger".
// Without MongoDB running the test skips rather than fails: these are
// integration tests, and a missing database is a missing prerequisite, not a
// broken invariant.
func testDB(t *testing.T) *mongo.Database {
	t.Helper()

	ctx := context.Background()
	client, err := connectMongo(ctx)
	if err != nil {
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

func TestCreateInviteRefusesDirectChannel(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, invites := newChannelStore(db), newInviteStore(db)
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	alice, bob := person("alice"), person("bob")
	dm, err := channels.Direct(ctx, alice, &User{ID: bob.UserID, Username: bob.Username})
	if err != nil {
		t.Fatalf("opening direct: %v", err)
	}

	s := &server{channels: channels, invites: invites}
	code, body := callInvite(t, s.handleCreateInvite, dm.ID, alice)
	if code != http.StatusBadRequest {
		t.Fatalf("inviting into a direct conversation: got %d %s, want 400", code, body)
	}

	list, err := invites.ForChannel(ctx, dm.ID)
	if err != nil {
		t.Fatalf("listing invites: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("a direct conversation ended up with %d invites", len(list))
	}
}

func TestInviteEndpointsHideChannelsYouAreNotIn(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, invites := newChannelStore(db), newInviteStore(db)
	if err := channels.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	ch, err := channels.Create(ctx, "private", person("owner"))
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}

	s := &server{channels: channels, invites: invites}
	// An outsider must not be able to tell "not yours" from "no such thing".
	if code, body := callInvite(t, s.handleListInvites, ch.ID, person("stranger")); code != http.StatusNotFound {
		t.Fatalf("outsider listing invites: got %d %s, want 404", code, body)
	}
	if code, body := callInvite(t, s.handleCreateInvite, ch.ID, person("stranger")); code != http.StatusNotFound {
		t.Fatalf("outsider making an invite: got %d %s, want 404", code, body)
	}
}

func TestCreateInviteStopsAtTheNumberTheListShows(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, invites := newChannelStore(db), newInviteStore(db)
	if err := invites.ensureIndexes(ctx); err != nil {
		t.Fatalf("indexes: %v", err)
	}

	owner := person("owner")
	ch, err := channels.Create(ctx, "general", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}

	for i := 0; i < invitesMaxPerChannel; i++ {
		if _, err := invites.Create(ctx, ch.ID, owner.UserID); err != nil {
			t.Fatalf("invite %d of %d: %v", i+1, invitesMaxPerChannel, err)
		}
	}
	if _, err := invites.Create(ctx, ch.ID, owner.UserID); !errors.Is(err, errTooManyInvites) {
		t.Fatalf("invite past the cap: got %v, want errTooManyInvites", err)
	}

	// The point of the cap: everything that exists is also revocable, because
	// everything that exists comes back in the list.
	stored, err := invites.col.CountDocuments(ctx, bson.M{"channel_id": ch.ID})
	if err != nil {
		t.Fatalf("counting invites: %v", err)
	}
	listed, err := invites.ForChannel(ctx, ch.ID)
	if err != nil {
		t.Fatalf("listing invites: %v", err)
	}
	if int(stored) != len(listed) {
		t.Fatalf("%d invites stored but %d listed — the rest cannot be revoked", stored, len(listed))
	}
}

func TestRevokeOnlyTouchesItsOwnChannel(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, invites := newChannelStore(db), newInviteStore(db)

	owner := person("owner")
	mine, err := channels.Create(ctx, "mine", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	yours, err := channels.Create(ctx, "yours", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}

	inv, err := invites.Create(ctx, mine.ID, owner.UserID)
	if err != nil {
		t.Fatalf("creating invite: %v", err)
	}

	if err := invites.Revoke(ctx, yours.ID, inv.Code); !errors.Is(err, errInviteNotFound) {
		t.Fatalf("revoking someone else's code: got %v, want errInviteNotFound", err)
	}
	if _, err := invites.ByCode(ctx, inv.Code); err != nil {
		t.Fatalf("the code should have survived: %v", err)
	}
	if err := invites.Revoke(ctx, mine.ID, inv.Code); err != nil {
		t.Fatalf("revoking own code: %v", err)
	}
	if _, err := invites.ByCode(ctx, inv.Code); !errors.Is(err, errInviteNotFound) {
		t.Fatalf("revoked code still resolves: %v", err)
	}
}

func TestDiscardTakesInvitesWithIt(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, invites, messages := newChannelStore(db), newInviteStore(db), newMessageStore(db)

	owner := person("owner")
	ch, err := channels.Create(ctx, "doomed", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	if _, err := invites.Create(ctx, ch.ID, owner.UserID); err != nil {
		t.Fatalf("creating invite: %v", err)
	}
	if _, err := messages.Insert(ctx, ch.ID, owner, "hello", "c1"); err != nil {
		t.Fatalf("inserting message: %v", err)
	}

	// The last member leaving takes the channel, and a code outliving the
	// channel it points at would be an invite to nothing.
	if err := channels.Leave(ctx, messages, invites, ch.ID, owner.UserID); err != nil {
		t.Fatalf("leaving: %v", err)
	}

	left, err := invites.ForChannel(ctx, ch.ID)
	if err != nil {
		t.Fatalf("listing invites: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("%d invites outlived their channel", len(left))
	}
}

func TestFollowingAnInviteTwiceIsNotAnError(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, invites := newChannelStore(db), newInviteStore(db)

	owner := person("owner")
	ch, err := channels.Create(ctx, "general", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	inv, err := invites.Create(ctx, ch.ID, owner.UserID)
	if err != nil {
		t.Fatalf("creating invite: %v", err)
	}

	s := &server{channels: channels, invites: invites, hub: NewHub()}
	joiner := person("joiner")

	// The second call is the case that matters: a member clicking the link they
	// pasted into the channel themselves. It must still say which channel.
	for i, want := range []string{"joined", "member"} {
		code, body := followInvite(t, s, inv.Code, joiner)
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

func TestFollowingARevokedInviteLeadsNowhere(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, invites := newChannelStore(db), newInviteStore(db)

	owner := person("owner")
	ch, err := channels.Create(ctx, "general", owner)
	if err != nil {
		t.Fatalf("creating channel: %v", err)
	}
	inv, err := invites.Create(ctx, ch.ID, owner.UserID)
	if err != nil {
		t.Fatalf("creating invite: %v", err)
	}
	if err := invites.Revoke(ctx, ch.ID, inv.Code); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	s := &server{channels: channels, invites: invites, hub: NewHub()}
	// A revoked code and a code that never existed must be the same answer.
	revoked, _ := followInvite(t, s, inv.Code, person("joiner"))
	invented, _ := followInvite(t, s, "NOSUCHCODE", person("joiner"))
	if revoked != http.StatusNotFound || invented != http.StatusNotFound {
		t.Fatalf("revoked gave %d, invented gave %d, want 404 for both", revoked, invented)
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

// callInvite drives one invite handler without a router or a login: PathValue
// is set directly, and the session is a value the middleware would have looked
// up anyway.
func callInvite(
	t *testing.T,
	h func(http.ResponseWriter, *http.Request, Session),
	channelID bson.ObjectID,
	sess Session,
) (int, string) {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/channels/"+channelID.Hex()+"/invites", nil)
	r.SetPathValue("id", channelID.Hex())
	w := httptest.NewRecorder()

	h(w, r, sess)

	var msg struct {
		Error string `json:"error"`
	}
	json.Unmarshal(w.Body.Bytes(), &msg)
	return w.Code, msg.Error
}
