package main

import (
	"context"
	"errors"
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
	ID           bson.ObjectID `bson:"_id,omitempty"  json:"id"`
	Username     string        `bson:"username"       json:"username"`
	DisplayName  string        `bson:"display_name"   json:"display_name"`
	PasswordHash string        `bson:"password_hash"  json:"-"`
	CreatedAt    time.Time     `bson:"created_at"     json:"created_at"`
}

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
	_, err := s.col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "username", Value: 1}},
		Options: options.Index().SetUnique(true),
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

func (s *userStore) GetByUsername(ctx context.Context, name string) (*User, error) {
	var u User
	err := s.col.FindOne(ctx, bson.M{"username": normaliseUsername(name)}).Decode(&u)
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
func (s *userStore) Search(ctx context.Context, term string) ([]User, error) {
	term = strings.TrimPrefix(strings.TrimSpace(term), "@")
	if term == "" {
		return []User{}, nil
	}

	pattern := regexp.QuoteMeta(term)
	cur, err := s.col.Find(ctx,
		bson.M{"$or": []bson.M{
			{"username": bson.M{"$regex": strings.ToLower(pattern)}},
			{"display_name": bson.M{"$regex": pattern, "$options": "i"}},
		}},
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

func (s *userStore) SetDisplayName(ctx context.Context, id bson.ObjectID, name string) error {
	_, err := s.col.UpdateOne(ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{"display_name": name}},
	)
	return err
}
