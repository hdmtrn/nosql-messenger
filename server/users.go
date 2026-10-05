package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const searchLimit = 20

// Username is the handle: lowercase, unique, and the key other documents embed.
// DisplayName is what people read; it changes, so nothing embeds it.
type User struct {
	ID           bson.ObjectID  `bson:"_id,omitempty"       json:"id"`
	Username     string         `bson:"username"            json:"username"`
	DisplayName  string         `bson:"display_name"        json:"display_name"`
	Bio          string         `bson:"bio,omitempty"       json:"bio"`
	PasswordHash string         `bson:"password_hash"       json:"-"`
	AvatarID     *bson.ObjectID `bson:"avatar_id,omitempty" json:"avatar_id,omitempty"`
	CreatedAt    time.Time      `bson:"created_at"          json:"created_at"`
	// When the user's last socket closed. Written only on that edge, so a
	// second tab closing costs nothing; a user who is online has no use for it.
	// Never encoded here: the profile is public so that search can find a
	// stranger, and GET /presence is where this is answered, to those who may ask.
	LastSeenAt *time.Time `bson:"last_seen_at,omitempty" json:"-"`

	// DeletedAt marks an account its owner deleted. The document stays, under
	// a name nobody can register, so that messages left in shared chats still
	// lead to an author; everything personal in it is gone.
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"-"`
}

// deletedNamePrefix starts the name a deleted account ends up with. Nobody can
// register it, or a name taken in advance would stop a deletion halfway.
const deletedNamePrefix = "deleted-"

func tombName(id bson.ObjectID) string {
	return deletedNamePrefix + id.Hex()
}

// alive is the filter clause for an account that has not been deleted.
var alive = bson.M{"$exists": false}

// normaliseUsername folds the handle so that Mara and mara cannot be two people,
// and so that a search need not ask for a case-insensitive match.
func normaliseUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

var (
	errUsernameTaken = errors.New("username already taken")
	errUserNotFound  = errors.New("user not found")
)

type userStore struct {
	col *mongo.Collection
}

func newUserStore(db *mongo.Database) *userStore {
	return &userStore{col: db.Collection("users")}
}

func (s *userStore) ensureIndexes(ctx context.Context) error {
	_, err := s.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "username", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		// Serves the erasure claim; only accounts still being erased are in it.
		{
			Keys:    bson.D{{Key: "erase_until", Value: 1}},
			Options: options.Index().SetSparse(true),
		},
	})
	return err
}

func (s *userStore) Create(ctx context.Context, u *User) error {
	res, err := s.col.InsertOne(ctx, u)
	if mongo.IsDuplicateKeyError(err) {
		return errUsernameTaken
	}
	if err != nil {
		return err
	}
	if oid, ok := res.InsertedID.(bson.ObjectID); ok {
		u.ID = oid
	}
	return nil
}

// GetByUsername finds a live account. A deleted one is not found: it cannot log
// in, be messaged, or be sent a friend request.
func (s *userStore) GetByUsername(ctx context.Context, name string) (*User, error) {
	var u User
	err := s.col.FindOne(ctx, bson.M{"username": normaliseUsername(name), "deleted_at": alive}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// Search matches a substring of either name, which no index can serve: a pattern
// without an anchor has no range to seek to. Both Mattermost and Rocket.Chat scan
// here too — the user collection is orders of magnitude smaller than messages, and
// the limit bounds the work. The same query over messages would not be defensible.
// ProfileByUsername is GetByUsername with deleted accounts included, for the one
// place that has to answer for them: a message's author.
func (s *userStore) ProfileByUsername(ctx context.Context, name string) (*User, error) {
	var u User
	err := s.col.FindOne(ctx, bson.M{"username": normaliseUsername(name)},
		options.FindOne().SetProjection(bson.M{"password_hash": 0}),
	).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *userStore) Search(ctx context.Context, term string) ([]User, error) {
	term = strings.TrimPrefix(strings.TrimSpace(term), "@")
	if term == "" {
		return []User{}, nil
	}

	pattern := regexp.QuoteMeta(term)
	cur, err := s.col.Find(ctx,
		bson.M{
			"$or": []bson.M{
				{"username": bson.M{"$regex": strings.ToLower(pattern)}},
				{"display_name": bson.M{"$regex": pattern, "$options": "i"}},
			},
			"deleted_at": alive,
		},
		options.Find().
			SetProjection(bson.M{"password_hash": 0}).
			SetSort(bson.D{{Key: "username", Value: 1}}).
			SetLimit(searchLimit),
	)
	if err != nil {
		return nil, err
	}

	users := []User{}
	if err := cur.All(ctx, &users); err != nil {
		return nil, err
	}
	return users, nil
}

// UpdateProfile, SetLastSeen and SetAvatar leave a deleted account alone: a
// request that was already under way, or the socket closing because of the
// deletion itself, must not write back what the deletion erased.
func (s *userStore) UpdateProfile(ctx context.Context, id bson.ObjectID, displayName, bio string) error {
	_, err := s.col.UpdateOne(ctx,
		bson.M{"_id": id, "deleted_at": alive},
		bson.M{"$set": bson.M{"display_name": displayName, "bio": bio}},
	)
	return err
}

func (s *userStore) SetLastSeen(ctx context.Context, id bson.ObjectID, at time.Time) error {
	_, err := s.col.UpdateOne(ctx,
		bson.M{"_id": id, "deleted_at": alive},
		bson.M{"$set": bson.M{"last_seen_at": at}},
	)
	return err
}

// LastSeenByIDs answers for the users that are offline right now. Those still
// online are not asked about: they are seen at this very moment.
func (s *userStore) LastSeenByIDs(ctx context.Context, ids []bson.ObjectID) (map[string]time.Time, error) {
	cur, err := s.col.Find(ctx,
		bson.M{"_id": bson.M{"$in": ids}, "last_seen_at": bson.M{"$exists": true}},
		options.Find().SetProjection(bson.M{"last_seen_at": 1}),
	)
	if err != nil {
		return nil, err
	}

	var rows []struct {
		ID         bson.ObjectID `bson:"_id"`
		LastSeenAt time.Time     `bson:"last_seen_at"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}

	out := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		out[row.ID.Hex()] = row.LastSeenAt
	}
	return out, nil
}

// SetAvatar swaps the avatar in one step and hands back the one it replaced, so
// two uploads racing each other each delete the avatar they actually displaced.
// A nil avatar removes it.
func (s *userStore) SetAvatar(ctx context.Context, id bson.ObjectID, avatar *bson.ObjectID) (*bson.ObjectID, error) {
	update := bson.M{"$unset": bson.M{"avatar_id": ""}}
	if avatar != nil {
		update = bson.M{"$set": bson.M{"avatar_id": *avatar}}
	}

	var before User
	err := s.col.FindOneAndUpdate(ctx, bson.M{"_id": id, "deleted_at": alive}, update,
		options.FindOneAndUpdate().
			SetReturnDocument(options.Before).
			SetProjection(bson.M{"avatar_id": 1}),
	).Decode(&before)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return before.AvatarID, nil
}

// markDeleted is the commit point of a deletion, one write: the account can no
// longer log in, is no longer found, and holds nothing personal but the name and
// the picture, which the rest of the erasure takes away. The claim comes with
// the mark, so the request that made it finishes the job, and the purge loop
// only takes over once the claim runs out. False means it was deleted already.
func (s *userStore) markDeleted(ctx context.Context, id bson.ObjectID, owner string, now time.Time) (bool, error) {
	res, err := s.col.UpdateOne(ctx,
		bson.M{"_id": id, "deleted_at": alive},
		bson.M{
			"$set": bson.M{
				"deleted_at":   now,
				"display_name": "",
				"erase_owner":  owner,
				"erase_until":  now.Add(eraseLease),
			},
			"$unset": bson.M{"password_hash": "", "bio": "", "last_seen_at": ""},
		},
	)
	if err != nil {
		return false, fmt.Errorf("marking the account deleted: %w", err)
	}
	return res.MatchedCount == 1, nil
}

// claimErasure hands owner one account whose erasure was left unfinished, the
// way claimDiscarded hands out channels: taking a claim that has run out is one
// write, so two nodes never erase the same account at once.
func (s *userStore) claimErasure(ctx context.Context, owner string, now time.Time) (bson.ObjectID, bool, error) {
	var u struct {
		ID bson.ObjectID `bson:"_id"`
	}
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{"erase_until": bson.M{"$lte": now}},
		bson.M{"$set": bson.M{"erase_owner": owner, "erase_until": now.Add(eraseLease)}},
		options.FindOneAndUpdate().
			SetSort(bson.D{{Key: "erase_until", Value: 1}}).
			SetProjection(bson.M{"_id": 1}),
	).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return bson.ObjectID{}, false, nil
	}
	if err != nil {
		return bson.ObjectID{}, false, fmt.Errorf("claiming an erasure: %w", err)
	}
	return u.ID, true, nil
}

// erasing reads what the erasure still needs from a deleted account: the name
// it had, for the event that tells the chats, and its picture.
func (s *userStore) erasing(ctx context.Context, id bson.ObjectID) (User, error) {
	var u User
	err := s.col.FindOne(ctx,
		bson.M{"_id": id, "deleted_at": bson.M{"$exists": true}},
		options.FindOne().SetProjection(bson.M{"username": 1, "avatar_id": 1}),
	).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return User{}, errUserNotFound
	}
	return u, err
}

// finishErasure is the last write: the name goes to its tomb form, which lets
// the unique index give the old one to whoever asks next, and the claim is
// dropped with it. Only the node still holding the claim may do it. The name
// goes last because until every message carries the tomb name, a newcomer
// under the old one would be shown as their author.
func (s *userStore) finishErasure(ctx context.Context, id bson.ObjectID, owner string) (bool, error) {
	res, err := s.col.UpdateOne(ctx,
		bson.M{"_id": id, "erase_owner": owner},
		bson.M{
			"$set":   bson.M{"username": tombName(id)},
			"$unset": bson.M{"erase_owner": "", "erase_until": "", "avatar_id": ""},
		},
	)
	if err != nil {
		return false, fmt.Errorf("finishing the erasure: %w", err)
	}
	return res.MatchedCount == 1, nil
}
