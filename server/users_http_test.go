package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
