package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	channelKindNamed  = "channel"
	channelKindDirect = "direct"

	roleOwner  = "owner"
	roleMember = "member"

	channelNameMinLen = 1
	channelNameMaxLen = 64

	channelsPageSize = 100
	channelsMaxLimit = 200
)

type ChannelMember struct {
	UserID   bson.ObjectID `bson:"user_id"    json:"user_id"`
	Username string        `bson:"username"   json:"username"`
	Role     string        `bson:"role"       json:"role"`
	JoinedAt time.Time     `bson:"joined_at"  json:"joined_at"`
}

type Channel struct {
	ID        bson.ObjectID   `bson:"_id,omitempty" json:"id"`
	Kind      string          `bson:"kind"          json:"kind"`
	Name      string          `bson:"name"          json:"name"`
	CreatedBy bson.ObjectID   `bson:"created_by"    json:"created_by"`
	CreatedAt time.Time       `bson:"created_at"    json:"created_at"`
	Members   []ChannelMember `bson:"members"       json:"members,omitempty"`

	// MemberCount survives the projection that drops the member list itself.
	MemberCount int `bson:"member_count,omitempty" json:"member_count,omitempty"`

	// DirectKey is the sorted pair of participants, which makes "the conversation
	// between these two" a value the database can enforce as unique.
	DirectKey string `bson:"direct_key,omitempty" json:"-"`
}

var (
	errChannelNotFound = errors.New("channel not found")
	errNotMember       = errors.New("not a member of this channel")
	errAlreadyMember   = errors.New("already a member of this channel")
)

type channelStore struct {
	col *mongo.Collection
}

func newChannelStore(db *mongo.Database) *channelStore {
	return &channelStore{col: db.Collection("channels")}
}

func (s *channelStore) ensureIndexes(ctx context.Context) error {
	_, err := s.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "members.user_id", Value: 1}, {Key: "_id", Value: 1}},
		},
		{
			Keys:    bson.D{{Key: "direct_key", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true),
		},
	})
	return err
}

func (s *channelStore) Create(ctx context.Context, name string, creator Session) (Channel, error) {
	now := time.Now()
	ch := Channel{
		Kind:      channelKindNamed,
		Name:      name,
		CreatedBy: creator.UserID,
		CreatedAt: now,
		Members: []ChannelMember{{
			UserID:   creator.UserID,
			Username: creator.Username,
			Role:     roleOwner,
			JoinedAt: now,
		}},
	}

	res, err := s.col.InsertOne(ctx, ch)
	if err != nil {
		return Channel{}, fmt.Errorf("creating channel: %w", err)
	}
	if oid, ok := res.InsertedID.(bson.ObjectID); ok {
		ch.ID = oid
	}
	return ch, nil
}

func (s *channelStore) ByID(ctx context.Context, id bson.ObjectID) (Channel, error) {
	var ch Channel
	err := s.col.FindOne(ctx, bson.M{"_id": id}).Decode(&ch)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Channel{}, errChannelNotFound
	}
	if err != nil {
		return Channel{}, fmt.Errorf("looking up channel: %w", err)
	}
	return ch, nil
}

func (s *channelStore) ForUser(ctx context.Context, userID bson.ObjectID, after bson.ObjectID, limit int) ([]Channel, error) {
	if limit <= 0 {
		limit = channelsPageSize
	}
	if limit > channelsMaxLimit {
		limit = channelsMaxLimit
	}

	filter := bson.M{"members.user_id": userID}
	if !after.IsZero() {
		filter["_id"] = bson.M{"$gt": after}
	}

	// A named channel needs no member list here; a direct one is displayed as the
	// other participant, so it does. $$REMOVE drops the field per document.
	cur, err := s.col.Aggregate(ctx, []bson.M{
		{"$match": filter},
		{"$sort": bson.M{"_id": 1}},
		{"$limit": limit},
		{"$addFields": bson.M{
			"member_count": bson.M{"$size": "$members"},
			"members": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$kind", channelKindDirect}}, "$members", "$$REMOVE",
			}},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("listing channels: %w", err)
	}
	channels := []Channel{}
	if err := cur.All(ctx, &channels); err != nil {
		return nil, fmt.Errorf("decoding channels: %w", err)
	}
	return channels, nil
}

// InCommon finds the channels both people belong to. $all over the multikey index
// on members.user_id expresses "contains both" directly; with a separate membership
// collection this would be a join of that collection with itself.
func (s *channelStore) InCommon(ctx context.Context, a, b bson.ObjectID) ([]Channel, error) {
	cur, err := s.col.Find(ctx,
		bson.M{
			"kind":            channelKindNamed,
			"members.user_id": bson.M{"$all": bson.A{a, b}},
		},
		options.Find().SetProjection(bson.M{"members": 0}).SetLimit(channelsMaxLimit),
	)
	if err != nil {
		return nil, fmt.Errorf("listing channels in common: %w", err)
	}
	out := []Channel{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decoding channels in common: %w", err)
	}
	return out, nil
}

func (s *channelStore) IsMember(ctx context.Context, channelID, userID bson.ObjectID) (bool, error) {
	err := s.col.FindOne(ctx,
		bson.M{"_id": channelID, "members.user_id": userID},
		options.FindOne().SetProjection(bson.M{"_id": 1}),
	).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking membership: %w", err)
	}
	return true, nil
}

func (s *channelStore) AddMember(ctx context.Context, channelID bson.ObjectID, u Session) error {
	res, err := s.col.UpdateOne(ctx,
		bson.M{"_id": channelID, "members.user_id": bson.M{"$ne": u.UserID}},
		bson.M{"$push": bson.M{"members": ChannelMember{
			UserID:   u.UserID,
			Username: u.Username,
			Role:     roleMember,
			JoinedAt: time.Now(),
		}}},
	)
	if err != nil {
		return fmt.Errorf("adding member: %w", err)
	}
	if res.MatchedCount == 0 {
		exists, cerr := s.exists(ctx, channelID)
		if cerr != nil {
			return cerr
		}
		if exists {
			return errAlreadyMember
		}
		return errChannelNotFound
	}
	return nil
}

// Leave pulls the member out and keeps the channel coherent afterwards: an
// owner who leaves hands the role to the earliest remaining member, and a
// channel nobody is left in goes away together with its messages.
func (s *channelStore) Leave(ctx context.Context, messages *messageStore, invites *inviteStore, channelID, userID bson.ObjectID) error {
	var ch Channel
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{"_id": channelID, "members.user_id": userID},
		bson.M{"$pull": bson.M{"members": bson.M{"user_id": userID}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&ch)

	if errors.Is(err, mongo.ErrNoDocuments) {
		return errNotMember
	}
	if err != nil {
		return fmt.Errorf("leaving channel: %w", err)
	}

	if len(ch.Members) == 0 {
		return s.discard(ctx, messages, invites, channelID)
	}

	for _, m := range ch.Members {
		if m.Role == roleOwner {
			return nil
		}
	}
	_, err = s.col.UpdateOne(ctx,
		bson.M{"_id": channelID, "members.user_id": ch.Members[0].UserID},
		bson.M{"$set": bson.M{"members.$.role": roleOwner}},
	)
	if err != nil {
		return fmt.Errorf("promoting owner: %w", err)
	}
	return nil
}

// discard drops a channel and its messages together. Two collections must go or
// stay as one, which is what the replica set buys us besides change streams.
func (s *channelStore) discard(ctx context.Context, messages *messageStore, invites *inviteStore, channelID bson.ObjectID) error {
	sess, err := s.col.Database().Client().StartSession()
	if err != nil {
		return fmt.Errorf("starting session: %w", err)
	}
	defer sess.EndSession(ctx)

	_, err = sess.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		if _, err := s.col.DeleteOne(ctx, bson.M{"_id": channelID}); err != nil {
			return nil, err
		}
		if _, err := messages.col.DeleteMany(ctx, bson.M{"channel_id": channelID}); err != nil {
			return nil, err
		}
		_, err := invites.col.DeleteMany(ctx, bson.M{"channel_id": channelID})
		return nil, err
	})
	if err != nil {
		return fmt.Errorf("discarding empty channel: %w", err)
	}
	return nil
}

func directKey(a, b bson.ObjectID) string {
	x, y := a.Hex(), b.Hex()
	if x > y {
		x, y = y, x
	}
	return x + ":" + y
}

// Direct returns the conversation between two people, creating it only if there
// is none. Both sides may press "message" at the same moment, so the upsert on
// the unique key does the deciding: one of them inserts, the other finds.
func (s *channelStore) Direct(ctx context.Context, me Session, other *User) (Channel, error) {
	now := time.Now()
	key := directKey(me.UserID, other.ID)

	var ch Channel
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{"direct_key": key},
		bson.M{"$setOnInsert": bson.M{
			"kind":       channelKindDirect,
			"direct_key": key,
			"created_by": me.UserID,
			"created_at": now,
			"members": []ChannelMember{
				{UserID: me.UserID, Username: me.Username, Role: roleMember, JoinedAt: now},
				{UserID: other.ID, Username: other.Username, Role: roleMember, JoinedAt: now},
			},
		}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&ch)
	if err != nil {
		return Channel{}, fmt.Errorf("opening direct channel: %w", err)
	}
	return ch, nil
}

func (s *channelStore) exists(ctx context.Context, id bson.ObjectID) (bool, error) {
	err := s.col.FindOne(ctx, bson.M{"_id": id},
		options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking channel: %w", err)
	}
	return true, nil
}
