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
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	mediaMaxBytes  = 10 << 20
	avatarMaxBytes = 1 << 20

	mediaMaxSide   = 10_000
	mediaMaxPixels = 40_000_000

	mediaUnsentTTL     = 24 * time.Hour
	mediaSweepInterval = time.Hour

	mediaKindAvatar     = "avatar"
	mediaKindAttachment = "attachment"
)

var (
	errMediaNotFound    = errors.New("media not found")
	errUnsupportedMedia = errors.New("unsupported media type")
	errImageTooLarge    = errors.New("image is too large")
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
	ExpiresAt   *time.Time     `bson:"expires_at,omitempty"`
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
	db    *mongo.Database
	col   *mongo.Collection
	files *mongo.GridFSBucket
}

func newMediaStore(db *mongo.Database) *mediaStore {
	return &mediaStore{db: db, col: db.Collection("media"), files: db.GridFSBucket()}
}

func (s *mediaStore) ensureIndexes(ctx context.Context) error {
	_, err := s.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "expires_at", Value: 1}},
			Options: options.Index().SetPartialFilterExpression(
				bson.M{"expires_at": bson.M{"$exists": true}}),
		},
		// Serves dropping a discarded channel's pictures.
		{Keys: bson.D{{Key: "channel_id", Value: 1}}},
		// Serves "does anything still point at these bytes" before a file is deleted.
		{Keys: bson.D{{Key: "file_id", Value: 1}}},
	})
	return err
}

// Save stores an image. channel is set only for a channel's avatar, which is
// then shown to that channel's members alone.
func (s *mediaStore) Save(ctx context.Context, owner bson.ObjectID, kind string, channel *bson.ObjectID, body io.Reader) (Media, error) {
	var head bytes.Buffer
	cfg, format, err := image.DecodeConfig(io.TeeReader(body, &head))
	if err != nil {
		return Media{}, errUnsupportedMedia
	}
	if cfg.Width > mediaMaxSide || cfg.Height > mediaMaxSide ||
		int64(cfg.Width)*int64(cfg.Height) > mediaMaxPixels {
		return Media{}, errImageTooLarge
	}

	fileID, err := s.db.GridFSBucket().UploadFromStream(ctx, "", io.MultiReader(&head, body))
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
		ChannelID:   channel,
		CreatedAt:   time.Now(),
	}
	if kind == mediaKindAttachment {
		expires := m.CreatedAt.Add(mediaUnsentTTL)
		m.ExpiresAt = &expires
	}
	if _, err := s.col.InsertOne(ctx, m); err != nil {
		_ = s.files.Delete(ctx, fileID)
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
		bson.M{
			"$set":   bson.M{"channel_id": channel},
			"$unset": bson.M{"expires_at": ""},
		},
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
	return s.deleteWhere(ctx, bson.M{"_id": id})
}

func (s *mediaStore) deleteWhere(ctx context.Context, filter bson.M) error {
	var m Media
	err := s.col.FindOneAndDelete(ctx, filter).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return errMediaNotFound
	}
	if err != nil {
		return fmt.Errorf("deleting media: %w", err)
	}
	if err := s.files.Delete(ctx, m.FileID); err != nil {
		return fmt.Errorf("deleting file: %w", err)
	}
	return nil
}

// deleteForChannel removes the channel's media records and returns the files
// they pointed at. It does not touch the files: a forwarded picture is a second
// record over the same bytes, so a file may still be in use elsewhere. Run
// inside the transaction that removes the channel.
func (s *mediaStore) deleteForChannel(ctx context.Context, channelID bson.ObjectID) ([]bson.ObjectID, error) {
	filter := bson.M{"channel_id": channelID}
	var fileIDs []bson.ObjectID
	if err := s.col.Distinct(ctx, "file_id", filter).Decode(&fileIDs); err != nil {
		return nil, fmt.Errorf("listing channel files: %w", err)
	}
	if _, err := s.col.DeleteMany(ctx, filter); err != nil {
		return nil, fmt.Errorf("deleting channel media: %w", err)
	}
	return fileIDs, nil
}

// DeleteUnreferencedFiles deletes those of the files no media record points at
// any more. A file whose last record is gone is garbage; one that a forwarded
// copy still names is kept.
func (s *mediaStore) DeleteUnreferencedFiles(ctx context.Context, fileIDs []bson.ObjectID) error {
	for _, id := range fileIDs {
		n, err := s.col.CountDocuments(ctx, bson.M{"file_id": id}, options.Count().SetLimit(1))
		if err != nil {
			return fmt.Errorf("counting references to file %s: %w", id.Hex(), err)
		}
		if n > 0 {
			continue
		}
		err = s.files.Delete(ctx, id)
		if err != nil && !errors.Is(err, mongo.ErrFileNotFound) {
			return fmt.Errorf("deleting file %s: %w", id.Hex(), err)
		}
	}
	return nil
}

func (s *mediaStore) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	cur, err := s.col.Find(ctx,
		bson.M{"expires_at": bson.M{"$lte": now}},
		options.Find().SetProjection(bson.M{"_id": 1}),
	)
	if err != nil {
		return 0, fmt.Errorf("finding expired media: %w", err)
	}
	var expired []struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := cur.All(ctx, &expired); err != nil {
		return 0, fmt.Errorf("decoding expired media: %w", err)
	}

	deleted := 0
	for _, e := range expired {
		err := s.deleteWhere(ctx, bson.M{"_id": e.ID, "expires_at": bson.M{"$lte": now}})
		if errors.Is(err, errMediaNotFound) {
			continue
		}
		if err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (s *mediaStore) sweepExpired(ctx context.Context) {
	ticker := time.NewTicker(mediaSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			n, err := s.DeleteExpired(ctx, now)
			if err != nil {
				log.Printf("deleting unsent media: %v", err)
			}
			if n > 0 {
				log.Printf("deleted %d unsent media files", n)
			}
		}
	}
}

func (s *mediaStore) Open(ctx context.Context, m Media) (*mongo.GridFSDownloadStream, error) {
	ds, err := s.files.OpenDownloadStream(ctx, m.FileID)
	if errors.Is(err, mongo.ErrFileNotFound) {
		return nil, errMediaNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	return ds, nil
}
