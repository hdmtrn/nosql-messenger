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
	// Counts invalidations. A lookup notes it before asking the database and writes
	// the answer to the cache only if it has not moved: an invalidation in between
	// means the session may have been deleted while the answer was on its way,
	// and caching it then would bring a revoked session back for cacheTTL. One
	// counter for the whole store rather than one per token, which would have
	// to be cleaned up: an unrelated invalidation costs only a skipped write, i.e.
	// one more database lookup next time.
	invalidations uint64
	// Runs just before a lookup writes to the cache, for tests that need a
	// revocation to land exactly there. Nil outside tests.
	beforeCache func()

	// Called after a session is gone from the database, so that the other
	// instances drop it from their caches too. Without it a revoked session
	// would keep working elsewhere for up to cacheTTL, which would take away
	// the immediate revocation that server-side sessions were chosen for.
	onRevoked func(sessionID bson.ObjectID)
}

func newSessionStore(db *mongo.Database) *sessionStore {
	return &sessionStore{
		col:       db.Collection("sessions"),
		cache:     make(map[string]cachedSession),
		onRevoked: func(bson.ObjectID) {},
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
		{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "_id", Value: 1}}},
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

// putIfCurrent caches what a lookup read, unless something was invalidated
// since the lookup noted the counter (seen). The check and the write share the
// lock with invalidate, so a revocation either comes first and the write is
// skipped, or comes after and removes what was written.
func (s *sessionStore) putIfCurrent(sess Session, seen uint64) {
	if s.beforeCache != nil {
		s.beforeCache()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invalidations != seen {
		return
	}
	s.cache[sess.Token] = cachedSession{sess: sess, cachedAt: time.Now()}
}

// invalidate is for a session we have just deleted: it may be on its way into
// the cache in a lookup that read it a moment earlier, so the counter moves.
func (s *sessionStore) invalidate(token string) {
	s.mu.Lock()
	delete(s.cache, token)
	s.invalidations++
	s.mu.Unlock()
}

// forget drops the cache entry of a token the database does not know, and
// leaves the counter alone. There is no deletion of ours for a lookup in
// flight to undo: whoever removed the document — Revoke, Delete, expiry going
// through Delete — moved the counter when it did. Counting here would let
// anyone move it at will: every request with a made-up cookie, on any route,
// reaches this, and a counter that never stands still keeps the cache from
// filling at all.
func (s *sessionStore) forget(token string) {
	s.mu.Lock()
	delete(s.cache, token)
	s.mu.Unlock()
}

// invalidateByID is what an instance does when it hears about a revocation
// elsewhere: it has the session id but not the token the cache is keyed by. The
// scan is over the sessions cached on this node and happens only on a
// revocation, which is rare — a second index kept in step on every login would
// cost more than it saves.
func (s *sessionStore) invalidateByID(id bson.ObjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.invalidations++
	for token, entry := range s.cache {
		if entry.sess.ID == id {
			delete(s.cache, token)
			return
		}
	}
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
	seen := s.invalidations
	s.mu.RUnlock()

	sess := entry.sess
	if !hit || time.Since(entry.cachedAt) >= cacheTTL {
		if err := s.col.FindOne(ctx, bson.M{"token": token}).Decode(&sess); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				s.forget(token)
				return Session{}, errSessionNotFound
			}
			return Session{}, fmt.Errorf("looking up session: %w", err)
		}
		s.putIfCurrent(sess, seen)
	}

	if sess.expired() {
		s.Delete(ctx, token)
		return Session{}, errSessionNotFound
	}

	s.touch(ctx, sess, seen)
	return sess, nil
}

func (s *sessionStore) touch(ctx context.Context, sess Session, seen uint64) {
	now := time.Now()
	if now.Sub(sess.LastActivityAt) < touchThreshold {
		return
	}

	sess.LastActivityAt = now
	sess.ExpiresAt = now.Add(sessionLength)

	res, err := s.col.UpdateOne(ctx,
		bson.M{"token": sess.Token},
		bson.M{"$set": bson.M{
			"last_activity_at": sess.LastActivityAt,
			"expires_at":       sess.ExpiresAt,
		}})
	// Matching nothing is not an error to the driver: the session was deleted
	// under us, and the extended copy must not go back into the cache.
	if err != nil || res.MatchedCount == 0 {
		return
	}

	s.putIfCurrent(sess, seen)
}

// Delete signs one device out. It deletes by token but reads the document back,
// because the announcement to the other instances carries the id: they must drop
// the session from their caches, and the token has no business travelling.
func (s *sessionStore) Delete(ctx context.Context, token string) error {
	var sess Session
	err := s.col.FindOneAndDelete(ctx, bson.M{"token": token}).Decode(&sess)
	if errors.Is(err, mongo.ErrNoDocuments) {
		// Signing out with a token that is already gone is not a failure, and
		// not a deletion either: anyone can sign out with a made-up cookie.
		s.forget(token)
		return nil
	}
	// Deleted, or the database failed and we cannot tell: count it either way.
	s.invalidate(token)
	if err != nil {
		return err
	}

	s.onRevoked(sess.ID)
	return nil
}

// ForUser lists a user's live sessions. The token never leaves the server: it is
// the credential itself, and a device list has no use for it.
func (s *sessionStore) ForUser(ctx context.Context, userID bson.ObjectID) ([]Session, error) {
	cur, err := s.col.Find(ctx,
		bson.M{"user_id": userID},
		options.Find().
			SetProjection(bson.M{"token": 0}).
			SetSort(bson.D{{Key: "last_activity_at", Value: -1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	out := []Session{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decoding sessions: %w", err)
	}
	return out, nil
}

// Revoke removes one of this user's sessions. The owner is part of the filter, so
// somebody else's session is simply not found. Deleting returns the document, which
// carries the token we must drop from the cache — otherwise the revoked session would
// keep working until the cached copy expired.
func (s *sessionStore) Revoke(ctx context.Context, userID, sessionID bson.ObjectID) error {
	var sess Session
	err := s.col.FindOneAndDelete(ctx, bson.M{"_id": sessionID, "user_id": userID}).Decode(&sess)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return errSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("revoking session: %w", err)
	}
	s.invalidate(sess.Token)
	s.onRevoked(sess.ID)
	return nil
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
