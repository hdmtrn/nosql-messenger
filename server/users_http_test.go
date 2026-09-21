package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Messages embed the username only, so a new display name or picture has to be
// announced; otherwise the people in a channel see it after a reload.
func TestProfileChangeReachesTheChannel(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	users := newUserStore(db)
	channels := newChannelStore(db)

	hub := NewHub()
	s := &server{hub: hub, bus: hub, users: users, channels: channels}

	mara := Session{UserID: bson.NewObjectID(), Username: "mara"}
	if err := users.Create(ctx, &User{ID: mara.UserID, Username: mara.Username, DisplayName: "mara"}); err != nil {
		t.Fatalf("creating the user: %v", err)
	}
	ch, err := channels.Create(ctx, "general", mara)
	if err != nil {
		t.Fatalf("creating the channel: %v", err)
	}

	watcher := newSubscriber(bson.NewObjectID().Hex())
	hub.Connect(watcher, []string{ch.ID.Hex()})

	body := bytes.NewReader([]byte(`{"display_name":"Mara P","bio":"hi"}`))
	r := httptest.NewRequest(http.MethodPost, "/auth/me/profile", body)
	w := httptest.NewRecorder()
	s.handleUpdateProfile(w, r, mara)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}

	select {
	case raw := <-watcher.send:
		var ev profileEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("decoding the event: %v", err)
		}
		if ev.Type != "profile" || ev.User.Username != mara.Username || ev.User.DisplayName != "Mara P" {
			t.Fatalf("event %+v, want mara renamed", ev)
		}
	default:
		t.Fatal("the channel heard nothing about the new name")
	}
}

// A stranger may read a profile — that is how anyone is found at all, and the
// only way in besides an invite code — but when the person was last online is
// not part of it. The field sits on User for the presence handler to fill, and
// left through these two handlers along with it.
func TestProfileOfAnotherUserCarriesNoLastSeen(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	users := newUserStore(db)
	s := &server{users: users}

	seen := time.Now().UTC()
	mara := &User{
		ID:          bson.NewObjectID(),
		Username:    "mara",
		DisplayName: "mara",
		LastSeenAt:  &seen,
	}
	if err := users.Create(ctx, mara); err != nil {
		t.Fatalf("creating the user: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/users/mara", nil)
	r.SetPathValue("username", "mara")
	w := httptest.NewRecorder()
	s.handleGetUser(w, r, Session{})
	if w.Code != http.StatusOK {
		t.Fatalf("fetching the profile gave %d, want 200", w.Code)
	}

	var one map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &one); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.Bytes(), err)
	}
	if _, ok := one["last_seen_at"]; ok {
		t.Fatalf("the profile carries last_seen_at: %s", w.Body.Bytes())
	}
	// The rest stays public on purpose: without it nobody can be found to befriend.
	if one["username"] != "mara" {
		t.Fatalf("the profile has username %v, want mara", one["username"])
	}

	// Search answers with the same struct, so it leaks the same field.
	r = httptest.NewRequest(http.MethodGet, "/users?q=mar", nil)
	w = httptest.NewRecorder()
	s.handleSearchUsers(w, r, Session{})
	if w.Code != http.StatusOK {
		t.Fatalf("searching gave %d, want 200", w.Code)
	}

	var many []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &many); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.Bytes(), err)
	}
	if len(many) == 0 {
		t.Fatal("search found nobody, want mara")
	}
	for _, u := range many {
		if _, ok := u["last_seen_at"]; ok {
			t.Fatalf("a search result carries last_seen_at: %s", w.Body.Bytes())
		}
	}
}
