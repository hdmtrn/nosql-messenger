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
	messageMaxLen    = 4000
	messagesPageSize = 50
	messagesMaxLimit = 100
)

type MessageAuthor struct {
	ID       bson.ObjectID `bson:"id"       json:"id"`
	Username string        `bson:"username" json:"username"`
}

type Message struct {
	ID          bson.ObjectID `bson:"_id,omitempty"           json:"id"`
	ChannelID   bson.ObjectID `bson:"channel_id"              json:"channel_id"`
	Author      MessageAuthor `bson:"author"                  json:"author"`
	Text        string        `bson:"text"                    json:"text"`
	CreatedAt   time.Time     `bson:"created_at"              json:"created_at"`
	ClientMsgID string        `bson:"client_msg_id,omitempty" json:"client_msg_id,omitempty"`
}

var (
	errDuplicateMessage = errors.New("message with this client_msg_id already exists")
	errMessageNotFound  = errors.New("message not found")
)

type messageStore struct {
	col *mongo.Collection
}

func newMessageStore(db *mongo.Database) *messageStore {
	return &messageStore{col: db.Collection("messages")}
}

func (s *messageStore) ensureIndexes(ctx context.Context) error {
	_, err := s.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "channel_id", Value: 1}, {Key: "_id", Value: 1}},
		},
		{
			Keys:    bson.D{{Key: "client_msg_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true),
		},
	})
	return err
}

func (s *messageStore) Insert(ctx context.Context, channelID bson.ObjectID, author Session, text, clientMsgID string) (Message, error) {
	msg := Message{
		ChannelID: channelID,
		Author: MessageAuthor{
			ID:       author.UserID,
			Username: author.Username,
		},
		Text:        text,
		CreatedAt:   time.Now(),
		ClientMsgID: clientMsgID,
	}

	res, err := s.col.InsertOne(ctx, msg)
	if mongo.IsDuplicateKeyError(err) {
		return Message{}, errDuplicateMessage
	}
	if err != nil {
		return Message{}, fmt.Errorf("inserting message: %w", err)
	}
	if oid, ok := res.InsertedID.(bson.ObjectID); ok {
		msg.ID = oid
	}
	return msg, nil
}

func (s *messageStore) ByClientMsgID(ctx context.Context, clientMsgID string) (Message, error) {
	var msg Message
	err := s.col.FindOne(ctx, bson.M{"client_msg_id": clientMsgID}).Decode(&msg)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Message{}, errMessageNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("looking up message by client id: %w", err)
	}
	return msg, nil
}

func (s *messageStore) List(ctx context.Context, channelID bson.ObjectID, before bson.ObjectID, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = messagesPageSize
	}
	if limit > messagesMaxLimit {
		limit = messagesMaxLimit
	}

	filter := bson.M{"channel_id": channelID}
	if !before.IsZero() {
		filter["_id"] = bson.M{"$lt": before}
	}

	cur, err := s.col.Find(ctx, filter,
		options.Find().
			SetSort(bson.D{{Key: "_id", Value: -1}}).
			SetLimit(int64(limit)),
	)
	if err != nil {
		return nil, fmt.Errorf("listing messages: %w", err)
	}

	messages := []Message{}
	if err := cur.All(ctx, &messages); err != nil {
		return nil, fmt.Errorf("decoding messages: %w", err)
	}
	return messages, nil
}
