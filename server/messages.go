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

	messageMaxAttachments = 10
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

	// The message this one answers. Only the id: the quote is filled in by the
	// client from the original, so an edited original shows its current text.
	ReplyTo *bson.ObjectID `bson:"reply_to,omitempty" json:"reply_to,omitempty"`

	Attachments []Attachment `bson:"attachments,omitempty" json:"attachments,omitempty"`
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
	// The unique index used to be on client_msg_id alone, which let an id made
	// up by one client collide with another's and answer with their message.
	// The compound one below replaces it, and Mongo names indexes after their
	// keys, so the old one has to go by name or it would stay and keep
	// enforcing the global uniqueness. A database that never had it answers
	// IndexNotFound (27), and one where nothing has been written yet has no
	// collection to look in either (NamespaceNotFound, 26).
	if err := s.col.Indexes().DropOne(ctx, "client_msg_id_1"); err != nil {
		var cmdErr mongo.CommandError
		if !errors.As(err, &cmdErr) || (cmdErr.Code != 26 && cmdErr.Code != 27) {
			return fmt.Errorf("dropping the old client_msg_id index: %w", err)
		}
	}

	_, err := s.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "channel_id", Value: 1}, {Key: "_id", Value: 1}},
		},
		{
			// Partial rather than sparse: a sparse compound index takes any
			// document holding one of its keys, and every message has a
			// channel_id, so the ones sent without a client_msg_id would all
			// index as {channel, null} and the second one in a channel would
			// be refused as a duplicate.
			Keys: bson.D{{Key: "channel_id", Value: 1}, {Key: "client_msg_id", Value: 1}},
			Options: options.Index().SetUnique(true).
				SetPartialFilterExpression(bson.M{"client_msg_id": bson.M{"$exists": true}}),
		},
	})
	return err
}

func authorOf(sess Session) MessageAuthor {
	return MessageAuthor{ID: sess.UserID, Username: sess.Username}
}

// Insert stores a message the caller has filled in and checked; the id and the
// time are the store's to set.
func (s *messageStore) Insert(ctx context.Context, msg Message) (Message, error) {
	msg.ID = bson.ObjectID{}
	msg.CreatedAt = time.Now()

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

// The channel is half of the lookup, as it is half of the index: a
// client_msg_id is the sender's own key for retrying, never a way to read a
// message out of a channel they are not in.
func (s *messageStore) ByClientMsgID(ctx context.Context, channelID bson.ObjectID, clientMsgID string) (Message, error) {
	var msg Message
	err := s.col.FindOne(ctx, bson.M{"channel_id": channelID, "client_msg_id": clientMsgID}).Decode(&msg)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Message{}, errMessageNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("looking up message by client id: %w", err)
	}
	return msg, nil
}

// ByIDs returns the messages of one channel whose ids are listed, in no particular
// order. The channel is part of the filter, so an id from another channel is not
// found, exactly like a missing one.
func (s *messageStore) ByIDs(ctx context.Context, channelID bson.ObjectID, ids []bson.ObjectID) ([]Message, error) {
	cur, err := s.col.Find(ctx, bson.M{
		"channel_id": channelID,
		"_id":        bson.M{"$in": ids},
	})
	if err != nil {
		return nil, fmt.Errorf("looking up messages by id: %w", err)
	}

	messages := []Message{}
	if err := cur.All(ctx, &messages); err != nil {
		return nil, fmt.Errorf("decoding messages: %w", err)
	}
	return messages, nil
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
