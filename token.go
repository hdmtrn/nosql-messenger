package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const tokenTTL = 24 * time.Hour

var signingMethod = jwt.SigningMethodHS256

type tokenClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

func jwtSecret() ([]byte, error) {
	s := os.Getenv("JWT_SECRET")
	if s == "" {
		return nil, errors.New("JWT_SECRET environment variable is not set")
	}
	if len(s) < 32 {
		return nil, fmt.Errorf("JWT_SECRET is too short: %d characters, at least 32 required", len(s))
	}
	return []byte(s), nil
}

func issueToken(secret []byte, userID, username string) (string, error) {
	now := time.Now()
	c := tokenClaims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
		},
	}
	return jwt.NewWithClaims(signingMethod, c).SignedString(secret)
}

var errInvalidToken = errors.New("invalid token")

func parseToken(secret []byte, raw string) (*tokenClaims, error) {
	var c tokenClaims

	_, err := jwt.ParseWithClaims(raw, &c,
		func(*jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{signingMethod.Alg()}),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidToken, err)
	}
	return &c, nil
}
