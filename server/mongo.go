package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

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

// mongoCredential reads the login from the environment: none set means no
// login, as the local stack runs. Kept out of MONGO_URI, whose address is no
// secret and changes with every deployment, and which would need the password
// escaped.
func mongoCredential() (*options.Credential, error) {
	user, pass := os.Getenv("MONGO_USERNAME"), os.Getenv("MONGO_PASSWORD")
	switch {
	case user == "" && pass == "":
		return nil, nil
	case user == "" || pass == "":
		return nil, errors.New("MONGO_USERNAME and MONGO_PASSWORD must be set together")
	}
	// Both the root user and the application's own live in admin.
	return &options.Credential{Username: user, Password: pass, AuthSource: "admin"}, nil
}

func connectMongo(ctx context.Context) (*mongo.Client, error) {
	// A standalone mongod acknowledges a write before it reaches the journal.
	// j:true makes an acknowledged message survive a crash, and it is announced
	// to the other nodes only after that acknowledgement.
	opts := options.Client().ApplyURI(mongoURI()).SetWriteConcern(writeconcern.Journaled())
	cred, err := mongoCredential()
	if err != nil {
		return nil, err
	}
	if cred != nil {
		opts.SetAuth(*cred)
	}
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
