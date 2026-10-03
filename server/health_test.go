package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// The databases here are addresses nothing listens on: /healthz must answer
// anyway, since a check that followed them would get every node replaced at
// the first hiccup. And the public router must not answer readiness, whose
// failures name addresses and database errors.
func TestHealthzTouchesNoDatabase(t *testing.T) {
	health, rdb := unreachable(t)
	routes := (&server{health: health, redis: rdb}).routes()
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}

	if code, body := get("/healthz"); code != http.StatusOK || !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("/healthz answered %d %s", code, body)
	}
	for _, path := range []string{"/readyz", "/health"} {
		if _, body := get(path); strings.Contains(body, `"mongo"`) {
			t.Fatalf("the public router answered %s with readiness: %s", path, body)
		}
	}
}

// unreachable returns a collection and a Redis client whose servers are gone:
// nothing listens on port 1, and both give up at once instead of retrying.
func unreachable(t *testing.T) (*mongo.Collection, *redis.Client) {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1").
		SetServerSelectionTimeout(100 * time.Millisecond))
	if err != nil {
		t.Fatalf("a client that does not dial yet failed: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() {
		client.Disconnect(context.Background())
		rdb.Close()
	})
	return client.Database("unreachable").Collection("health"), rdb
}

func TestReadyz(t *testing.T) {
	ctx := context.Background()

	readyz := func(t *testing.T, s *server) (int, map[string]string) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.internalRoutes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unreadable answer %q: %v", rec.Body, err)
		}
		return rec.Code, body
	}
	_, bus := testInstance(t)
	deadMongo, deadRedis := unreachable(t)

	t.Run("ready", func(t *testing.T) {
		db := testDB(t)
		code, body := readyz(t, &server{health: db.Collection("health"), redis: bus.rdb})
		if code != http.StatusOK || body["mongo"] != "ok" || body["redis"] != "ok" {
			t.Fatalf("got %d %v, want 200 with both ok", code, body)
		}

		// It wrote, not merely pinged.
		var doc struct {
			CheckedAt time.Time `bson:"checked_at"`
		}
		if err := db.Collection("health").FindOne(ctx, bson.M{"_id": "readiness"}).Decode(&doc); err != nil {
			t.Fatalf("no readiness document: %v", err)
		}
		if time.Since(doc.CheckedAt) > time.Minute {
			t.Fatalf("checked_at %v is stale", doc.CheckedAt)
		}
	})

	// The case a ping misses: the database answers but refuses the write, as it
	// does when the disk is full. A validator no document passes stands in for
	// that here.
	t.Run("mongo answers a ping but cannot write", func(t *testing.T) {
		db := testDB(t)
		refuseAll := options.CreateCollection().SetValidator(bson.M{"never": bson.M{"$exists": true}})
		if err := db.CreateCollection(ctx, "health", refuseAll); err != nil {
			t.Fatalf("creating the collection: %v", err)
		}
		if err := db.Client().Ping(ctx, readpref.Primary()); err != nil {
			t.Fatalf("the ping this case relies on failed: %v", err)
		}

		code, body := readyz(t, &server{health: db.Collection("health"), redis: bus.rdb})
		if code != http.StatusServiceUnavailable || body["mongo"] == "ok" || body["redis"] != "ok" {
			t.Fatalf("got %d %v, want 503 naming mongo only", code, body)
		}
	})

	t.Run("mongo gone", func(t *testing.T) {
		code, body := readyz(t, &server{health: deadMongo, redis: bus.rdb})
		if code != http.StatusServiceUnavailable || body["mongo"] == "ok" || body["redis"] != "ok" {
			t.Fatalf("got %d %v, want 503 naming mongo only", code, body)
		}
	})

	t.Run("redis gone", func(t *testing.T) {
		db := testDB(t)
		code, body := readyz(t, &server{health: db.Collection("health"), redis: deadRedis})
		if code != http.StatusServiceUnavailable || body["mongo"] != "ok" || body["redis"] == "ok" {
			t.Fatalf("got %d %v, want 503 naming redis only", code, body)
		}
	})

	t.Run("both gone", func(t *testing.T) {
		code, body := readyz(t, &server{health: deadMongo, redis: deadRedis})
		if code != http.StatusServiceUnavailable || body["mongo"] == "ok" || body["redis"] == "ok" {
			t.Fatalf("got %d %v, want 503 naming both", code, body)
		}
	})
}
