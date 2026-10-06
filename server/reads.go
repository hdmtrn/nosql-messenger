package main

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// readStore keeps how far each person has read each channel, as a message
// number. Unread is the channel's last number minus this one, the way
// Mattermost keeps a message count per member: it survives a reload and a
// reconnect, which a count kept in the browser does not.
//
// A nil store does nothing, which is what handler tests that are not about
// reading run with.
type readStore struct {
	col *mongo.Collection
}

func newReadStore(db *mongo.Database) *readStore {
	return &readStore{col: db.Collection("reads")}
}

func (s *readStore) ensureIndexes(ctx context.Context) error {
	_, err := s.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		// The channel list looks reads up by channel, then by person.
		{
			Keys:    bson.D{{Key: "channel_id", Value: 1}, {Key: "user_id", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		// An account erasure deletes a person's.
		{Keys: bson.D{{Key: "user_id", Value: 1}}},
	})
	return err
}

// mark moves a read position forward and never back, so a late request from
// another tab cannot unread what this one has shown.
func (s *readStore) mark(ctx context.Context, userID, channelID bson.ObjectID, seq int64) error {
	if s == nil {
		return nil
	}
	filter := bson.M{"user_id": userID, "channel_id": channelID}
	update := bson.M{"$max": bson.M{"read_seq": seq}}
	opts := options.UpdateOne().SetUpsert(true)
	_, err := s.col.UpdateOne(ctx, filter, update, opts)
	// Two first marks at once both try to insert; the one that lost finds the
	// row the other made.
	if mongo.IsDuplicateKeyError(err) {
		_, err = s.col.UpdateOne(ctx, filter, update)
	}
	if err != nil {
		return fmt.Errorf("marking read: %w", err)
	}
	return nil
}

func (s *readStore) deleteFor(ctx context.Context, filter bson.M) error {
	if s == nil {
		return nil
	}
	if _, err := s.col.DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("deleting read positions: %w", err)
	}
	return nil
}
