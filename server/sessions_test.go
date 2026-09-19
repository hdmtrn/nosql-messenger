package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// A lookup reads the session from MongoDB and then writes it to the node's
// cache. A revocation that lands between the two used to lose: the write came
// after the invalidation and brought the revoked session back for cacheTTL, so
// the node kept accepting it — new sockets included. Each case revokes at
// exactly that point and then asks again.
func TestRevokedMidLookupIsNotCachedAgain(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	for _, tc := range []struct {
		name string
		// warm puts the session in B's cache first, so that the lookup is a
		// cache hit and the write comes from touch, not from the database read.
		warm   bool
		revoke func(a, b *sessionStore, sess Session)
	}{
		{"revoked on this node", false, func(a, b *sessionStore, sess Session) {
			if err := b.Revoke(ctx, sess.UserID, sess.ID); err != nil {
				t.Fatalf("revoking: %v", err)
			}
		}},
		// The other node deletes it, and this one hears about it over the bus.
		{"revoked on another node", false, func(a, b *sessionStore, sess Session) {
			if err := a.Revoke(ctx, sess.UserID, sess.ID); err != nil {
				t.Fatalf("revoking: %v", err)
			}
			b.invalidateByID(sess.ID)
		}},
		{"revoked while touch extends it", true, func(a, b *sessionStore, sess Session) {
			if err := b.Revoke(ctx, sess.UserID, sess.ID); err != nil {
				t.Fatalf("revoking: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Two stores over one database are two nodes, as in the bus tests.
			a, b := newSessionStore(db), newSessionStore(db)
			user := &User{ID: bson.NewObjectID(), Username: "alice"}
			sess, err := a.Create(ctx, user, "a browser")
			if err != nil {
				t.Fatalf("creating a session: %v", err)
			}

			if tc.warm {
				// Last used a day ago, so the lookup extends it.
				stale := sess
				stale.LastActivityAt = time.Now().Add(-24 * time.Hour)
				b.put(stale)
			}

			b.beforeCache = func() {
				b.beforeCache = nil
				tc.revoke(a, b, sess)
			}
			// This lookup began before the revocation and may still succeed; it
			// is the next one that must not.
			if _, err := b.ByToken(ctx, sess.Token); err != nil {
				t.Fatalf("the lookup the revocation raced: %v", err)
			}
			if b.beforeCache != nil {
				t.Fatal("the lookup never reached the cache write, so nothing raced")
			}

			if _, err := b.ByToken(ctx, sess.Token); !errors.Is(err, errSessionNotFound) {
				t.Fatalf("after the revocation the node still accepts the session (err = %v)", err)
			}
		})
	}
}

// Any request with a made-up cookie, on any route, looks the token up and
// finds nothing; signing out with one needs no session at all. If that moved
// the counter, a stream of such requests would keep every real lookup from
// caching what it read. Only a deletion of a real session may move it.
func TestMadeUpTokensDoNotKeepTheCacheFromFilling(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	a, b := newSessionStore(db), newSessionStore(db)

	user := &User{ID: bson.NewObjectID(), Username: "bob"}
	sess, err := a.Create(ctx, user, "a browser")
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}

	// While Bob's lookup is at the database, someone sends made-up cookies.
	b.beforeCache = func() {
		b.beforeCache = nil
		for i := range 20 {
			token := fmt.Sprintf("made-up-%d", i)
			if _, err := b.ByToken(ctx, token); !errors.Is(err, errSessionNotFound) {
				t.Fatalf("a made-up token was not refused: %v", err)
			}
			if err := b.Delete(ctx, token); err != nil {
				t.Fatalf("signing out with a made-up token: %v", err)
			}
		}
	}
	if _, err := b.ByToken(ctx, sess.Token); err != nil {
		t.Fatalf("Bob's lookup: %v", err)
	}
	b.mu.RLock()
	_, cached := b.cache[sess.Token]
	b.mu.RUnlock()
	if !cached {
		t.Fatal("made-up tokens kept Bob's session out of the cache")
	}

	// A real deletion still counts: signing out, then a revocation elsewhere.
	before := b.invalidations
	if err := b.Delete(ctx, sess.Token); err != nil {
		t.Fatalf("signing out: %v", err)
	}
	b.invalidateByID(bson.NewObjectID())
	if got := b.invalidations - before; got != 2 {
		t.Fatalf("a sign-out and a revocation moved the counter by %d, want 2", got)
	}
}
