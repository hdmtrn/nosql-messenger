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
	mediaMaxBytes = 10 << 20

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
	col    *mongo.Collection
	bucket *mongo.GridFSBucket
}

func newMediaStore(db *mongo.Database) *mediaStore {
	return &mediaStore{
		col:    db.Collection("media"),
		bucket: db.GridFSBucket(),
	}
}

func (s *mediaStore) Save(ctx context.Context, owner bson.ObjectID, kind string, body io.Reader) (Media, error) {
	var head bytes.Buffer
	cfg, format, err := image.DecodeConfig(io.TeeReader(body, &head))
	if err != nil {
		return Media{}, errUnsupportedMedia
	}

	fileID, err := s.bucket.UploadFromStream(ctx, "", io.MultiReader(&head, body))
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
		_ = s.bucket.Delete(ctx, fileID)
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

func (s *mediaStore) Open(ctx context.Context, m Media) (*mongo.GridFSDownloadStream, error) {
	ds, err := s.bucket.OpenDownloadStream(ctx, m.FileID)
	if errors.Is(err, mongo.ErrFileNotFound) {
		return nil, errMediaNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	return ds, nil
}
