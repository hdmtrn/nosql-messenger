package main

import (
	"bytes"
	"context"
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

func TestSweepFinishesADiscardThatStoppedHalfway(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages, invites, media := newChannelStore(db), newMessageStore(db), newInviteStore(db), newMediaStore(db)
	if err := media.ensureIndexes(ctx); err != nil {
		t.Fatalf("media indexes: %v", err)
	}

	alice := person("alice")
	doomed := createChannel(t, channels, "doomed", alice)
	kept := createChannel(t, channels, "kept", alice)
	for _, ch := range []Channel{doomed, kept} {
		if _, err := messages.Insert(ctx, Message{ChannelID: ch.ID, Author: authorOf(alice), Text: "hi", ClientMsgID: "c1"}); err != nil {
			t.Fatalf("inserting message: %v", err)
		}
	}
	if _, err := invites.Create(ctx, doomed.ID, alice.UserID); err != nil {
		t.Fatalf("creating invite: %v", err)
	}
	save := func() Media {
		t.Helper()
		m, err := media.Save(ctx, alice.UserID, mediaKindAttachment, nil, bytes.NewReader(testPNG(t, 2, 2)))
		if err != nil {
			t.Fatalf("saving: %v", err)
		}
		return m
	}
	forwarded, alone := save(), save()
	if _, err := media.Attach(ctx, []bson.ObjectID{forwarded.ID, alone.ID}, alice.UserID, doomed.ID); err != nil {
		t.Fatalf("attaching: %v", err)
	}
	if _, err := media.CopyTo(ctx, []Attachment{forwarded.Attachment()}, alice.UserID, kept.ID); err != nil {
		t.Fatalf("forwarding: %v", err)
	}

	// discard got past its commit point and the process died there.
	if _, err := channels.col.DeleteOne(ctx, bson.M{"_id": doomed.ID}); err != nil {
		t.Fatalf("deleting the channel: %v", err)
	}

	s := &server{channels: channels, messages: messages, invites: invites, media: media}
	if err := s.sweepOrphans(ctx, time.Now()); err != nil {
		t.Fatalf("sweeping: %v", err)
	}

	count := func(col *mongo.Collection, channelID bson.ObjectID) int64 {
		t.Helper()
		n, err := col.CountDocuments(ctx, bson.M{"channel_id": channelID})
		if err != nil {
			t.Fatalf("counting %s: %v", col.Name(), err)
		}
		return n
	}
	if n := count(messages.col, doomed.ID); n != 0 {
		t.Fatalf("%d messages outlived their channel", n)
	}
	if n := count(invites.col, doomed.ID); n != 0 {
		t.Fatalf("%d invites outlived their channel", n)
	}
	if n := count(media.col, doomed.ID); n != 0 {
		t.Fatalf("%d media records outlived their channel", n)
	}
	if fileStored(t, media, alone.FileID) {
		t.Fatalf("a file used only by the gone channel is still stored")
	}
	if !fileStored(t, media, forwarded.FileID) {
		t.Fatalf("the file behind a forwarded copy was deleted")
	}
	if n := count(messages.col, kept.ID); n != 1 {
		t.Fatalf("the sweep touched a channel that exists: %d messages left of 1", n)
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
	if n, err := media.deleteUnreferencedBefore(ctx, time.Now().Add(-orphanFileMinAge)); err != nil || n != 0 {
		t.Fatalf("young file: deleted %d, err %v; want 0", n, err)
	}
	if !fileStored(t, media, stray) {
		t.Fatalf("a file young enough to be an upload in progress was deleted")
	}

	if n, err := media.deleteUnreferencedBefore(ctx, time.Now().Add(time.Minute)); err != nil || n != 1 {
		t.Fatalf("old file: deleted %d, err %v; want 1", n, err)
	}
	if fileStored(t, media, stray) {
		t.Fatalf("an old file no record names is still stored")
	}
	if !fileStored(t, media, kept.FileID) {
		t.Fatalf("a file a record names was deleted")
	}
}

func TestSweepGoesOnPastACollectionItCannotRead(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages, invites, media := newChannelStore(db), newMessageStore(db), newInviteStore(db), newMediaStore(db)

	alice := person("alice")
	gone := createChannel(t, channels, "gone", alice)
	if _, err := invites.Create(ctx, gone.ID, alice.UserID); err != nil {
		t.Fatalf("creating invite: %v", err)
	}
	if _, err := channels.col.DeleteOne(ctx, bson.M{"_id": gone.ID}); err != nil {
		t.Fatalf("deleting the channel: %v", err)
	}
	// A channel id that is not an ObjectID makes the messages pass fail.
	if _, err := messages.col.InsertOne(ctx, bson.M{"channel_id": "not-an-id", "text": "stray"}); err != nil {
		t.Fatalf("inserting the stray message: %v", err)
	}

	s := &server{channels: channels, messages: messages, invites: invites, media: media}
	if err := s.sweepOrphans(ctx, time.Now()); err == nil {
		t.Fatalf("a collection the sweep could not read went unreported")
	}
	if n, _ := invites.col.CountDocuments(ctx, bson.M{"channel_id": gone.ID}); n != 0 {
		t.Fatalf("one unreadable collection kept the others from being swept: %d invites left", n)
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

func TestOrphanSweepGoesAheadWithRedisDown(t *testing.T) {
	nowhere := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { nowhere.Close() })
	if !(&server{presence: newPresence(nowhere)}).takeOrphanRound(context.Background()) {
		t.Fatalf("with Redis down the node skipped the sweep instead of doing it")
	}
}
