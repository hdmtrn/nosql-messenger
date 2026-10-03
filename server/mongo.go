package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

func mongoURI() string {
	if uri := os.Getenv("MONGO_URI"); uri != "" {
		return uri
	}

	return "mongodb://localhost:27017"
}

func connectMongo(ctx context.Context) (*mongo.Client, error) {
	// A standalone mongod acknowledges a write before it reaches the journal.
	// j:true makes an acknowledged message survive a crash, and it is announced
	// to the other nodes only after that acknowledgement.
	opts := options.Client().ApplyURI(mongoURI()).SetWriteConcern(writeconcern.Journaled())
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("connecting to MongoDB: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("ping MongoDB: %w", err)
	}
	return client, nil
}

// replicaSetLeftover reports a volume written while MongoDB still ran as a
// replica set. Started standalone, mongod keeps the TTL monitor off for such
// data, so expired sessions stay in the database without any error.
func replicaSetLeftover(ctx context.Context, client *mongo.Client) bool {
	n, err := client.Database("local").Collection("system.replset").CountDocuments(ctx, bson.M{})
	return err == nil && n > 0
}
