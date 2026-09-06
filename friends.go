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
	friendPending  = "pending"
	friendAccepted = "accepted"
	friendDeclined = "declined"

	friendsMaxLimit = 200
)

type FriendParty struct {
	ID       bson.ObjectID `bson:"id"       json:"id"`
	Username string        `bson:"username" json:"username"`
}

type FriendRequest struct {
	ID          bson.ObjectID `bson:"_id,omitempty"          json:"id"`
	From        FriendParty   `bson:"from"                   json:"from"`
	To          FriendParty   `bson:"to"                     json:"to"`
	Status      string        `bson:"status"                 json:"status"`
	CreatedAt   time.Time     `bson:"created_at"             json:"created_at"`
	RespondedAt *time.Time    `bson:"responded_at,omitempty" json:"responded_at,omitempty"`
}

var (
	errFriendSelf      = errors.New("cannot send a friend request to yourself")
	errFriendPending   = errors.New("a request between these users is already pending")
	errFriendAlready   = errors.New("these users are already friends")
	errRequestNotFound = errors.New("friend request not found")
)

type friendStore struct {
	col *mongo.Collection
}

func newFriendStore(db *mongo.Database) *friendStore {
	return &friendStore{col: db.Collection("friend_requests")}
}

func (s *friendStore) ensureIndexes(ctx context.Context) error {
	_, err := s.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "to.id", Value: 1}, {Key: "status", Value: 1}, {Key: "_id", Value: 1}}},
		{Keys: bson.D{{Key: "from.id", Value: 1}, {Key: "status", Value: 1}, {Key: "_id", Value: 1}}},
		{
			Keys: bson.D{{Key: "from.id", Value: 1}, {Key: "to.id", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetPartialFilterExpression(bson.M{"status": friendPending}),
		},
	})
	return err
}

func (s *friendStore) between(ctx context.Context, a, b bson.ObjectID, statuses ...string) (FriendRequest, error) {
	var req FriendRequest
	err := s.col.FindOne(ctx, bson.M{
		"status": bson.M{"$in": statuses},
		"$or": []bson.M{
			{"from.id": a, "to.id": b},
			{"from.id": b, "to.id": a},
		},
	}).Decode(&req)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return FriendRequest{}, errRequestNotFound
	}
	if err != nil {
		return FriendRequest{}, fmt.Errorf("looking up friendship: %w", err)
	}
	return req, nil
}

// Send creates a request, unless one is already open the other way round: answering
// an incoming request by sending your own is consent, not a second request.
func (s *friendStore) Send(ctx context.Context, from Session, to *User) (FriendRequest, error) {
	if from.UserID == to.ID {
		return FriendRequest{}, errFriendSelf
	}

	existing, err := s.between(ctx, from.UserID, to.ID, friendPending, friendAccepted)
	switch {
	case err == nil && existing.Status == friendAccepted:
		return FriendRequest{}, errFriendAlready
	case err == nil && existing.To.ID == from.UserID:
		return s.Respond(ctx, existing.ID, from, friendAccepted)
	case err == nil:
		return FriendRequest{}, errFriendPending
	case !errors.Is(err, errRequestNotFound):
		return FriendRequest{}, err
	}

	req := FriendRequest{
		From:      FriendParty{ID: from.UserID, Username: from.Username},
		To:        FriendParty{ID: to.ID, Username: to.Username},
		Status:    friendPending,
		CreatedAt: time.Now(),
	}

	res, err := s.col.InsertOne(ctx, req)
	if mongo.IsDuplicateKeyError(err) {
		return FriendRequest{}, errFriendPending
	}
	if err != nil {
		return FriendRequest{}, fmt.Errorf("creating friend request: %w", err)
	}
	if oid, ok := res.InsertedID.(bson.ObjectID); ok {
		req.ID = oid
	}
	return req, nil
}

// Respond settles a pending request. The recipient is part of the filter, so a
// request addressed to somebody else simply is not found.
func (s *friendStore) Respond(ctx context.Context, id bson.ObjectID, by Session, status string) (FriendRequest, error) {
	now := time.Now()
	var req FriendRequest
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{"_id": id, "to.id": by.UserID, "status": friendPending},
		bson.M{"$set": bson.M{"status": status, "responded_at": now}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&req)

	if errors.Is(err, mongo.ErrNoDocuments) {
		return FriendRequest{}, errRequestNotFound
	}
	if err != nil {
		return FriendRequest{}, fmt.Errorf("responding to friend request: %w", err)
	}
	return req, nil
}

func (s *friendStore) list(ctx context.Context, filter bson.M) ([]FriendRequest, error) {
	cur, err := s.col.Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(friendsMaxLimit))
	if err != nil {
		return nil, fmt.Errorf("listing friend requests: %w", err)
	}
	out := []FriendRequest{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decoding friend requests: %w", err)
	}
	return out, nil
}

func (s *friendStore) Incoming(ctx context.Context, userID bson.ObjectID) ([]FriendRequest, error) {
	return s.list(ctx, bson.M{"to.id": userID, "status": friendPending})
}

func (s *friendStore) Outgoing(ctx context.Context, userID bson.ObjectID) ([]FriendRequest, error) {
	return s.list(ctx, bson.M{"from.id": userID, "status": friendPending})
}

func (s *friendStore) Friends(ctx context.Context, userID bson.ObjectID) ([]FriendParty, error) {
	accepted, err := s.list(ctx, bson.M{
		"status": friendAccepted,
		"$or":    []bson.M{{"from.id": userID}, {"to.id": userID}},
	})
	if err != nil {
		return nil, err
	}

	friends := []FriendParty{}
	for _, r := range accepted {
		if r.From.ID == userID {
			friends = append(friends, r.To)
		} else {
			friends = append(friends, r.From)
		}
	}
	return friends, nil
}
