package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func fileStored(t *testing.T, media *mediaStore, id bson.ObjectID) bool {
	t.Helper()
	n, err := media.db.Collection("fs.files").CountDocuments(context.Background(), bson.M{"_id": id})
	if err != nil {
		t.Fatalf("counting files: %v", err)
	}
	return n > 0
}

func countIn(t *testing.T, col *mongo.Collection, filter bson.M) int64 {
	t.Helper()
	n, err := col.CountDocuments(context.Background(), filter)
	if err != nil {
		t.Fatalf("counting %s: %v", col.Name(), err)
	}
	return n
}

// ageMark moves a channel's mark back past the grace, as if it had waited.
func ageMark(t *testing.T, channels *channelStore, id bson.ObjectID) {
	t.Helper()
	_, err := channels.col.UpdateOne(context.Background(), bson.M{"_id": id},
		bson.M{"$set": bson.M{"deleted_at": time.Now().Add(-purgeGrace - time.Minute)}})
	if err != nil {
		t.Fatalf("ageing the mark: %v", err)
	}
}

// purgeMarked runs the purge job over channels whose mark has waited out the grace.
func purgeMarked(t *testing.T, s *server, ids ...bson.ObjectID) {
	t.Helper()
	for _, id := range ids {
		ageMark(t, s.channels, id)
	}
	if err := s.purgeDiscarded(context.Background(), "test-node", purgeBatch); err != nil {
		t.Fatalf("purging: %v", err)
	}
}

type purgeFixture struct {
	s     *server
	alice Session
}

func newPurgeFixture(t *testing.T) purgeFixture {
	t.Helper()
	db := testDB(t)
	s := &server{
		channels: newChannelStore(db),
		messages: newMessageStore(db),
		invites:  newInviteStore(db),
		media:    newMediaStore(db),
	}
	if err := s.channels.ensureIndexes(context.Background()); err != nil {
		t.Fatalf("channel indexes: %v", err)
	}
	if err := s.media.ensureIndexes(context.Background()); err != nil {
		t.Fatalf("media indexes: %v", err)
	}
	return purgeFixture{s: s, alice: person("alice")}
}

// marked makes a channel holding n messages and one attached picture, and has
// its only member leave it.
func (f purgeFixture) marked(t *testing.T, name string, n int) (Channel, Media) {
	t.Helper()
	ctx := context.Background()
	ch := createChannel(t, f.s.channels, name, f.alice)
	for i := range n {
		msg := Message{ChannelID: ch.ID, Author: authorOf(f.alice), Text: "hi", ClientMsgID: name + string(rune('a'+i))}
		if _, err := f.s.messages.Insert(ctx, msg); err != nil {
			t.Fatalf("inserting message: %v", err)
		}
	}
	pic, err := f.s.media.Save(ctx, f.alice.UserID, mediaKindAttachment, nil, bytes.NewReader(testPNG(t, 2, 2)))
	if err != nil {
		t.Fatalf("saving picture: %v", err)
	}
	if _, err := f.s.media.Attach(ctx, []bson.ObjectID{pic.ID}, f.alice.UserID, ch.ID); err != nil {
		t.Fatalf("attaching: %v", err)
	}
	if err := f.s.channels.Leave(ctx, f.s.invites, ch.ID, f.alice.UserID); err != nil {
		t.Fatalf("leaving: %v", err)
	}
	return ch, pic
}

func TestPurgeRemovesAMarkedChannelWithEverythingInIt(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	ch, pic := f.marked(t, "doomed", 5)

	// What lands after the mark: a message that passed its membership check a
	// moment before, and an invite Leave failed to delete.
	if _, err := f.s.messages.Insert(ctx, Message{ChannelID: ch.ID, Author: authorOf(f.alice), Text: "late"}); err != nil {
		t.Fatalf("inserting the late message: %v", err)
	}
	if _, err := f.s.invites.col.InsertOne(ctx, bson.M{"channel_id": ch.ID, "code": "stray"}); err != nil {
		t.Fatalf("inserting the stray invite: %v", err)
	}

	ageMark(t, f.s.channels, ch.ID)
	// Batches of two make the history take several, each renewing the claim.
	if err := f.s.purgeDiscarded(ctx, "A", 2); err != nil {
		t.Fatalf("purging: %v", err)
	}

	if n := countIn(t, f.s.channels.col, bson.M{"_id": ch.ID}); n != 0 {
		t.Fatalf("the channel itself was not deleted")
	}
	for _, col := range []*mongo.Collection{f.s.messages.col, f.s.invites.col, f.s.media.col} {
		if n := countIn(t, col, bson.M{"channel_id": ch.ID}); n != 0 {
			t.Fatalf("%d %s outlived the purge", n, col.Name())
		}
	}
	if fileStored(t, f.s.media, pic.FileID) {
		t.Fatalf("the channel's picture is still stored")
	}
}

func TestPurgeWaitsOutTheGrace(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	ch, _ := f.marked(t, "fresh", 1)

	if err := f.s.purgeDiscarded(ctx, "A", purgeBatch); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if n := countIn(t, f.s.messages.col, bson.M{"channel_id": ch.ID}); n != 1 {
		t.Fatalf("a mark younger than the grace was purged: %d of 1 messages left", n)
	}
}

func TestClaimsSplitChannelsBetweenNodes(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	one, _ := f.marked(t, "one", 0)
	two, _ := f.marked(t, "two", 0)
	ageMark(t, f.s.channels, one.ID)
	ageMark(t, f.s.channels, two.ID)

	now := time.Now()
	a, ok, err := f.s.channels.claimDiscarded(ctx, "A", now)
	if err != nil || !ok {
		t.Fatalf("node A got nothing: ok %v, err %v", ok, err)
	}
	b, ok, err := f.s.channels.claimDiscarded(ctx, "B", now)
	if err != nil || !ok {
		t.Fatalf("node B got nothing: ok %v, err %v", ok, err)
	}
	if a == b {
		t.Fatalf("both nodes got channel %s", a.Hex())
	}
	if _, ok, err := f.s.channels.claimDiscarded(ctx, "C", now); err != nil || ok {
		t.Fatalf("node C got a channel both others hold: ok %v, err %v", ok, err)
	}
}

func TestPurgeTakesOverAClaimThatRanOut(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	ch, _ := f.marked(t, "abandoned", 2)
	ageMark(t, f.s.channels, ch.ID)

	// Node A claimed it and died, and its claim ran out a minute ago.
	if _, ok, err := f.s.channels.claimDiscarded(ctx, "A", time.Now()); err != nil || !ok {
		t.Fatalf("node A could not claim: ok %v, err %v", ok, err)
	}
	if _, err := f.s.channels.col.UpdateOne(ctx, bson.M{"_id": ch.ID},
		bson.M{"$set": bson.M{"purge_until": time.Now().Add(-time.Minute)}}); err != nil {
		t.Fatalf("running out node A's claim: %v", err)
	}
	if err := f.s.purgeDiscarded(ctx, "B", purgeBatch); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if n := countIn(t, f.s.channels.col, bson.M{"_id": ch.ID}); n != 0 {
		t.Fatalf("a channel whose claim ran out was left for the dead node")
	}
}

func TestPurgeLeavesALiveClaimAlone(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	ch, _ := f.marked(t, "busy", 2)
	ageMark(t, f.s.channels, ch.ID)

	if _, ok, err := f.s.channels.claimDiscarded(ctx, "A", time.Now()); err != nil || !ok {
		t.Fatalf("node A could not claim: ok %v, err %v", ok, err)
	}
	if err := f.s.purgeDiscarded(ctx, "B", purgeBatch); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if n := countIn(t, f.s.messages.col, bson.M{"channel_id": ch.ID}); n != 2 {
		t.Fatalf("node B worked on a channel node A holds: %d of 2 messages left", n)
	}
}

func TestAStaleOwnerDoesNotDropTheChannel(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	ch, _ := f.marked(t, "contested", 3)
	ageMark(t, f.s.channels, ch.ID)

	if _, ok, err := f.s.channels.claimDiscarded(ctx, "A", time.Now()); err != nil || !ok {
		t.Fatalf("node A could not claim: ok %v, err %v", ok, err)
	}
	// Node A stalls past its claim, and node B takes the channel over.
	if _, err := f.s.channels.col.UpdateOne(ctx, bson.M{"_id": ch.ID},
		bson.M{"$set": bson.M{"purge_owner": "B", "purge_until": time.Now().Add(purgeLease)}}); err != nil {
		t.Fatalf("handing the claim to B: %v", err)
	}

	if err := f.s.purgeChannel(ctx, ch.ID, "A", 1); !errors.Is(err, errClaimLost) {
		t.Fatalf("node A carried on after losing the claim: got %v, want errClaimLost", err)
	}
	// It notices at the first renewal, one batch in.
	if n := countIn(t, f.s.messages.col, bson.M{"channel_id": ch.ID}); n != 2 {
		t.Fatalf("node A kept deleting after losing the claim: %d of 3 messages left, want 2", n)
	}
	if dropped, err := f.s.channels.dropDiscarded(ctx, ch.ID, "A"); err != nil || dropped {
		t.Fatalf("node A deleted a channel node B holds: dropped %v, err %v", dropped, err)
	}
	if n := countIn(t, f.s.channels.col, bson.M{"_id": ch.ID}); n != 1 {
		t.Fatalf("the channel is gone while node B still works on it")
	}
}

func TestPurgeFinishesAFileDeleteCutShort(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	ch, pic := f.marked(t, "cut", 0)

	// GridFS deletes the file document before its chunks; a crash in between
	// leaves the chunks, and the record still names the file.
	if _, err := f.s.media.db.Collection("fs.files").DeleteOne(ctx, bson.M{"_id": pic.FileID}); err != nil {
		t.Fatalf("deleting the file document: %v", err)
	}
	purgeMarked(t, f.s, ch.ID)

	if n := countIn(t, f.s.media.files.GetChunksCollection(), bson.M{"files_id": pic.FileID}); n != 0 {
		t.Fatalf("%d chunks of a half-deleted file outlived the purge", n)
	}
	if n := countIn(t, f.s.media.col, bson.M{"channel_id": ch.ID}); n != 0 {
		t.Fatalf("%d media records outlived the purge", n)
	}
}

func TestSweepLeavesYoungUnreferencedFilesAlone(t *testing.T) {
	ctx := context.Background()
	media := newMediaStore(testDB(t))

	stray, err := media.files.UploadFromStream(ctx, "", bytes.NewReader(testPNG(t, 2, 2)))
	if err != nil {
		t.Fatalf("uploading: %v", err)
	}
	kept, err := media.Save(ctx, person("alice").UserID, mediaKindAvatar, nil, bytes.NewReader(testPNG(t, 2, 2)))
	if err != nil {
		t.Fatalf("saving: %v", err)
	}

	// Just uploaded and not yet named by a record: what Save looks like midway.
	if n, err := media.deleteUnreferencedBefore(ctx, time.Now().Add(-fileMinAge), fileSweepLimit); err != nil || n != 0 {
		t.Fatalf("young file: deleted %d, err %v; want 0", n, err)
	}
	if !fileStored(t, media, stray) {
		t.Fatalf("a file young enough to be an upload in progress was deleted")
	}

	if n, err := media.deleteUnreferencedBefore(ctx, time.Now().Add(time.Minute), fileSweepLimit); err != nil || n != 1 {
		t.Fatalf("old file: deleted %d, err %v; want 1", n, err)
	}
	if fileStored(t, media, stray) {
		t.Fatalf("an old file no record names is still stored")
	}
	if !fileStored(t, media, kept.FileID) {
		t.Fatalf("a file a record names was deleted")
	}
}

func TestFileSweepStopsAtTheLimit(t *testing.T) {
	ctx := context.Background()
	media := newMediaStore(testDB(t))

	var strays []bson.ObjectID
	for range 3 {
		id, err := media.files.UploadFromStream(ctx, "", bytes.NewReader(testPNG(t, 2, 2)))
		if err != nil {
			t.Fatalf("uploading: %v", err)
		}
		strays = append(strays, id)
	}

	// Three strays against a limit of two: a broken media collection, not a crash.
	if n, err := media.deleteUnreferencedBefore(ctx, time.Now().Add(time.Minute), 2); err == nil || n != 0 {
		t.Fatalf("over the limit: deleted %d, err %v; want 0 and an error", n, err)
	}
	for _, id := range strays {
		if !fileStored(t, media, id) {
			t.Fatalf("a file was deleted past the limit")
		}
	}
	if n, err := media.deleteUnreferencedBefore(ctx, time.Now().Add(time.Minute), 3); err != nil || n != 3 {
		t.Fatalf("at the limit: deleted %d, err %v; want 3", n, err)
	}
}

func TestOneNodeTakesARound(t *testing.T) {
	ctx := context.Background()
	first, _ := testPresence(t)
	second, _ := testPresence(t)
	// A key of its own: the tests share Redis with whatever runs on the machine.
	key := "test:round:" + bson.NewObjectID().Hex()
	t.Cleanup(func() { first.rdb.Del(ctx, key) })

	if ok, err := first.takeRound(ctx, key, time.Hour); err != nil || !ok {
		t.Fatalf("the first node did not get the round: ok %v, err %v", ok, err)
	}
	if ok, err := second.takeRound(ctx, key, time.Hour); err != nil || ok {
		t.Fatalf("a second node took the same round: ok %v, err %v", ok, err)
	}

	// Gone before the holder's next tick, or that tick would find it and skip.
	ttl, err := first.rdb.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("reading the key's TTL: %v", err)
	}
	if ttl <= 0 || ttl > time.Hour-time.Hour/10 {
		t.Fatalf("the key lives %v, want it gone well before the round ends", ttl)
	}
}

func TestFileSweepGoesAheadWithRedisDown(t *testing.T) {
	nowhere := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { nowhere.Close() })
	if !(&server{presence: newPresence(nowhere)}).takeFileRound(context.Background()) {
		t.Fatalf("with Redis down the node skipped the sweep instead of doing it")
	}
}
