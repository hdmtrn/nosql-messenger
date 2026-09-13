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

// ForwardedFrom is a snapshot of the source taken at forwarding time. A copy
// instead of a reference: reading a channel needs no second query, and the
// forward outlives the source channel being discarded.
type ForwardedFrom struct {
	MessageID bson.ObjectID `bson:"message_id" json:"message_id"`
	ChannelID bson.ObjectID `bson:"channel_id" json:"channel_id"`
	Author    MessageAuthor `bson:"author"     json:"author"`
	CreatedAt time.Time     `bson:"created_at" json:"created_at"`
}

type Message struct {
	ID          bson.ObjectID `bson:"_id,omitempty"           json:"id"`
	ChannelID   bson.ObjectID `bson:"channel_id"              json:"channel_id"`
	Author      MessageAuthor `bson:"author"                  json:"author"`
	Text        string        `bson:"text"                    json:"text"`
	CreatedAt   time.Time     `bson:"created_at"              json:"created_at"`
	ClientMsgID string        `bson:"client_msg_id,omitempty" json:"client_msg_id,omitempty"`

	// A pointer, so that ordinary messages store no forwarded field at all.
	Forwarded *ForwardedFrom `bson:"forwarded,omitempty" json:"forwarded,omitempty"`
}

// forwardOf is what a copy of m carries about its source: a forward of a
// forward keeps pointing at the original, so chains never nest.
func (m Message) forwardOf() *ForwardedFrom {
	if m.Forwarded != nil {
		return m.Forwarded
	}
	return &ForwardedFrom{
		MessageID: m.ID,
		ChannelID: m.ChannelID,
		Author:    m.Author,
		CreatedAt: m.CreatedAt,
	}
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

func (s *messageStore) Insert(ctx context.Context, channelID bson.ObjectID, author Session, text, clientMsgID string, fwd *ForwardedFrom) (Message, error) {
	msg := Message{
		ChannelID: channelID,
		Author: MessageAuthor{
			ID:       author.UserID,
			Username: author.Username,
		},
		Text:        text,
		CreatedAt:   time.Now(),
		ClientMsgID: clientMsgID,
		Forwarded:   fwd,
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

func (s *messageStore) ByID(ctx context.Context, id bson.ObjectID) (Message, error) {
	var msg Message
	err := s.col.FindOne(ctx, bson.M{"_id": id}).Decode(&msg)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Message{}, errMessageNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("looking up message: %w", err)
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
