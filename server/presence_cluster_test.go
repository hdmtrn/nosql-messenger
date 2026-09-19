package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// The whole chain in one process: a node dies without a word, another node
// notices the missing pulse, sweeps its sockets, writes last_seen and publishes
// the offline over the real bus, where a socket of that other node receives it.
// What is left to a manual check is an actual process being killed and the ten
// seconds of the ticker.
func TestPresenceSweepReachesAnotherNode(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	users := newUserStore(db)
	channels := newChannelStore(db)

	mara := Session{UserID: bson.NewObjectID(), Username: "mara"}
	if err := users.Create(ctx, &User{ID: mara.UserID, Username: mara.Username}); err != nil {
		t.Fatalf("creating the user: %v", err)
	}
	ch, err := channels.Create(ctx, "general", mara)
	if err != nil {
		t.Fatalf("creating the channel: %v", err)
	}

	// The live node, with a watcher reading the channel Mara is in.
	hub, bus := testInstance(t)
	live := newPresence(bus.rdb)
	node := &server{hub: hub, bus: bus, presence: live, users: users, channels: channels}
	watcher := newSubscriber(bson.NewObjectID().Hex())
	hub.Connect(watcher, []string{ch.ID.Hex()})
	waitForSubscribers(t, bus, channelTopic+ch.ID.Hex(), 1)

	// The node about to die: it registers Mara's socket, joins the registry and
	// then never beats again.
	doomed := newPresence(bus.rdb)
	t.Cleanup(func() {
		bus.rdb.Del(ctx, presenceNodeKey+doomed.nodeID, presenceSocketsKey+mara.UserID.Hex(),
			presenceVersionKey+mara.UserID.Hex(), presenceAliveKey+live.nodeID, presenceNodeKey+live.nodeID)
		bus.rdb.SRem(ctx, presenceNodesKey, doomed.nodeID, live.nodeID)
	})
	if _, _, err := doomed.connect(ctx, mara.UserID.Hex(), "a socket"); err != nil {
		t.Fatalf("connecting on the doomed node: %v", err)
	}
	if err := bus.rdb.SAdd(ctx, presenceNodesKey, doomed.nodeID).Err(); err != nil {
		t.Fatalf("registering the doomed node: %v", err)
	}

	// beat and listen are the round's own steps; the sweep is then asked for
	// directly, because the sweeper key is one for the whole Redis and a
	// messenger running on this machine takes it every ten seconds.
	if _, err := live.beat(ctx); err != nil {
		t.Fatalf("beating: %v", err)
	}
	if _, err := live.listen(ctx); err != nil {
		t.Fatalf("listening: %v", err)
	}
	node.sweepNode(ctx, doomed.nodeID)

	select {
	case raw := <-watcher.send:
		var ev presenceEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("decoding the event: %v", err)
		}
		if ev.Type != "presence" || ev.UserID != mara.UserID.Hex() || ev.Online {
			t.Fatalf("event %+v, want mara offline", ev)
		}
		if ev.LastSeen == nil {
			t.Fatal("the swept event carries no last seen")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event on the live node: the sweep did not reach it")
	}

	stored, err := users.GetByUsername(ctx, mara.Username)
	if err != nil {
		t.Fatalf("reading the user: %v", err)
	}
	if stored.LastSeenAt == nil {
		t.Fatal("the sweep wrote no last seen")
	}

	if left, err := live.socketsOf(ctx, doomed.nodeID); err != nil || len(left) != 0 {
		t.Fatalf("the dead node still holds %v (%v)", left, err)
	}
}
