package main

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Same rule as testDB and testInstance: without Redis the test is missing a
// prerequisite, but in CI that is a broken build.
func testPresence(t *testing.T) (*presence, string) {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test: needs Redis, skipped under -short")
	}

	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr()})
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		if os.Getenv("CI") != "" {
			t.Fatalf("no Redis at %s: %v", redisAddr(), err)
		}
		t.Skipf("no Redis at %s: %v", redisAddr(), err)
	}

	// A fresh node and a fresh user id per test: the tests share one Redis with
	// each other and with whatever runs on the machine.
	p := newPresence(rdb)
	userID := bson.NewObjectID().Hex()
	t.Cleanup(func() {
		rdb.Del(ctx,
			presenceSocketsKey+userID,
			presenceVersionKey+userID,
			presenceNodeKey+p.nodeID,
			presenceAliveKey+p.nodeID,
		)
		rdb.SRem(ctx, presenceNodesKey, p.nodeID)
		rdb.Close()
	})
	return p, userID
}

func connect(t *testing.T, p *presence, userID, socketID string) (presenceState, bool) {
	t.Helper()
	state, changed, err := p.connect(context.Background(), userID, socketID)
	if err != nil {
		t.Fatalf("connecting %s: %v", socketID, err)
	}
	return state, changed
}

func disconnect(t *testing.T, p *presence, userID, socketID string) (presenceState, bool) {
	t.Helper()
	state, changed, err := p.disconnect(context.Background(), userID, socketID)
	if err != nil {
		t.Fatalf("disconnecting %s: %v", socketID, err)
	}
	return state, changed
}

func lookupOne(t *testing.T, p *presence, userID string) presenceState {
	t.Helper()
	states, err := p.lookup(context.Background(), []string{userID})
	if err != nil {
		t.Fatalf("looking up %s: %v", userID, err)
	}
	return states[userID]
}

// Only the first socket and the last one are a change; the ones in between are
// the user's other tabs and nobody has to hear about them.
func TestPresenceChangesOnlyOnTheEdges(t *testing.T) {
	p, user := testPresence(t)

	if state, changed := connect(t, p, user, "a"); !changed || !state.Online {
		t.Fatalf("first socket: changed=%v online=%v, want true/true", changed, state.Online)
	}
	if state, changed := connect(t, p, user, "b"); changed || !state.Online {
		t.Fatalf("second socket: changed=%v online=%v, want false/true", changed, state.Online)
	}
	if state, changed := disconnect(t, p, user, "a"); changed || !state.Online {
		t.Fatalf("closing one of two: changed=%v online=%v, want false/true", changed, state.Online)
	}
	if state, changed := disconnect(t, p, user, "b"); !changed || state.Online {
		t.Fatalf("closing the last: changed=%v online=%v, want true/false", changed, state.Online)
	}
}

// The hub can drop a socket twice, and the second drop must not announce an
// offline the user is not in.
func TestPresenceDisconnectTwice(t *testing.T) {
	p, user := testPresence(t)

	connect(t, p, user, "a")
	disconnect(t, p, user, "a")

	if state, changed := disconnect(t, p, user, "a"); changed || state.Online {
		t.Fatalf("second disconnect: changed=%v online=%v, want false/false", changed, state.Online)
	}
	if state, changed := connect(t, p, user, "b"); !changed || !state.Online {
		t.Fatalf("connecting after it: changed=%v online=%v, want true/true", changed, state.Online)
	}
}

// A reload closes the old socket on one node and opens a new one on another, in
// either order. Whatever the order, the state with the highest version is the
// true one — that is what the version is for.
func TestPresenceReloadInEitherOrder(t *testing.T) {
	for _, closeFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "close first", false: "open first"}[closeFirst], func(t *testing.T) {
			p, user := testPresence(t)
			connect(t, p, user, "old")

			var closed, opened presenceState
			if closeFirst {
				closed, _ = disconnect(t, p, user, "old")
				opened, _ = connect(t, p, user, "new")
			} else {
				opened, _ = connect(t, p, user, "new")
				closed, _ = disconnect(t, p, user, "old")
			}

			latest := closed
			if opened.Version > closed.Version {
				latest = opened
			}
			if !latest.Online {
				t.Fatalf("the newest state (version %d) says offline after a reload", latest.Version)
			}
			if state := lookupOne(t, p, user); !state.Online {
				t.Fatalf("lookup says offline after a reload")
			}
		})
	}
}

func TestPresenceLookup(t *testing.T) {
	p, user := testPresence(t)
	_, other := testPresence(t)

	state, _ := connect(t, p, user, "a")

	states, err := p.lookup(context.Background(), []string{user, other})
	if err != nil {
		t.Fatalf("looking up two users: %v", err)
	}
	if got := states[user]; !got.Online || got.Version != state.Version {
		t.Fatalf("online user: %+v, want online at version %d", got, state.Version)
	}
	// Nobody has ever connected as this one, so it has no keys at all.
	if got := states[other]; got.Online || got.Version != 0 {
		t.Fatalf("unknown user: %+v, want offline at version 0", got)
	}
}

// presenceSetup wires a server whose bus reaches this process only, with a
// watcher already reading the channel. Nothing reads the watcher's socket, so
// len(send) counts the events it was sent.
func presenceSetup(t *testing.T) (s *server, watcher *Subscriber, user, chID string) {
	t.Helper()

	p, user := testPresence(t)
	h := NewHub()
	s = &server{hub: h, bus: h, presence: p}
	chID = bson.NewObjectID().Hex()

	watcher = newSubscriber(bson.NewObjectID().Hex())
	h.Connect(watcher, []string{chID})
	return s, watcher, user, chID
}

// open connects a socket of the user the way handleWS does.
func openSocket(t *testing.T, s *server, user, chID string) *Subscriber {
	t.Helper()
	c := newSubscriber(user)
	s.hub.Connect(c, []string{chID})
	s.socketOpened(context.Background(), c, []string{chID})
	return c
}

func closeSocket(t *testing.T, s *server, c *Subscriber) {
	t.Helper()
	leaving := s.hub.ChannelsOf(c)
	s.hub.Disconnect(c)
	s.socketClosed(c, leaving)
}

func nextEvent(t *testing.T, c *Subscriber) presenceEvent {
	t.Helper()
	select {
	case raw := <-c.send:
		var ev presenceEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("decoding the event: %v", err)
		}
		if ev.Type != "presence" {
			t.Fatalf("event type %q, want presence", ev.Type)
		}
		return ev
	default:
		t.Fatal("no event, want one")
		return presenceEvent{}
	}
}

func TestPresenceTellsTheChannel(t *testing.T) {
	s, watcher, user, chID := presenceSetup(t)

	first := openSocket(t, s, user, chID)
	ev := nextEvent(t, watcher)
	if ev.UserID != user || !ev.Online {
		t.Fatalf("event %+v, want %s online", ev, user)
	}

	// Another tab of the same user is nobody else's business.
	second := openSocket(t, s, user, chID)
	if n := len(watcher.send); n != 0 {
		t.Fatalf("a second socket sent %d events, want 0", n)
	}

	closeSocket(t, s, first)
	if n := len(watcher.send); n != 0 {
		t.Fatalf("closing one of two sockets sent %d events, want 0", n)
	}

	closeSocket(t, s, second)
	gone := nextEvent(t, watcher)
	if gone.UserID != user || gone.Online {
		t.Fatalf("event %+v, want %s offline", gone, user)
	}
	if gone.Version <= ev.Version {
		t.Fatalf("version went %d -> %d, want it to grow", ev.Version, gone.Version)
	}
}

// The socket that is leaving must not be told about itself, and a channel it
// joined after connecting has to hear about it all the same.
func TestPresenceLeavingSocketHearsNothing(t *testing.T) {
	s, watcher, user, chID := presenceSetup(t)

	c := openSocket(t, s, user, chID)
	<-watcher.send
	// Its own arrival it does hear: it is in the channel by then, and the
	// client skips its own user id.
	nextEvent(t, c)

	joined := bson.NewObjectID().Hex()
	late := newSubscriber(bson.NewObjectID().Hex())
	s.hub.Connect(late, []string{joined})
	s.hub.Subscribe(user, joined)

	closeSocket(t, s, c)

	if n := len(c.send); n != 0 {
		t.Fatalf("the leaving socket got %d events, want 0", n)
	}
	if ev := nextEvent(t, late); ev.Online {
		t.Fatalf("the channel joined later got %+v, want offline", ev)
	}
	nextEvent(t, watcher)
}

// Last seen is written on the edge only: while another tab is open the user has
// not been seen leaving, and the offline event carries the time it was written.
func TestPresenceLastSeen(t *testing.T) {
	ctx := context.Background()
	s, watcher, user, chID := presenceSetup(t)
	s.users = newUserStore(testDB(t))

	id, err := bson.ObjectIDFromHex(user)
	if err != nil {
		t.Fatalf("test user id: %v", err)
	}
	if err := s.users.Create(ctx, &User{ID: id, Username: "mara", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("creating the user: %v", err)
	}

	first := openSocket(t, s, user, chID)
	second := openSocket(t, s, user, chID)
	nextEvent(t, watcher)

	closeSocket(t, s, first)
	if stored, err := s.users.GetByUsername(ctx, "mara"); err != nil {
		t.Fatalf("reading the user: %v", err)
	} else if stored.LastSeenAt != nil {
		t.Fatalf("last seen %v after closing one of two sockets, want none", stored.LastSeenAt)
	}

	closeSocket(t, s, second)
	ev := nextEvent(t, watcher)
	if ev.Online || ev.LastSeen == nil {
		t.Fatalf("event %+v, want offline with a last seen", ev)
	}

	stored, err := s.users.GetByUsername(ctx, "mara")
	if err != nil {
		t.Fatalf("reading the user: %v", err)
	}
	if stored.LastSeenAt == nil {
		t.Fatal("nothing was written to the user")
	}
	// Mongo keeps milliseconds, the event carries what Go had.
	if d := stored.LastSeenAt.Sub(*ev.LastSeen); d > time.Millisecond || d < -time.Millisecond {
		t.Fatalf("stored %v, event %v: %v apart", stored.LastSeenAt, ev.LastSeen, d)
	}
}

// An online event says nothing about last seen: the user is here, and the
// client has no use for when they were last here.
func TestPresenceOnlineCarriesNoLastSeen(t *testing.T) {
	s, watcher, user, chID := presenceSetup(t)

	openSocket(t, s, user, chID)

	if ev := nextEvent(t, watcher); !ev.Online || ev.LastSeen != nil {
		t.Fatalf("event %+v, want online without a last seen", ev)
	}
}

// otherNode is a second process against the same Redis: it has its own node id
// and its own idea of who is beating.
func otherNode(t *testing.T, p *presence) *presence {
	t.Helper()

	other := newPresence(p.rdb)
	t.Cleanup(func() {
		ctx := context.Background()
		other.rdb.Del(ctx, presenceNodeKey+other.nodeID, presenceAliveKey+other.nodeID)
		other.rdb.SRem(ctx, presenceNodesKey, other.nodeID)
	})
	return other
}

// A socket on a node that stopped beating is not an online user, and a reader
// sees that as soon as the pulse expires, without waiting for the sweep.
func TestPresenceLookupIgnoresADeadNode(t *testing.T) {
	ctx := context.Background()
	p, user := testPresence(t)
	live := otherNode(t, p)

	connect(t, p, user, "a")

	if _, err := live.beat(ctx); err != nil {
		t.Fatalf("beating: %v", err)
	}
	if _, err := live.listen(ctx); err != nil {
		t.Fatalf("listening: %v", err)
	}

	// p never beat, so to the other node its sockets are leftovers.
	states, err := live.lookup(ctx, []string{user})
	if err != nil {
		t.Fatalf("looking up: %v", err)
	}
	if states[user].Online {
		t.Fatal("a socket of a silent node counts as online")
	}
	// To itself a node is always alive.
	if state := lookupOne(t, p, user); !state.Online {
		t.Fatal("a node does not see its own socket")
	}
}

// The dead node goes to exactly one cleaner, whoever asks first.
func TestPresenceClaimsADeadNodeOnce(t *testing.T) {
	ctx := context.Background()
	p, user := testPresence(t)
	first := otherNode(t, p)
	second := otherNode(t, p)

	connect(t, p, user, "a")
	// p is in the registry but never beats: that is what a killed process
	// looks like once its pulse has expired.
	if err := p.rdb.SAdd(ctx, presenceNodesKey, p.nodeID).Err(); err != nil {
		t.Fatalf("registering the node: %v", err)
	}

	for _, node := range []*presence{first, second} {
		if _, err := node.beat(ctx); err != nil {
			t.Fatalf("beating: %v", err)
		}
	}

	dead, err := first.listen(ctx)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	if !slices.Contains(dead, p.nodeID) {
		t.Fatalf("dead nodes %v, want %s among them", dead, p.nodeID)
	}

	mine, err := first.claim(ctx, p.nodeID)
	if err != nil || !mine {
		t.Fatalf("first claim: %v %v, want true", mine, err)
	}
	if mine, err := second.claim(ctx, p.nodeID); err != nil || mine {
		t.Fatalf("second claim: %v %v, want false", mine, err)
	}

	sockets, err := first.socketsOf(ctx, p.nodeID)
	if err != nil {
		t.Fatalf("reading the sockets: %v", err)
	}
	if len(sockets) != 1 {
		t.Fatalf("the dead node held %v, want one socket", sockets)
	}

	id, socketID, _ := strings.Cut(sockets[0], ":")
	if id != user {
		t.Fatalf("socket of %s, want %s", id, user)
	}
	state, changed, err := first.remove(ctx, id, socketID)
	if err != nil || !changed || state.Online {
		t.Fatalf("removing it: %+v changed=%v err=%v, want an offline change", state, changed, err)
	}

	first.forget(ctx, p.nodeID)
	if left, _ := first.socketsOf(ctx, p.nodeID); len(left) != 0 {
		t.Fatalf("%v left behind the sweep", left)
	}
}

// Redis losing everything, or another node sweeping this one as dead, both look
// the same from here: the registry no longer knows us.
func TestPresenceBeatNoticesBeingForgotten(t *testing.T) {
	ctx := context.Background()
	p, _ := testPresence(t)

	if known, err := p.beat(ctx); err != nil || known {
		t.Fatalf("first beat: known=%v err=%v, want false", known, err)
	}
	if known, err := p.beat(ctx); err != nil || !known {
		t.Fatalf("second beat: known=%v err=%v, want true", known, err)
	}
	if p.epochValue() <= 0 {
		t.Fatalf("epoch %d, want the clock", p.epochValue())
	}

	if err := p.rdb.SRem(ctx, presenceNodesKey, p.nodeID).Err(); err != nil {
		t.Fatalf("forgetting the node: %v", err)
	}
	if known, err := p.beat(ctx); err != nil || known {
		t.Fatalf("beat after being forgotten: known=%v err=%v, want false", known, err)
	}
}
