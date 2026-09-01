package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	sessionLength = 30 * 24 * time.Hour
	tokenBytes    = 32
	cacheTTL      = 10 * time.Minute
	sweepInterval = time.Minute
)

var touchThreshold = max(min(sessionLength/100, 24*time.Hour), 5*time.Minute)

type Session struct {
	ID       bson.ObjectID `bson:"_id,omitempty"`
	Token    string        `bson:"token"`
	UserID   bson.ObjectID `bson:"user_id"`
	Username string        `bson:"username"`

	CreatedAt      time.Time `bson:"created_at"`
	ExpiresAt      time.Time `bson:"expires_at"`
	LastActivityAt time.Time `bson:"last_activity_at"`
	UserAgent      string    `bson:"user_agent"`
}

func (s Session) expired() bool { return time.Now().After(s.ExpiresAt) }

var errSessionNotFound = errors.New("session not found or expired")

type cachedSession struct {
	sess     Session
	cachedAt time.Time
}

type sessionStore struct {
	col *mongo.Collection

	mu    sync.RWMutex
	cache map[string]cachedSession
}

func newSessionStore(db *mongo.Database) *sessionStore {
	return &sessionStore{
		col:   db.Collection("sessions"),
		cache: make(map[string]cachedSession),
	}
}

func (s *sessionStore) ensureIndexes(ctx context.Context) error {
	_, err := s.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "token", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "expires_at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(0),
		},
	})
	return err
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *sessionStore) put(sess Session) {
	s.mu.Lock()
	s.cache[sess.Token] = cachedSession{sess: sess, cachedAt: time.Now()}
	s.mu.Unlock()
}

func (s *sessionStore) evict(token string) {
	s.mu.Lock()
	delete(s.cache, token)
	s.mu.Unlock()
}

func (s *sessionStore) Create(ctx context.Context, u *User, userAgent string) (Session, error) {
	token, err := newToken()
	if err != nil {
		return Session{}, err
	}
	now := time.Now()
	sess := Session{
		Token:          token,
		UserID:         u.ID,
		Username:       u.Username,
		CreatedAt:      now,
		ExpiresAt:      now.Add(sessionLength),
		LastActivityAt: now,
		UserAgent:      userAgent,
	}

	res, err := s.col.InsertOne(ctx, sess)
	if err != nil {
		return Session{}, fmt.Errorf("creating session: %w", err)
	}
	if oid, ok := res.InsertedID.(bson.ObjectID); ok {
		sess.ID = oid
	}

	s.put(sess)
	return sess, nil
}

func (s *sessionStore) ByToken(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, errSessionNotFound
	}

	s.mu.RLock()
	entry, hit := s.cache[token]
	s.mu.RUnlock()

	sess := entry.sess
	if !hit || time.Since(entry.cachedAt) >= cacheTTL {
		if err := s.col.FindOne(ctx, bson.M{"token": token}).Decode(&sess); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				s.evict(token)
				return Session{}, errSessionNotFound
			}
			return Session{}, fmt.Errorf("looking up session: %w", err)
		}
		s.put(sess)
	}

	if sess.expired() {
		s.Delete(ctx, token)
		return Session{}, errSessionNotFound
	}

	s.touch(ctx, sess)
	return sess, nil
}

func (s *sessionStore) touch(ctx context.Context, sess Session) {
	now := time.Now()
	if now.Sub(sess.LastActivityAt) < touchThreshold {
		return
	}

	sess.LastActivityAt = now
	sess.ExpiresAt = now.Add(sessionLength)

	_, err := s.col.UpdateOne(ctx,
		bson.M{"token": sess.Token},
		bson.M{"$set": bson.M{
			"last_activity_at": sess.LastActivityAt,
			"expires_at":       sess.ExpiresAt,
		}})
	if err != nil {
		return
	}

	s.put(sess)
}

func (s *sessionStore) Delete(ctx context.Context, token string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"token": token})
	s.evict(token)
	return err
}

func (s *sessionStore) sweepCache(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			for token, entry := range s.cache {
				if time.Since(entry.cachedAt) >= cacheTTL {
					delete(s.cache, token)
				}
			}
			s.mu.Unlock()
		}
	}
}
