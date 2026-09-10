package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")

	if err := s.mongo.Ping(ctx, readpref.Primary()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"status":"degraded","mongo":%q}`, err.Error())
		return
	}
	w.Write([]byte(`{"status":"ok","mongo":"ok"}`))
}
