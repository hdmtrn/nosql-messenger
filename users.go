package main

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type User struct {
	ID           bson.ObjectID `bson:"_id,omitempty"`
	Username     string        `bson:"username"`
	PasswordHash string        `bson:"password_hash"`
	CreatedAt    time.Time     `bson:"created_at"`
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
	err := s.col.FindOne(ctx, bson.M{"username": name}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}
