package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const invitesMaxPerChannel = 20

// Invite is keyed by the code itself, so following one is a primary-key lookup
// and uniqueness comes free with _id rather than a second index.
type Invite struct {
	Code      string        `bson:"_id"        json:"code"`
	ChannelID bson.ObjectID `bson:"channel_id" json:"channel_id"`
	CreatedBy bson.ObjectID `bson:"created_by" json:"created_by"`
	CreatedAt time.Time     `bson:"created_at" json:"created_at"`
}

var (
	errInviteNotFound = errors.New("invite not found")
	errTooManyInvites = errors.New("too many invites for this channel")
)

type inviteStore struct {
	col *mongo.Collection
}

func newInviteStore(db *mongo.Database) *inviteStore {
	return &inviteStore{col: db.Collection("invites")}
}

func (s *inviteStore) ensureIndexes(ctx context.Context) error {
	_, err := s.col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "channel_id", Value: 1}, {Key: "created_at", Value: -1}},
	})
	return err
}

// Create refuses past invitesMaxPerChannel, which is the same number ForChannel
// returns. The two must match: an invite that exists but never appears in the
// list is one nobody can revoke, and it keeps working forever.
//
// Counting and inserting are two operations, so two simultaneous requests can
// both see 19 and leave 21 behind. The cap is here to stop a runaway loop, not
// to be exact, and being one over does not break the property above.
func (s *inviteStore) Create(ctx context.Context, channelID, by bson.ObjectID) (Invite, error) {
	n, err := s.col.CountDocuments(ctx, bson.M{"channel_id": channelID})
	if err != nil {
		return Invite{}, fmt.Errorf("counting invites: %w", err)
	}
	if n >= invitesMaxPerChannel {
		return Invite{}, errTooManyInvites
	}

	inv := Invite{
		Code:      rand.Text(),
		ChannelID: channelID,
		CreatedBy: by,
		CreatedAt: time.Now(),
	}
	if _, err := s.col.InsertOne(ctx, inv); err != nil {
		return Invite{}, fmt.Errorf("creating invite: %w", err)
	}
	return inv, nil
}

func (s *inviteStore) ByCode(ctx context.Context, code string) (Invite, error) {
	var inv Invite
	err := s.col.FindOne(ctx, bson.M{"_id": code}).Decode(&inv)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Invite{}, errInviteNotFound
	}
	if err != nil {
		return Invite{}, fmt.Errorf("looking up invite: %w", err)
	}
	return inv, nil
}

func (s *inviteStore) ForChannel(ctx context.Context, channelID bson.ObjectID) ([]Invite, error) {
	cur, err := s.col.Find(ctx,
		bson.M{"channel_id": channelID},
		options.Find().
			SetSort(bson.D{{Key: "created_at", Value: -1}}).
			SetLimit(invitesMaxPerChannel),
	)
	if err != nil {
		return nil, fmt.Errorf("listing invites: %w", err)
	}
	out := []Invite{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decoding invites: %w", err)
	}
	return out, nil
}

// Revoke deletes one invite. The channel is part of the filter, so a code
// belonging to somewhere else is not found rather than quietly removed.
func (s *inviteStore) Revoke(ctx context.Context, channelID bson.ObjectID, code string) error {
	res, err := s.col.DeleteOne(ctx, bson.M{"_id": code, "channel_id": channelID})
	if err != nil {
		return fmt.Errorf("revoking invite: %w", err)
	}
	if res.DeletedCount == 0 {
		return errInviteNotFound
	}
	return nil
}

func (s *inviteStore) RevokeForChannel(ctx context.Context, channelID bson.ObjectID) error {
	_, err := s.col.DeleteMany(ctx, bson.M{"channel_id": channelID})
	return err
}
