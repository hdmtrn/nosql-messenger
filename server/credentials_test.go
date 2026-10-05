package main

import "testing"

// Locally neither variable is set and the client logs in as nobody. One
// without the other is a mistake, refused at startup rather than left to
// surface as a puzzling authentication error.
func TestMongoCredentialFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name, user, pass  string
		wantNone, wantErr bool
	}{
		{name: "neither", wantNone: true},
		{name: "both", user: "messenger", pass: "secret"},
		{name: "user only", user: "messenger", wantErr: true},
		{name: "password only", pass: "secret", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MONGO_USERNAME", tc.user)
			t.Setenv("MONGO_PASSWORD", tc.pass)

			cred, err := mongoCredential()
			switch {
			case tc.wantErr:
				if err == nil {
					t.Fatalf("got %+v, want an error", cred)
				}
			case tc.wantNone:
				if err != nil || cred != nil {
					t.Fatalf("got %+v, %v, want no credential", cred, err)
				}
			default:
				if err != nil || cred == nil {
					t.Fatalf("got %+v, %v, want a credential", cred, err)
				}
				if cred.Username != tc.user || cred.Password != tc.pass || cred.AuthSource != "admin" {
					t.Fatalf("got %+v, want %s in admin", cred, tc.user)
				}
			}
		})
	}
}

func TestRedisPasswordFromEnv(t *testing.T) {
	t.Setenv("REDIS_PASSWORD", "")
	if p := redisOptions().Password; p != "" {
		t.Fatalf("an unset REDIS_PASSWORD sent %q", p)
	}
	t.Setenv("REDIS_PASSWORD", "secret")
	if p := redisOptions().Password; p != "secret" {
		t.Fatalf("got password %q, want secret", p)
	}
}
