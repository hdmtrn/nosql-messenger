package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
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
	AvatarID  *bson.ObjectID  `bson:"avatar_id,omitempty" json:"avatar_id,omitempty"`

	// MemberCount survives the projection that drops the member list itself.
	MemberCount int `bson:"member_count,omitempty" json:"member_count,omitempty"`

	// DirectKey is the sorted pair of participants, which makes "the conversation
	// between these two" a value the database can enforce as unique.
	DirectKey string `bson:"direct_key,omitempty" json:"-"`

	// InviteCode is the one link into a named channel. Resetting replaces it,
	// so the old link stops working the moment the new one exists.
	InviteCode string `bson:"invite_code,omitempty" json:"-"`

	// DeletedAt is set by the last member leaving; the purge job removes the
	// channel and its data once the mark is old enough.
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"-"`
}

var (
	errChannelNotFound = errors.New("channel not found")
	errNotMember       = errors.New("not a member of this channel")
	errAlreadyMember   = errors.New("already a member of this channel")
	errNotJoinable     = errors.New("channel cannot be joined")
	errNotOwner        = errors.New("only the owner can do this")
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
		{
			Keys:    bson.D{{Key: "invite_code", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true),
		},
		// Serves the purge job's claim; only marked channels are in it.
		{
			Keys:    bson.D{{Key: "deleted_at", Value: 1}},
			Options: options.Index().SetSparse(true),
		},
	})
	return err
}

func (s *channelStore) Create(ctx context.Context, name string, creator Session) (Channel, error) {
	now := time.Now()
	ch := Channel{
		Kind:       channelKindNamed,
		Name:       name,
		CreatedBy:  creator.UserID,
		CreatedAt:  now,
		InviteCode: rand.Text(),
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

// AddMember only ever grows a named channel. A direct conversation is defined
// by exactly the two people in it — its direct_key is their sorted pair — so a
// third member would leave the document contradicting its own key. The rule
// lives here rather than in the callers so that every future way of joining
// inherits it.
func (s *channelStore) AddMember(ctx context.Context, channelID bson.ObjectID, u Session) error {
	res, err := s.col.UpdateOne(ctx,
		bson.M{
			"_id":             channelID,
			"kind":            channelKindNamed,
			"members.user_id": bson.M{"$ne": u.UserID},
			"deleted_at":      bson.M{"$exists": false},
		},
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
		return s.whyNotAdded(ctx, channelID, u.UserID)
	}
	return nil
}

// whyNotAdded turns "the filter matched nothing" back into the reason it
// matched nothing. It costs a second read, but only on the path that already
// failed.
func (s *channelStore) whyNotAdded(ctx context.Context, channelID, userID bson.ObjectID) error {
	var ch Channel
	err := s.col.FindOne(ctx, bson.M{"_id": channelID},
		options.FindOne().SetProjection(bson.M{"kind": 1, "members.user_id": 1, "deleted_at": 1}),
	).Decode(&ch)
	if errors.Is(err, mongo.ErrNoDocuments) || (err == nil && ch.DeletedAt != nil) {
		return errChannelNotFound
	}
	if err != nil {
		return fmt.Errorf("checking channel: %w", err)
	}
	if ch.Kind != channelKindNamed {
		return errNotJoinable
	}
	// Named, and it exists: the only clause left for the filter to have
	// rejected is the one saying the member is not already there.
	return errAlreadyMember
}

// InviteCode answers, in one read, the questions the link endpoint asks:
// whether this person is inside the channel, whether it is the sort of channel
// that has a link at all, and what the link is. Only a direct conversation is
// refused: a third person in it would contradict its direct_key.
func (s *channelStore) InviteCode(ctx context.Context, channelID, userID bson.ObjectID) (string, error) {
	var ch Channel
	err := s.col.FindOne(ctx,
		bson.M{"_id": channelID, "members.user_id": userID},
		options.FindOne().SetProjection(bson.M{"kind": 1, "invite_code": 1}),
	).Decode(&ch)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", errNotMember
	}
	if err != nil {
		return "", fmt.Errorf("reading invite code: %w", err)
	}
	if ch.Kind != channelKindNamed {
		return "", errNotJoinable
	}
	if ch.InviteCode != "" {
		return ch.InviteCode, nil
	}
	return s.giveInviteCode(ctx, channelID)
}

// giveInviteCode is for a channel made before the code lived on the channel.
// $ifNull keeps a code another request set in the meantime, so two first
// asks agree on one link instead of the second one silently replacing it.
func (s *channelStore) giveInviteCode(ctx context.Context, channelID bson.ObjectID) (string, error) {
	var ch Channel
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{"_id": channelID},
		mongo.Pipeline{{{Key: "$set", Value: bson.M{
			"invite_code": bson.M{"$ifNull": bson.A{"$invite_code", rand.Text()}},
		}}}},
		options.FindOneAndUpdate().
			SetReturnDocument(options.After).
			SetProjection(bson.M{"invite_code": 1}),
	).Decode(&ch)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", errChannelNotFound
	}
	if err != nil {
		return "", fmt.Errorf("giving an invite code: %w", err)
	}
	return ch.InviteCode, nil
}

// ResetInviteCode replaces the link with a new one. The owner check is in the
// filter, as in SetAvatar, and a direct conversation has no owner, so it never
// matches.
func (s *channelStore) ResetInviteCode(ctx context.Context, channelID, ownerID bson.ObjectID) (string, error) {
	var ch Channel
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{
			"_id":     channelID,
			"members": bson.M{"$elemMatch": bson.M{"user_id": ownerID, "role": roleOwner}},
		},
		bson.M{"$set": bson.M{"invite_code": rand.Text()}},
		options.FindOneAndUpdate().
			SetReturnDocument(options.After).
			SetProjection(bson.M{"invite_code": 1}),
	).Decode(&ch)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", s.whyNotOwner(ctx, channelID, ownerID)
	}
	if err != nil {
		return "", fmt.Errorf("resetting invite code: %w", err)
	}
	return ch.InviteCode, nil
}

// ByInviteCode is the channel a link leads to. A code that was reset is
// simply not on any channel any more, the same as one that never existed.
func (s *channelStore) ByInviteCode(ctx context.Context, code string) (bson.ObjectID, error) {
	var ch Channel
	err := s.col.FindOne(ctx,
		bson.M{"invite_code": code},
		options.FindOne().SetProjection(bson.M{"_id": 1}),
	).Decode(&ch)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return bson.ObjectID{}, errChannelNotFound
	}
	if err != nil {
		return bson.ObjectID{}, fmt.Errorf("looking up invite code: %w", err)
	}
	return ch.ID, nil
}

// SetAvatar is users.SetAvatar for a channel, with the owner check inside the
// filter: $elemMatch requires the same array element to hold both the user and
// the role, so "a member" and "an owner" cannot be satisfied by two different
// people. A direct conversation has no owner, so it never matches.
func (s *channelStore) SetAvatar(ctx context.Context, channelID, ownerID bson.ObjectID, avatar *bson.ObjectID) (*bson.ObjectID, error) {
	update := bson.M{"$unset": bson.M{"avatar_id": ""}}
	if avatar != nil {
		update = bson.M{"$set": bson.M{"avatar_id": *avatar}}
	}

	var before Channel
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{
			"_id":     channelID,
			"members": bson.M{"$elemMatch": bson.M{"user_id": ownerID, "role": roleOwner}},
		},
		update,
		options.FindOneAndUpdate().
			SetReturnDocument(options.Before).
			SetProjection(bson.M{"avatar_id": 1}),
	).Decode(&before)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, s.whyNotOwner(ctx, channelID, ownerID)
	}
	if err != nil {
		return nil, fmt.Errorf("setting channel avatar: %w", err)
	}
	return before.AvatarID, nil
}

// whyNotOwner separates "not yours to change" from "not yours to see": an
// outsider gets the same answer as for a channel that does not exist.
func (s *channelStore) whyNotOwner(ctx context.Context, channelID, userID bson.ObjectID) error {
	member, err := s.IsMember(ctx, channelID, userID)
	if err != nil {
		return err
	}
	if !member {
		return errNotMember
	}
	return errNotOwner
}

// Leave takes the member out with the same write that decides the channel's
// fate: the last one out marks it deleted. Nothing is deleted here; the purge
// job does that once the mark is old enough. The mark has to come with the
// emptying write, not after it, since it is what AddMember refuses.
func (s *channelStore) Leave(ctx context.Context, channelID, userID bson.ObjectID) error {
	empty := bson.M{"$eq": bson.A{bson.M{"$size": "$members"}, 0}}
	var ch Channel
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{"_id": channelID, "members.user_id": userID},
		mongo.Pipeline{
			{{Key: "$set", Value: bson.M{"members": bson.M{"$filter": bson.M{
				"input": "$members",
				"cond":  bson.M{"$ne": bson.A{"$$this.user_id", userID}},
			}}}}},
			// An emptied conversation gives up its direct_key, so the same two
			// people can open a new one while this one waits for the purge.
			{{Key: "$set", Value: bson.M{
				"deleted_at": bson.M{"$cond": bson.A{empty, time.Now(), "$$REMOVE"}},
				"direct_key": bson.M{"$cond": bson.A{empty, "$$REMOVE", "$direct_key"}},
			}}},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&ch)

	if errors.Is(err, mongo.ErrNoDocuments) {
		return errNotMember
	}
	if err != nil {
		return fmt.Errorf("leaving channel: %w", err)
	}

	// A marked channel keeps its code until the purge: AddMember refuses it,
	// so the link already answers "not found".
	if ch.DeletedAt == nil {
		return s.ensureOwner(ctx, channelID)
	}
	return nil
}

// ensureOwner hands the role to the earliest member when nobody holds it.
// The check is the filter rather than the member list Leave got back: that
// list is stale by the time the update runs, and the member it names may
// have left in between.
func (s *channelStore) ensureOwner(ctx context.Context, channelID bson.ObjectID) error {
	_, err := s.col.UpdateOne(ctx,
		bson.M{
			"_id":          channelID,
			"members.0":    bson.M{"$exists": true},
			"members.role": bson.M{"$ne": roleOwner},
		},
		bson.M{"$set": bson.M{"members.0.role": roleOwner}},
	)
	if err != nil {
		return fmt.Errorf("promoting owner: %w", err)
	}
	return nil
}

// claimDiscarded hands owner one marked channel old enough to purge, until
// the claim runs out. Checking that the claim is free and taking it is one
// write, so two nodes never get the same channel; a claim that runs out lets
// another node finish what a dead one started.
func (s *channelStore) claimDiscarded(ctx context.Context, owner string, now time.Time) (bson.ObjectID, bool, error) {
	var ch struct {
		ID bson.ObjectID `bson:"_id"`
	}
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{
			"deleted_at": bson.M{"$lte": now.Add(-purgeGrace)},
			"$or": bson.A{
				bson.M{"purge_until": bson.M{"$exists": false}},
				bson.M{"purge_until": bson.M{"$lte": now}},
			},
		},
		bson.M{"$set": bson.M{"purge_owner": owner, "purge_until": now.Add(purgeLease)}},
		options.FindOneAndUpdate().
			SetSort(bson.D{{Key: "deleted_at", Value: 1}}).
			SetProjection(bson.M{"_id": 1}),
	).Decode(&ch)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return bson.ObjectID{}, false, nil
	}
	if err != nil {
		return bson.ObjectID{}, false, fmt.Errorf("claiming a discarded channel: %w", err)
	}
	return ch.ID, true, nil
}

// renewClaim extends owner's claim, and reports false once another node has
// taken the channel over.
func (s *channelStore) renewClaim(ctx context.Context, channelID bson.ObjectID, owner string, now time.Time) (bool, error) {
	res, err := s.col.UpdateOne(ctx,
		bson.M{"_id": channelID, "purge_owner": owner},
		bson.M{"$set": bson.M{"purge_until": now.Add(purgeLease)}},
	)
	if err != nil {
		return false, fmt.Errorf("renewing the purge claim: %w", err)
	}
	return res.MatchedCount == 1, nil
}

// dropDiscarded deletes the marked channel itself, the last step of a purge,
// and only for the node still holding the claim: one whose claim ran out must
// not remove the record of work another node is still doing.
func (s *channelStore) dropDiscarded(ctx context.Context, channelID bson.ObjectID, owner string) (bool, error) {
	res, err := s.col.DeleteOne(ctx, bson.M{
		"_id":         channelID,
		"purge_owner": owner,
		"deleted_at":  bson.M{"$exists": true},
	})
	if err != nil {
		return false, fmt.Errorf("deleting the discarded channel: %w", err)
	}
	return res.DeletedCount == 1, nil
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

// channelsOf is where to announce something about a user: their channels are
// who displays them. A socket of our own carries its channels in the hub, so
// this is for the paths that have no socket at hand — the presence sweep and a
// changed profile.
func (s *server) channelsOf(ctx context.Context, userID string) []string {
	id, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		log.Printf("presence: unreadable user id %q", userID)
		return nil
	}

	chans, err := s.channels.ForUser(ctx, id, bson.ObjectID{}, channelsMaxLimit)
	if err != nil {
		log.Printf("presence: listing the channels of %s: %v", userID, err)
		return nil
	}

	ids := make([]string, 0, len(chans))
	for _, ch := range chans {
		ids = append(ids, ch.ID.Hex())
	}
	return ids
}

// SharingAChannelWith narrows a list of people down to those the user actually
// meets somewhere. The pair of $match clauses is served by the multikey index on
// members.user_id, and the unwind is what makes the answer the ids themselves
// rather than every member of every channel they have in common.
func (s *channelStore) SharingAChannelWith(ctx context.Context, userID bson.ObjectID, ids []bson.ObjectID) (map[bson.ObjectID]bool, error) {
	seen := map[bson.ObjectID]bool{}
	if len(ids) == 0 {
		return seen, nil
	}

	cur, err := s.col.Aggregate(ctx, []bson.M{
		{"$match": bson.M{"$and": []bson.M{
			{"members.user_id": userID},
			{"members.user_id": bson.M{"$in": ids}},
		}}},
		{"$project": bson.M{"members.user_id": 1}},
		{"$unwind": "$members"},
		{"$match": bson.M{"members.user_id": bson.M{"$in": ids}}},
		{"$group": bson.M{"_id": "$members.user_id"}},
	})
	if err != nil {
		return nil, fmt.Errorf("listing people sharing a channel: %w", err)
	}

	var rows []struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("decoding people sharing a channel: %w", err)
	}
	for _, row := range rows {
		seen[row.ID] = true
	}
	return seen, nil
}
