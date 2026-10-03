package main

import (
	"context"
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

	discardPurgeTimeout = 2 * time.Minute
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

	// Runs between Leave seeing the channel empty and discard, for tests that
	// need someone to join exactly there. Nil outside tests.
	beforeDiscard func()
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
		options.FindOne().SetProjection(bson.M{"kind": 1, "members.user_id": 1}),
	).Decode(&ch)
	if errors.Is(err, mongo.ErrNoDocuments) {
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

// KindForMember answers both questions the invite endpoints ask, in one read:
// whether this person is inside the channel, and whether it is the sort of
// channel that has invites at all.
func (s *channelStore) KindForMember(ctx context.Context, channelID, userID bson.ObjectID) (string, error) {
	var ch Channel
	err := s.col.FindOne(ctx,
		bson.M{"_id": channelID, "members.user_id": userID},
		options.FindOne().SetProjection(bson.M{"kind": 1}),
	).Decode(&ch)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", errNotMember
	}
	if err != nil {
		return "", fmt.Errorf("reading channel kind: %w", err)
	}
	return ch.Kind, nil
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

// Leave pulls the member out and keeps the channel coherent afterwards: an
// owner who leaves hands the role to the earliest remaining member, and a
// channel nobody is left in goes away together with its messages.
func (s *channelStore) Leave(ctx context.Context, messages *messageStore, invites *inviteStore, media *mediaStore, channelID, userID bson.ObjectID) error {
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
		if s.beforeDiscard != nil {
			s.beforeDiscard()
		}
		discarded, err := s.discard(ctx, messages, invites, media, channelID)
		if err != nil || discarded {
			return err
		}
		// Someone joined in between: the channel lives on and needs an owner.
	}
	return s.ensureOwner(ctx, channelID)
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

// discard drops a channel nobody is left in, with everything that belongs to it.
// Deleting the channel document is the commit point, and only while it is still
// empty: someone may have joined since Leave saw it empty. Past that point the
// rest is unreachable, so a failure leaves garbage rather than a broken channel,
// and the orphan sweep collects it.
//
// The picture files go last, and only those nothing else points at: a forwarded
// copy may still name the same bytes. It reports whether the channel went.
func (s *channelStore) discard(ctx context.Context, messages *messageStore, invites *inviteStore, media *mediaStore, channelID bson.ObjectID) (bool, error) {
	res, err := s.col.DeleteOne(ctx, bson.M{"_id": channelID, "members": bson.M{"$size": 0}})
	if err != nil {
		return false, fmt.Errorf("discarding empty channel: %w", err)
	}
	if res.DeletedCount == 0 {
		return false, nil
	}

	// The client leaving must not stop the cleanup halfway: the decision is made.
	// Nor may a stalled database hold the request forever; the sweep finishes.
	purgeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discardPurgeTimeout)
	defer cancel()
	if err := s.purge(purgeCtx, messages, invites, media, channelID); err != nil {
		log.Printf("discarding channel %s: %v", channelID.Hex(), err)
	}
	return true, nil
}

// purge deletes what belongs to a channel that is already gone. Every step can
// be repeated, so the orphan sweep can finish what a broken discard began.
func (s *channelStore) purge(ctx context.Context, messages *messageStore, invites *inviteStore, media *mediaStore, channelID bson.ObjectID) error {
	var errs []error
	if _, err := messages.col.DeleteMany(ctx, bson.M{"channel_id": channelID}); err != nil {
		errs = append(errs, fmt.Errorf("messages: %w", err))
	}
	if _, err := invites.col.DeleteMany(ctx, bson.M{"channel_id": channelID}); err != nil {
		errs = append(errs, fmt.Errorf("invites: %w", err))
	}
	files, err := media.deleteForChannel(ctx, channelID)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	if err := media.DeleteUnreferencedFiles(ctx, files); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// missingFrom returns the channel ids col still refers to that no channel has.
func (s *channelStore) missingFrom(ctx context.Context, col *mongo.Collection) ([]bson.ObjectID, error) {
	var ids []bson.ObjectID
	if err := col.Distinct(ctx, "channel_id", bson.M{"channel_id": bson.M{"$ne": nil}}).Decode(&ids); err != nil {
		return nil, fmt.Errorf("listing channels named in %s: %w", col.Name(), err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	cur, err := s.col.Find(ctx, bson.M{"_id": bson.M{"$in": ids}}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, fmt.Errorf("finding channels: %w", err)
	}
	var found []struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := cur.All(ctx, &found); err != nil {
		return nil, fmt.Errorf("decoding channels: %w", err)
	}

	exists := make(map[bson.ObjectID]bool, len(found))
	for _, f := range found {
		exists[f.ID] = true
	}
	var missing []bson.ObjectID
	for _, id := range ids {
		if !exists[id] {
			missing = append(missing, id)
		}
	}
	return missing, nil
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
