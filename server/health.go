package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const readyTimeout = 2 * time.Second

// handleHealthz answers whether the process serves at all, and touches no
// database. The load balancer checks it, and in ECS a failed check replaces the
// task: a check that followed MongoDB would kill every node at the first hiccup
// of the database, which killing the nodes does nothing to cure.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz answers whether this node can do its work: store a message and
// reach the other nodes. It lives on the internal listener, since its answers
// name addresses and database errors.
func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()

	var mongoErr, redisErr error
	var wg sync.WaitGroup
	wg.Go(func() { mongoErr = s.writeReadiness(ctx) })
	wg.Go(func() { redisErr = s.redis.Ping(ctx).Err() })
	wg.Wait()

	body := map[string]string{"status": "ok", "mongo": "ok", "redis": "ok"}
	code := http.StatusOK
	if mongoErr != nil {
		body["mongo"], body["status"], code = mongoErr.Error(), "unavailable", http.StatusServiceUnavailable
	}
	if redisErr != nil {
		body["redis"], body["status"], code = redisErr.Error(), "unavailable", http.StatusServiceUnavailable
	}
	writeJSON(w, code, body)
}

// writeReadiness rewrites the one document of the health collection. A ping is
// answered by a database that cannot write (a full disk, a broken journal), and
// a messenger that cannot store a message is not ready, so the check writes,
// journaled like every message.
func (s *server) writeReadiness(ctx context.Context) error {
	_, err := s.health.UpdateOne(ctx,
		bson.M{"_id": "readiness"},
		bson.M{"$set": bson.M{"checked_at": time.Now()}},
		options.UpdateOne().SetUpsert(true))
	return err
}
