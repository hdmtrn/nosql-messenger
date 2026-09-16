package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const (
	mediaMaxBytes  = 10 << 20
	avatarMaxBytes = 1 << 20

	mediaKindAvatar     = "avatar"
	mediaKindAttachment = "attachment"
)

var (
	errMediaNotFound    = errors.New("media not found")
	errUnsupportedMedia = errors.New("unsupported media type")
)

type Media struct {
	ID          bson.ObjectID  `bson:"_id"`
	FileID      bson.ObjectID  `bson:"file_id"`
	OwnerID     bson.ObjectID  `bson:"owner_id"`
	Kind        string         `bson:"kind"`
	ContentType string         `bson:"content_type"`
	Width       int            `bson:"width"`
	Height      int            `bson:"height"`
	ChannelID   *bson.ObjectID `bson:"channel_id,omitempty"`
	CreatedAt   time.Time      `bson:"created_at"`
}

type Attachment struct {
	ID          bson.ObjectID `bson:"id"           json:"id"`
	ContentType string        `bson:"content_type" json:"content_type"`
	Width       int           `bson:"width"        json:"width"`
	Height      int           `bson:"height"       json:"height"`
}

func (m Media) Attachment() Attachment {
	return Attachment{
		ID:          m.ID,
		ContentType: m.ContentType,
		Width:       m.Width,
		Height:      m.Height,
	}
}

type mediaStore struct {
	db  *mongo.Database
	col *mongo.Collection
}

func newMediaStore(db *mongo.Database) *mediaStore {
	return &mediaStore{db: db, col: db.Collection("media")}
}

func (s *mediaStore) bucket() *mongo.GridFSBucket {
	return s.db.GridFSBucket()
}

func (s *mediaStore) Save(ctx context.Context, owner bson.ObjectID, kind string, body io.Reader) (Media, error) {
	var head bytes.Buffer
	cfg, format, err := image.DecodeConfig(io.TeeReader(body, &head))
	if err != nil {
		return Media{}, errUnsupportedMedia
	}

	fileID, err := s.bucket().UploadFromStream(ctx, "", io.MultiReader(&head, body))
	if err != nil {
		return Media{}, fmt.Errorf("uploading file: %w", err)
	}

	m := Media{
		ID:          bson.NewObjectID(),
		FileID:      fileID,
		OwnerID:     owner,
		Kind:        kind,
		ContentType: "image/" + format,
		Width:       cfg.Width,
		Height:      cfg.Height,
		CreatedAt:   time.Now(),
	}
	if _, err := s.col.InsertOne(ctx, m); err != nil {
		_ = s.bucket().Delete(ctx, fileID)
		return Media{}, fmt.Errorf("inserting media: %w", err)
	}
	return m, nil
}

func (s *mediaStore) ByID(ctx context.Context, id bson.ObjectID) (Media, error) {
	var m Media
	err := s.col.FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Media{}, errMediaNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("looking up media: %w", err)
	}
	return m, nil
}

func (s *mediaStore) byIDs(ctx context.Context, filter bson.M, ids []bson.ObjectID) ([]Media, error) {
	filter["_id"] = bson.M{"$in": ids}
	cur, err := s.col.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("looking up media: %w", err)
	}
	var found []Media
	if err := cur.All(ctx, &found); err != nil {
		return nil, fmt.Errorf("decoding media: %w", err)
	}

	byID := make(map[bson.ObjectID]Media, len(found))
	for _, m := range found {
		byID[m.ID] = m
	}
	out := make([]Media, 0, len(ids))
	for _, id := range ids {
		m, ok := byID[id]
		if !ok {
			return nil, errMediaNotFound
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *mediaStore) Attach(ctx context.Context, ids []bson.ObjectID, owner, channel bson.ObjectID) ([]Attachment, error) {
	_, err := s.col.UpdateMany(ctx,
		bson.M{
			"_id":      bson.M{"$in": ids},
			"owner_id": owner,
			"kind":     mediaKindAttachment,
			"$or": []bson.M{
				{"channel_id": bson.M{"$exists": false}},
				{"channel_id": channel},
			},
		},
		bson.M{"$set": bson.M{"channel_id": channel}},
	)
	if err != nil {
		return nil, fmt.Errorf("attaching media: %w", err)
	}

	attached, err := s.byIDs(ctx, bson.M{"owner_id": owner, "kind": mediaKindAttachment, "channel_id": channel}, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Attachment, 0, len(attached))
	for _, m := range attached {
		out = append(out, m.Attachment())
	}
	return out, nil
}

func (s *mediaStore) CopyTo(ctx context.Context, from []Attachment, owner, channel bson.ObjectID) ([]Attachment, error) {
	ids := make([]bson.ObjectID, 0, len(from))
	for _, a := range from {
		ids = append(ids, a.ID)
	}
	sources, err := s.byIDs(ctx, bson.M{}, ids)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	copies := make([]Media, 0, len(sources))
	out := make([]Attachment, 0, len(sources))
	for _, src := range sources {
		c := src
		c.ID = bson.NewObjectID()
		c.OwnerID = owner
		c.ChannelID = &channel
		c.CreatedAt = now
		copies = append(copies, c)
		out = append(out, c.Attachment())
	}
	if _, err := s.col.InsertMany(ctx, copies); err != nil {
		return nil, fmt.Errorf("copying media: %w", err)
	}
	return out, nil
}

func (s *mediaStore) Delete(ctx context.Context, id bson.ObjectID) error {
	var m Media
	err := s.col.FindOneAndDelete(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return errMediaNotFound
	}
	if err != nil {
		return fmt.Errorf("deleting media: %w", err)
	}
	if err := s.bucket().Delete(ctx, m.FileID); err != nil {
		return fmt.Errorf("deleting file: %w", err)
	}
	return nil
}

func (s *mediaStore) Open(ctx context.Context, m Media) (*mongo.GridFSDownloadStream, error) {
	ds, err := s.bucket().OpenDownloadStream(ctx, m.FileID)
	if errors.Is(err, mongo.ErrFileNotFound) {
		return nil, errMediaNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	return ds, nil
}
