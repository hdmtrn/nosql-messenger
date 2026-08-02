package main

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testSecret = []byte("test-secret-that-is-longer-than-32-chars")

func TestTokenRoundTrip(t *testing.T) {
	raw, err := issueToken(testSecret, "507f1f77bcf86cd799439011", "alice")
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseToken(testSecret, raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "507f1f77bcf86cd799439011" {
		t.Errorf("sub = %q", c.Subject)
	}
	if c.Username != "alice" {
		t.Errorf("username = %q", c.Username)
	}
}

func TestRejectWrongSecret(t *testing.T) {
	raw, _ := issueToken(testSecret, "id", "alice")
	if _, err := parseToken([]byte("another-secret-also-longer-than-32-chars"), raw); err == nil {
		t.Error("token accepted when signed with a different secret")
	}
}

func TestPayloadIsReadableWithoutSecret(t *testing.T) {
	raw, _ := issueToken(testSecret, "id", "alice")
	payload := strings.Split(raw, ".")[1]
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), "alice") {
		t.Errorf("username not found in the unencrypted payload: %s", decoded)
	}
}

func TestRejectExpired(t *testing.T) {
	c := tokenClaims{
		Username: "alice",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "id",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	raw, err := jwt.NewWithClaims(signingMethod, c).SignedString(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseToken(testSecret, raw); err == nil {
		t.Error("expired token was accepted")
	}
}

func TestRejectAlgNone(t *testing.T) {
	b64 := base64.RawURLEncoding.EncodeToString
	header := b64([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := b64([]byte(`{"sub":"id","username":"admin","exp":99999999999}`))
	forged := header + "." + payload + "."

	if _, err := parseToken(testSecret, forged); err == nil {
		t.Fatal("token with alg:none was accepted: protection is not working")
	}
}
